package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/service"
)

func registerEntryRoutes(mux *http.ServeMux, svc *service.Service) {
	mux.HandleFunc("/api/entries", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
			entries, err := svc.ListEntries(r.Context(), from, to)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, entries)
		default:
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
		}
	})
	mux.HandleFunc("/api/entries/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/entries/"), "/")
		if len(parts) == 3 && parts[1] == "checkoffs" {
			if r.Method != http.MethodPost && r.Method != http.MethodDelete {
				writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
				return
			}
			habitID, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil || habitID <= 0 {
				writeServiceError(w, apperror.New(apperror.CodeHabitNotFound, "habit not found"))
				return
			}
			entry, err := svc.SetCheckoff(r.Context(), parts[0], habitID, r.Method == http.MethodPost)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, entry)
			return
		}
		if len(parts) != 1 || parts[0] == "" {
			writeServiceError(w, apperror.New(apperror.CodeNotFound, "not found"))
			return
		}
		date := parts[0]
		switch r.Method {
		case http.MethodGet:
			entry, err := svc.GetEntry(r.Context(), date)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, entry)
		case http.MethodPut:
			patch, err := decodeEntryPatch(r)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			entry, err := svc.UpsertEntry(r.Context(), date, patch)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, entry)
		case http.MethodDelete:
			if err := svc.DeleteEntry(r.Context(), date); err != nil {
				writeServiceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
		}
	})
}

