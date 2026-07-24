package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

type directoriesResponse struct {
	Path    string           `json:"path"`
	Entries []DirectoryEntry `json:"entries"`
}

// handleDirectories lists immediate subdirectories of a path, confined to the
// configured workspace roots by the injected validator. It never reads file
// contents and never lists non-directory entries (section 15 excludes raw file
// reads).
func (s *Server) handleDirectories(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "missing path")
		return
	}

	resolved, err := s.deps.Directories.Resolve(path)
	if err != nil {
		// A rejected path is a forbidden location, not a server fault.
		writeError(w, http.StatusForbidden, codeForbidden, "path not allowed")
		return
	}

	dirents, err := os.ReadDir(resolved)
	if err != nil {
		writeError(w, http.StatusNotFound, codeNotFound, "directory not readable")
		return
	}

	entries := make([]DirectoryEntry, 0, len(dirents))
	for _, de := range dirents {
		// Directories only. Skip symlinks: os.DirEntry.IsDir reports false for a
		// symlink (it reflects the entry type, not the target), so a symlink to a
		// directory is naturally excluded, preventing escape via link following.
		if !de.IsDir() {
			continue
		}
		name := de.Name()
		entries = append(entries, DirectoryEntry{
			Name: name,
			Path: filepath.Join(resolved, name),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	writeJSON(w, http.StatusOK, directoriesResponse{Path: resolved, Entries: entries})
}
