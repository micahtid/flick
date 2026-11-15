package chunk

// Store saves and reads whole files. Each file is built from chunks, and the
// files table in Postgres remembers which chunks make it up, in order.

import (
	"context"
	"errors"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Schema creates the files table. IF NOT EXISTS makes it safe to run on every
// start. The API runs it, along with its own tables. A path names a file, such
// as uploads/<id>.mp4 or hls/<id>/720p_003.ts.
const Schema = `CREATE TABLE IF NOT EXISTS files (
  path   TEXT PRIMARY KEY,
  chunks TEXT[] NOT NULL
)`

// ErrNotFound means no file has that path.
var ErrNotFound = errors.New("file not found")

// Store saves and reads files, keeping the bytes on the nodes and the list of
// chunks in Postgres.
type Store struct {
	db     *pgxpool.Pool
	chunks *Client
}

func NewStore(db *pgxpool.Pool, chunks *Client) *Store {
	return &Store{db: db, chunks: chunks}
}

// Save stores r as the file at path. Saving a path again replaces the file,
// so a retried job can simply save its files again.
func (s *Store) Save(ctx context.Context, path string, r io.Reader) error {
	ids, err := s.chunks.Put(ctx, r)
	if err != nil {
		return err
	}
	// ON CONFLICT turns the insert into an update when the path is taken.
	_, err = s.db.Exec(ctx,
		`INSERT INTO files (path, chunks) VALUES ($1, $2)
		 ON CONFLICT (path) DO UPDATE SET chunks = EXCLUDED.chunks`,
		path, ids)
	return err
}

// Copy writes the file at path to w. It returns ErrNotFound before writing
// anything if there's no such file.
func (s *Store) Copy(ctx context.Context, w io.Writer, path string) error {
	var ids []string
	err := s.db.QueryRow(ctx, `SELECT chunks FROM files WHERE path = $1`, path).Scan(&ids)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return s.chunks.Get(ctx, w, ids)
}

// Delete forgets the file at path. Its chunks stay on the nodes, since
// another file may share them.
func (s *Store) Delete(ctx context.Context, path string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM files WHERE path = $1`, path)
	return err
}
