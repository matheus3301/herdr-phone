package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	snap := s.deps.State.Snapshot()
	etag := `"` + snap.Hash + `"`

	// Allow conditional revalidation despite the API no-store default.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)

	if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	body, err := json.Marshal(snap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "snapshot encode failed")
		return
	}
	s.writeMaybeGzip(w, r, http.StatusOK, "application/json; charset=utf-8", body)
}

type capabilitiesResponse struct {
	Version      int             `json:"version"`
	Operations   []string        `json:"operations"`
	Capabilities json.RawMessage `json:"capabilities"`
	Status       DaemonStatus    `json:"status"`
	Tunnel       TunnelInfo      `json:"tunnel"`
	Limits       limitsJSON      `json:"limits"`
}

type limitsJSON struct {
	MaxBodyBytes     int64 `json:"max_body_bytes"`
	MaxPaneReadLines int   `json:"max_pane_read_lines"`
	ConfirmationTTLs int   `json:"confirmation_ttl_seconds"`
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	status := s.deps.Daemon.Status()
	status.Clients = s.Clients()

	resp := capabilitiesResponse{
		Version:      1,
		Operations:   allowedOperationNames(),
		Capabilities: s.deps.State.Capabilities(),
		Status:       status,
		Tunnel:       s.deps.Tunnel.Tunnel(),
		Limits: limitsJSON{
			MaxBodyBytes:     s.cfg.MaxBodyBytes,
			MaxPaneReadLines: s.cfg.MaxPaneReadLines,
			ConfirmationTTLs: int(s.cfg.ConfirmationTTL.Seconds()),
		},
	}
	body, err := json.Marshal(resp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "capabilities encode failed")
		return
	}
	s.writeMaybeGzip(w, r, http.StatusOK, "application/json; charset=utf-8", body)
}

var paneReadSources = map[string]bool{
	"visible":          true,
	"recent":           true,
	"recent-unwrapped": true,
}

type paneReadResponse struct {
	PaneID  string `json:"pane_id"`
	Source  string `json:"source"`
	Lines   int    `json:"lines"`
	Content string `json:"content"`
}

func (s *Server) handlePaneRead(w http.ResponseWriter, r *http.Request) {
	paneID := r.PathValue("pane_id")
	if paneID == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "missing pane id")
		return
	}
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "visible"
	}
	if !paneReadSources[source] {
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid source")
		return
	}
	lines := 100
	if v := r.URL.Query().Get("lines"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, codeBadRequest, "invalid lines")
			return
		}
		lines = n
	}
	if lines > s.cfg.MaxPaneReadLines {
		lines = s.cfg.MaxPaneReadLines
	}

	content, err := s.deps.State.ReadPane(r.Context(), paneID, source, lines)
	if err != nil {
		writeError(w, http.StatusNotFound, codeNotFound, "pane read failed")
		return
	}
	writeJSON(w, http.StatusOK, paneReadResponse{
		PaneID:  paneID,
		Source:  source,
		Lines:   lines,
		Content: string(content),
	})
}

// writeMaybeGzip writes body, gzip-compressing when the client accepts it.
func (s *Server) writeMaybeGzip(w http.ResponseWriter, r *http.Request, status int, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	if acceptsGzip(r) && len(body) >= 512 {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		if _, err := gz.Write(body); err == nil && gz.Close() == nil {
			w.WriteHeader(status)
			_, _ = w.Write(buf.Bytes())
			return
		}
		// Fall through to uncompressed on any gzip failure.
		w.Header().Del("Content-Encoding")
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func acceptsGzip(r *http.Request) bool {
	for enc := range strings.SplitSeq(r.Header.Get("Accept-Encoding"), ",") {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(enc, ";", 2)[0]), "gzip") {
			return true
		}
	}
	return false
}

// etagMatches reports whether the If-None-Match header matches etag. It handles
// a comma list and the "*" wildcard, ignoring weak validators' W/ prefix.
func etagMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "*" {
		return true
	}
	for candidate := range strings.SplitSeq(header, ",") {
		c := strings.TrimSpace(candidate)
		c = strings.TrimPrefix(c, "W/")
		if c == etag {
			return true
		}
	}
	return false
}
