package main

// This file sends the workers' HLS files to the cache. The cache asks for
// /hls/... here as /files/hls/..., and each file is rebuilt from its chunks on
// the storage nodes.

import (
	"bytes"
	"errors"
	"net/http"
	"path"
	"strconv"

	"github.com/micahtid/flick/pkg/chunk"
)

// contentTypes tells the browser what each kind of HLS file is.
var contentTypes = map[string]string{
	".m3u8": "application/vnd.apple.mpegurl",
	".ts":   "video/mp2t",
	".jpg":  "image/jpeg",
}

// getHLSFile sends one HLS file, such as hls/<id>/720p_003.ts. HLS files are
// small (a segment is a few MB), so the whole file is read before replying.
// That way a failure partway through still becomes a proper error reply.
func (s *server) getHLSFile(w http.ResponseWriter, r *http.Request) {
	name := "hls/" + r.PathValue("path")
	var buf bytes.Buffer
	err := s.files.Copy(r.Context(), &buf, name)
	if errors.Is(err, chunk.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		internalError(w, "read "+name, err)
		return
	}

	if ct, ok := contentTypes[path.Ext(name)]; ok {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	buf.WriteTo(w)
}
