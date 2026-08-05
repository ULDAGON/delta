package server

import (
	"net/http"
	"strconv"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/service"
)

func registerGridRoutes(mux *http.ServeMux, svc *service.Service) {
	mux.HandleFunc("/api/grid", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
			return
		}
		selectedYear := service.LocalCurrentYear()
		if year := r.URL.Query().Get("year"); year != "" {
			parsed, err := strconv.Atoi(year)
			if err != nil {
				writeServiceError(w, apperror.New(apperror.CodeInvalidGrid, "year must be a four-digit calendar year"))
				return
			}
			selectedYear = parsed
		}
		view := r.URL.Query().Get("view")
		if view == "" {
			view = service.GridViewRating
		}
		grid, err := svc.Grid(r.Context(), selectedYear, view)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, grid)
	})
}
