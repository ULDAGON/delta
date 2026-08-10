package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/service"
)

// registerColorRoutes owns the exact path /api/settings/colors so it wins over
// the /api/settings/ catch-all, which answers 404 for everything else.
func registerColorRoutes(mux *http.ServeMux, svc *service.Service) {
	mux.HandleFunc("/api/settings/colors", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			colors, err := svc.UIColors(r.Context())
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeColors(w, colors)
		case http.MethodPut:
			body, err := readColorsBody(r)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			stored, err := svc.SetUIColors(r.Context(), body)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeColors(w, stored)
		case http.MethodDelete:
			if err := svc.ClearUIColors(r.Context()); err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, okEnvelope{OK: true})
		default:
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
		}
	})
}

func readColorsBody(r *http.Request) (json.RawMessage, error) {
	if r.Body == nil {
		return nil, apperror.New(apperror.CodeInvalidUIColors, "colors JSON is required")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, apperror.Wrap(apperror.CodeInvalidUIColors, "invalid colors JSON", err)
	}
	return body, nil
}

func writeColors(w http.ResponseWriter, colors json.RawMessage) {
	if len(colors) == 0 {
		colors = json.RawMessage("{}")
	}
	writeJSON(w, http.StatusOK, colors)
}
