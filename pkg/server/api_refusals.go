package server

import (
	"net/http"
	"time"

	"github.com/sirrobot01/decypharr/internal/utils"
)

// handleListUncachedRefusals lists releases refused as not cached in the last
// 24 hours, newest first. ?since=<RFC 3339 time> narrows the list.
func (s *Server) handleListUncachedRefusals(w http.ResponseWriter, r *http.Request) {
	since, ok := s.refusalsSince(w, r)
	if !ok {
		return
	}
	utils.JSONResponse(w, s.manager.UncachedRefusals(since), http.StatusOK)
}

// handleListDeadItems lists items with a file whose download link the
// provider keeps refusing (TorBox requestdl answering DATABASE_ERROR) in the
// last 24 hours, newest first. Reads of such a file fail at once with EIO.
// ?since=<RFC 3339 time> narrows the list.
func (s *Server) handleListDeadItems(w http.ResponseWriter, r *http.Request) {
	since, ok := s.refusalsSince(w, r)
	if !ok {
		return
	}
	utils.JSONResponse(w, s.manager.DeadItems(since), http.StatusOK)
}

func (s *Server) refusalsSince(w http.ResponseWriter, r *http.Request) (time.Time, bool) {
	raw := r.URL.Query().Get("since")
	if raw == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		s.sendJSONError(w, "since must be an RFC 3339 time", http.StatusBadRequest)
		return time.Time{}, false
	}
	return t, true
}
