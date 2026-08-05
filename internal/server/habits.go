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

func registerHabitRoutes(mux *http.ServeMux, svc *service.Service) {
	mux.HandleFunc("/api/habits", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			habits, err := svc.ListHabits(r.Context())
			if err != nil {
				writeServiceError(w, err)
				return
			}
			if habits == nil {
				habits = make([]service.Habit, 0)
			}
			writeJSON(w, http.StatusOK, habits)
		case http.MethodPost:
			name, err := decodeHabitName(r)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			habit, err := svc.CreateHabit(r.Context(), name)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, habit)
		default:
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
		}
	})
	mux.HandleFunc("/api/habits/", func(w http.ResponseWriter, r *http.Request) {
		idText := strings.TrimPrefix(r.URL.Path, "/api/habits/")
		if idText == "" || strings.Contains(idText, "/") {
			writeServiceError(w, apperror.New(apperror.CodeNotFound, "not found"))
			return
		}
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || id <= 0 {
			writeServiceError(w, apperror.New(apperror.CodeHabitNotFound, "habit not found"))
			return
		}
		switch r.Method {
		case http.MethodPatch:
			patch, err := decodeHabitPatch(r)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			habit, err := svc.PatchHabit(r.Context(), id, patch)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, habit)
		default:
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
		}
	})
}

func decodeHabitName(r *http.Request) (string, error) {
	var fields map[string]json.RawMessage
	if err := decodeHabitObject(r, &fields); err != nil {
		return "", err
	}
	if err := rejectUnknownFields(fields, map[string]struct{}{"name": {}}); err != nil {
		return "", invalidHabitJSON(err)
	}
	raw, ok := fields["name"]
	if !ok {
		return "", apperror.New(apperror.CodeInvalidHabit, "habit name is required")
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return "", apperror.Wrap(apperror.CodeInvalidHabit, "invalid habit name", err)
	}
	return name, nil
}

func decodeHabitPatch(r *http.Request) (service.HabitPatch, error) {
	var fields map[string]json.RawMessage
	if err := decodeHabitObject(r, &fields); err != nil {
		return service.HabitPatch{}, err
	}
	if err := rejectUnknownFields(fields, map[string]struct{}{
		"name": {}, "position": {}, "ranges": {}, "archived": {},
	}); err != nil {
		return service.HabitPatch{}, invalidHabitJSON(err)
	}
	patch := service.HabitPatch{}
	if raw, ok := fields["name"]; ok {
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return service.HabitPatch{}, apperror.Wrap(apperror.CodeInvalidHabit, "invalid habit name", err)
		}
		patch.Name = &name
	}
	if raw, ok := fields["position"]; ok {
		// The service clamps positions below zero and at/above the habit count.
		var position int
		if err := json.Unmarshal(raw, &position); err != nil {
			return service.HabitPatch{}, invalidHabitField("position", err)
		}
		patch.Position = &position
	}
	if raw, ok := fields["ranges"]; ok {
		var ranges []service.HabitRange
		if err := json.Unmarshal(raw, &ranges); err != nil {
			return service.HabitPatch{}, invalidHabitField("ranges", err)
		}
		patch.Ranges = &ranges
	}
	if raw, ok := fields["archived"]; ok {
		var archived bool
		if err := json.Unmarshal(raw, &archived); err != nil {
			return service.HabitPatch{}, invalidHabitField("archived", err)
		}
		patch.Archived = &archived
	}
	if patch.Name == nil && patch.Position == nil && patch.Ranges == nil && patch.Archived == nil {
		return service.HabitPatch{}, apperror.New(apperror.CodeInvalidHabit, "habit patch cannot be empty")
	}
	return patch, nil
}

func decodeHabitObject(r *http.Request, target *map[string]json.RawMessage) error {
	if r.Body == nil {
		return apperror.New(apperror.CodeInvalidHabit, "habit JSON is required")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return apperror.Wrap(apperror.CodeInvalidHabit, "invalid habit JSON", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return apperror.New(apperror.CodeInvalidHabit, "habit JSON is required")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return apperror.Wrap(apperror.CodeInvalidHabit, "invalid habit JSON", err)
	}
	return nil
}

func invalidHabitJSON(err error) error {
	return apperror.Wrap(apperror.CodeInvalidHabit, apperror.Message(err), err)
}

func invalidHabitField(field string, err error) error {
	return apperror.Wrap(apperror.CodeInvalidHabit, fmt.Sprintf("invalid %s", field), err)
}