func decodeEntryPatch(r *http.Request) (service.EntryPatch, error) {
	if r.Body == nil {
		return service.EntryPatch{}, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return service.EntryPatch{}, apperror.Wrap(apperror.CodeInvalidEntry, "invalid entry JSON", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return service.EntryPatch{}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return service.EntryPatch{}, apperror.Wrap(apperror.CodeInvalidEntry, "invalid entry JSON", err)
	}
	if _, ok := fields["checkoffs"]; ok {
		return service.EntryPatch{}, apperror.New(apperror.CodeInvalidEntry, "check-offs are written via the dedicated check-off endpoints")
	}
	if err := rejectUnknownFields(fields, map[string]struct{}{
		"text": {}, "goals": {}, "gratitudes": {}, "ws": {}, "ratings": {}, "pixel": {},
		"work_hours": {},
	}); err != nil {
		return service.EntryPatch{}, err
	}
	patch := service.EntryPatch{}
	if raw, ok := fields["text"]; ok {
		if err := decodeString("text", raw, &patch.Text); err != nil {
			return service.EntryPatch{}, err
		}
	}
	if raw, ok := fields["goals"]; ok {
		patch.GoalsSet = true
		if string(raw) == "null" {
			patch.Goals = make([]service.Goal, 5)
		} else {
			if err := json.Unmarshal(raw, &patch.Goals); err != nil {
				return service.EntryPatch{}, invalidEntryJSON("goals", err)
			}
		}
	}
	if raw, ok := fields["gratitudes"]; ok {
		patch.GratitudesSet = true
		if string(raw) == "null" {
			patch.Gratitudes = make([]string, 3)
		} else {
			if err := json.Unmarshal(raw, &patch.Gratitudes); err != nil {
				return service.EntryPatch{}, invalidEntryJSON("gratitudes", err)
			}
		}
	}
	if raw, ok := fields["ws"]; ok {
		if err := decodeWSPatch(raw, &patch.Ws); err != nil {
			return service.EntryPatch{}, err
		}
	}
	if raw, ok := fields["ratings"]; ok {
		if err := decodeRatingsPatch(raw, &patch.Ratings); err != nil {
			return service.EntryPatch{}, err
		}
	}
	if raw, ok := fields["pixel"]; ok {
		if err := decodePixel(raw, &patch.Pixel); err != nil {
			return service.EntryPatch{}, err
		}
	}
	if raw, ok := fields["work_hours"]; ok {
		if err := decodeWorkHours(raw, &patch.WorkHours); err != nil {
			return service.EntryPatch{}, err
		}
	}
	return patch, nil
}

func decodeString(field string, raw json.RawMessage, target *service.OptionalString) error {
	if string(raw) == "null" {
		target.Set = true
		target.Value = ""
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return invalidEntryJSON(field, err)
	}
	target.Set = true
	target.Value = value
	return nil
}

func decodePixel(raw json.RawMessage, target *service.OptionalPixel) error {
	if string(raw) == "null" {
		return apperror.New(apperror.CodeInvalidEntry, "pixel cannot be null; use 0, 1, or 2")
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return invalidEntryJSON("pixel", err)
	}
	if value < 0 || value > 2 {
		return apperror.New(apperror.CodeInvalidEntry, "pixel must be 0 (grey), 1 (green), or 2 (orange)")
	}
	target.Set = true
	target.Value = value
	return nil
}

// decodeWorkHours treats null as an explicit clear. Zero is a real recorded
// value, so only null unsets the field.
func decodeWorkHours(raw json.RawMessage, target *service.OptionalWorkHours) error {
	target.Set = true
	if string(raw) == "null" {
		target.Value = nil
		return nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return invalidEntryJSON("work_hours", err)
	}
	if err := service.ValidateWorkHours(value); err != nil {
		return err
	}
	target.Value = &value
	return nil
}

func decodeWSPatch(raw json.RawMessage, patch *service.WsPatch) error {
	if string(raw) == "null" {
		patch.WentWell = service.OptionalString{Set: true}
		patch.CouldHaveGoneBetter = service.OptionalString{Set: true}
		patch.GoalForTomorrow = service.OptionalString{Set: true}
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return invalidEntryJSON("ws", err)
	}
	if err := rejectUnknownFields(fields, map[string]struct{}{
		"went_well": {}, "could_have_gone_better": {}, "goal_for_tomorrow": {},
	}); err != nil {
		return err
	}
	for _, field := range []struct {
		name   string
		target *service.OptionalString
	}{
		{name: "went_well", target: &patch.WentWell},
		{name: "could_have_gone_better", target: &patch.CouldHaveGoneBetter},
		{name: "goal_for_tomorrow", target: &patch.GoalForTomorrow},
	} {
		if value, ok := fields[field.name]; ok {
			if err := decodeString("ws."+field.name, value, field.target); err != nil {
				return err
			}
		}
	}
	return nil
}

func decodeRatingsPatch(raw json.RawMessage, patch *service.RatingsPatch) error {
	if string(raw) == "null" {
		patch.Total = service.OptionalRating{Set: true}
		patch.Body = service.OptionalRating{Set: true}
		patch.Mind = service.OptionalRating{Set: true}
		patch.Spirit = service.OptionalRating{Set: true}
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return invalidEntryJSON("ratings", err)
	}
	if err := rejectUnknownFields(fields, map[string]struct{}{
		"total": {}, "body": {}, "mind": {}, "spirit": {},
	}); err != nil {
		return err
	}
	for _, field := range []struct {
		name   string
		target *service.OptionalRating
	}{
		{name: "total", target: &patch.Total},
		{name: "body", target: &patch.Body},
		{name: "mind", target: &patch.Mind},
		{name: "spirit", target: &patch.Spirit},
	} {
		if value, ok := fields[field.name]; ok {
			field.target.Set = true
			if string(value) != "null" {
				var rating int
				if err := json.Unmarshal(value, &rating); err != nil {
					return invalidEntryJSON("ratings."+field.name, err)
				}
				field.target.Value = &rating
			}
		}
	}
	return nil
}

func rejectUnknownFields(fields map[string]json.RawMessage, allowed map[string]struct{}) error {
	unknown := make([]string, 0)
	for name := range fields {
		if _, ok := allowed[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return apperror.New(apperror.CodeInvalidEntry, fmt.Sprintf("unknown entry field %q", unknown[0]))
}

func invalidEntryJSON(field string, err error) error {
	return apperror.Wrap(apperror.CodeInvalidEntry, fmt.Sprintf("invalid %s", field), err)
}
