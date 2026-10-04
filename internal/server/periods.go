package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/service"
)

// periodRoute binds one period kind to its collection path and error codes.
// Eras and dynasties expose the same endpoints under different roots.
type periodRoute struct {
	kind         service.PeriodKind
	path         string
	invalidCode  string
	notFoundCode string
}

var periodRoutes = []periodRoute{
	{kind: service.PeriodEra, path: "/api/eras", invalidCode: apperror.CodeInvalidEra, notFoundCode: apperror.CodeEraNotFound},
	{kind: service.PeriodDynasty, path: "/api/dynasties", invalidCode: apperror.CodeInvalidDynasty, notFoundCode: apperror.CodeDynastyNotFound},
}

func registerPeriodRoutes(mux *http.ServeMux, svc *service.Service) {
	for _, route := range periodRoutes {
		registerPeriodKindRoutes(mux, svc, route)
	}
}

func registerPeriodKindRoutes(mux *http.ServeMux, svc *service.Service, route periodRoute) {
	mux.HandleFunc(route.path, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			periods, err := svc.ListPeriods(r.Context(), route.kind)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			if periods == nil {
				periods = make([]service.Period, 0)
			}
			writeJSON(w, http.StatusOK, periods)
		case http.MethodPost:
			input, err := route.decodeInput(r)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			period, err := svc.CreatePeriod(r.Context(), route.kind, input)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, period)
		default:
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
		}
	})
	mux.HandleFunc(route.path+"/", func(w http.ResponseWriter, r *http.Request) {
		idText := strings.TrimPrefix(r.URL.Path, route.path+"/")
		if idText == "" || strings.Contains(idText, "/") {
			writeServiceError(w, apperror.New(apperror.CodeNotFound, "not found"))
			return
		}
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || id <= 0 {
			writeServiceError(w, apperror.New(route.notFoundCode, fmt.Sprintf("%s not found", route.kind)))
			return
		}
		switch r.Method {
		case http.MethodPatch:
			patch, err := route.decodePatch(r)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			period, err := svc.PatchPeriod(r.Context(), route.kind, id, patch)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, period)
		case http.MethodDelete:
			if err := svc.DeletePeriod(r.Context(), route.kind, id); err != nil {
				writeServiceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
		}
	})
}

// decodeInput reads a POST body. Absent fields stay at their zero value so
// the service reports them with the same messages as present-but-invalid ones.
func (route periodRoute) decodeInput(r *http.Request) (service.PeriodInput, error) {
	patch, err := route.decodeFields(r)
	if err != nil {
		return service.PeriodInput{}, err
	}
	input := service.PeriodInput{EndDate: patch.EndDate.Value}
	if patch.Name != nil {
		input.Name = *patch.Name
	}
	if patch.Color != nil {
		input.Color = *patch.Color
	}
	if patch.StartDate != nil {
		input.StartDate = *patch.StartDate
	}
	return input, nil
}

func (route periodRoute) decodePatch(r *http.Request) (service.PeriodPatch, error) {
	patch, err := route.decodeFields(r)
	if err != nil {
		return service.PeriodPatch{}, err
	}
	if patch.Name == nil && patch.Color == nil && patch.StartDate == nil && !patch.EndDate.Set {
		return service.PeriodPatch{}, apperror.New(route.invalidCode, fmt.Sprintf("%s patch cannot be empty", route.kind))
	}
	return patch, nil
}

func (route periodRoute) decodeFields(r *http.Request) (service.PeriodPatch, error) {
	if r.Body == nil {
		return service.PeriodPatch{}, apperror.New(route.invalidCode, fmt.Sprintf("%s JSON is required", route.kind))
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return service.PeriodPatch{}, apperror.Wrap(route.invalidCode, fmt.Sprintf("invalid %s JSON", route.kind), err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return service.PeriodPatch{}, apperror.New(route.invalidCode, fmt.Sprintf("%s JSON is required", route.kind))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return service.PeriodPatch{}, apperror.Wrap(route.invalidCode, fmt.Sprintf("invalid %s JSON", route.kind), err)
	}
	for name := range fields {
		switch name {
		case "name", "color", "start_date", "end_date":
		default:
			return service.PeriodPatch{}, apperror.New(route.invalidCode, fmt.Sprintf("unknown %s field %q", route.kind, name))
		}
	}
	patch := service.PeriodPatch{}
	for name, target := range map[string]**string{"name": &patch.Name, "color": &patch.Color, "start_date": &patch.StartDate} {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		// A JSON null would decode into a nil pointer and read as "omitted",
		// so only a real string is accepted for the required fields.
		var value string
		if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
			return service.PeriodPatch{}, apperror.New(route.invalidCode, fmt.Sprintf("%s must be a string", name))
		}
		*target = &value
	}
	if raw, ok := fields["end_date"]; ok {
		// An explicit null is meaningful here: it makes the period ongoing.
		var value *string
		if err := json.Unmarshal(raw, &value); err != nil {
			return service.PeriodPatch{}, apperror.New(route.invalidCode, "end_date must be a string or null")
		}
		patch.EndDate = service.OptionalEndDate{Set: true, Value: value}
	}
	return patch, nil
}
