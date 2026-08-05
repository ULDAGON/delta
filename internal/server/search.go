package server

import (
	"net/http"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/service"
)

func registerSearchRoutes(mux *http.ServeMux, svc *service.Service) {
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
			return
		}
		results, err := svc.Search(r.Context(), r.URL.Query().Get("q"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, results)
	})
}
