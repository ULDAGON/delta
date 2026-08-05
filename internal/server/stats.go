package server

import (
	"net/http"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/service"
)

func registerStatsRoutes(mux *http.ServeMux, svc *service.Service) {
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
			return
		}
		stats, err := svc.Stats(r.Context(), r.URL.Query().Get("from"), r.URL.Query().Get("to"), r.URL.Query().Get("agg"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, stats)
	})
}
