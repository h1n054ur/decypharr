package server

import (
	"net/http"
	"time"

	"github.com/sirrobot01/decypharr/internal/utils"
)

// handleListUncachedRefusals lists releases refused as not cached in the last
// 24 hours, newest first. ?since=<RFC 3339 time> narrows the list.
func (s *Server) handleListUncachedRefusals(w http.ResponseWriter, r *http.Request) {
	var since time.Time
	if raw := r.URL.Query().Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			s.sendJSONError(w, "since must be an RFC 3339 time", http.StatusBadRequest)
			return
		}
		since = t
	}
	utils.JSONResponse(w, s.manager.UncachedRefusals(since), http.StatusOK)
}
