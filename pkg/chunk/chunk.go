// Package chunk stores files on the storage nodes. It cuts each file into
// 4 MB chunks, sends every chunk to all the nodes, and puts the chunks back
// together when the file is read. The API and the workers both use it.
package chunk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Size is the largest a chunk can be: 4 MB (4 × 2²⁰ bytes). The last chunk of
// a file is usually smaller.
const Size = 4 << 20

// ID is a chunk's name: the SHA-256 fingerprint of its bytes, written as 64
// hex characters. Because the name is the fingerprint, a node can check a
// chunk isn't damaged with nothing else stored.
func ID(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Client sends chunks to the storage nodes and fetches them back.
type Client struct {
	nodes []string // These are the node addresses, such as http://node1:9001.
	http  *http.Client
}

// New makes a client for the nodes in a comma-separated list, the way the
// STORAGE_NODES environment variable holds them.
func New(nodes string) *Client {
	return &Client{
		nodes: strings.Split(nodes, ","),
		// Give up on a node after 30 seconds, so a stuck node can't hold an
		// upload open forever.
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// Put cuts r into chunks, sends each one to the nodes, and returns the chunk
// IDs in order. Only one chunk is held in memory at a time.
func (c *Client) Put(ctx context.Context, r io.Reader) ([]string, error) {
	var ids []string
	buf := make([]byte, Size)
	for {
		n, err := io.ReadFull(r, buf) // Read up to 4 MB.
		if n > 0 {
			id := ID(buf[:n])
			if err := c.writeAll(ctx, id, buf[:n]); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		// ReadFull reports EOF when nothing was left to read, and
		// ErrUnexpectedEOF when the last chunk was short. Both mean the end.
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return ids, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// writeAll sends one chunk to every node at the same time. It succeeds if most
// of them (2 of 3) save it, so one dead node doesn't stop uploads. It waits
// for every node to answer, since they all read from data, which Put reuses
// for the next chunk.
func (c *Client) writeAll(ctx context.Context, id string, data []byte) error {
	errs := make([]error, len(c.nodes))
	var wg sync.WaitGroup
	for i, node := range c.nodes {
		wg.Go(func() { errs[i] = c.write(ctx, node, id, data) })
	}
	wg.Wait()

	var failed []error
	for _, err := range errs {
		if err != nil {
			failed = append(failed, err)
		}
	}
	if saved := len(c.nodes) - len(failed); saved <= len(c.nodes)/2 {
		return fmt.Errorf("chunk %s saved on %d of %d nodes: %w", id, saved, len(c.nodes), errors.Join(failed...))
	}
	return nil
}

// write sends one chunk to one node.
func (c *Client) write(ctx context.Context, node, id string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, node+"/chunks/"+id, bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("%s replied %s", node, resp.Status)
	}
	return nil
}

// Get writes the chunks to w in order. For each chunk, it asks the nodes one
// at a time and uses the first good answer, so the file can be read while any
// one node is down.
func (c *Client) Get(ctx context.Context, w io.Writer, ids []string) error {
	for _, id := range ids {
		data, err := c.readAny(ctx, id)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// readAny fetches one chunk from the first node that has a good copy.
func (c *Client) readAny(ctx context.Context, id string) ([]byte, error) {
	var errs []error
	for _, node := range c.nodes {
		data, err := c.read(ctx, node, id)
		if err == nil {
			return data, nil
		}
		errs = append(errs, err)
	}
	return nil, fmt.Errorf("chunk %s on no node: %w", id, errors.Join(errs...))
}

// read fetches one chunk from one node, and checks its fingerprint in case it
// was damaged on the way.
func (c *Client) read(ctx context.Context, node, id string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, node+"/chunks/"+id, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s replied %s", node, resp.Status)
	}

	// Read one byte over the limit, so a too-big reply fails the check below.
	data, err := io.ReadAll(io.LimitReader(resp.Body, Size+1))
	if err != nil {
		return nil, err
	}
	if ID(data) != id {
		return nil, fmt.Errorf("%s sent a damaged chunk", node)
	}
	return data, nil
}
