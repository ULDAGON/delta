package server_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/api"
)

type periodJSON struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Color     string  `json:"color"`
	StartDate string  `json:"start_date"`
	EndDate   *string `json:"end_date"`
}

func TestEraAndDynastyRoutesShareOneLifecycle(t *testing.T) {
	for _, kind := range []struct{ name, path string }{
		{"era", "/api/eras"},
		{"dynasty", "/api/dynasties"},
	} {
		t.Run(kind.name, func(t *testing.T) {
			h := api.NewTestHarness(t)
			invalid, notFound := "invalid_"+kind.name, kind.name+"_not_found"

			empty := readEntryBody(t, habitRequest(t, h, http.MethodGet, kind.path, ""))
			if strings.TrimSpace(string(empty)) != "[]" {
				t.Fatalf("empty list = %s, want []", empty)
			}

			created := habitRequest(t, h, http.MethodPost, kind.path, `{"name":" Berlin ","color":"#1A2b3C","start_date":"2020-01-01","end_date":"2020-12-31"}`)
			if created.StatusCode != http.StatusCreated {
				t.Fatalf("create status = %d, body = %s", created.StatusCode, readEntryBody(t, created))
			}
			var berlin periodJSON
			decodeJSON(t, created, &berlin)
			if berlin.ID == 0 || berlin.Name != "Berlin" || berlin.Color != "#1A2b3C" || berlin.StartDate != "2020-01-01" || berlin.EndDate == nil || *berlin.EndDate != "2020-12-31" {
				t.Fatalf("created = %#v", berlin)
			}
			// An omitted end_date and an explicit null both mean ongoing.
			var now periodJSON
			decodeJSON(t, habitRequest(t, h, http.MethodPost, kind.path, `{"name":"Now","color":"#000000","start_date":"2024-01-01"}`), &now)
			if now.EndDate != nil {
				t.Fatalf("ongoing end_date = %v, want null", *now.EndDate)
			}
			if raw := readEntryBody(t, habitRequest(t, h, http.MethodGet, kind.path, "")); !strings.Contains(string(raw), `"end_date":null`) {
				t.Fatalf("list = %s, want an explicit null end_date", raw)
			}

			var listed []periodJSON
			decodeJSON(t, habitRequest(t, h, http.MethodGet, kind.path, ""), &listed)
			if len(listed) != 2 || listed[0].ID != now.ID || listed[1].ID != berlin.ID {
				t.Fatalf("listed = %#v, want newest start first", listed)
			}

			for _, tc := range []struct{ body, message string }{
				{`{"color":"#000000","start_date":"2022-01-01"}`, "name cannot be empty"},
				{`{"name":"X","start_date":"2022-01-01"}`, "#rrggbb"},
				{`{"name":"X","color":"red","start_date":"2022-01-01"}`, "#rrggbb"},
				{`{"name":"X","color":"#000000"}`, "start_date"},
				{`{"name":"X","color":"#000000","start_date":"2022-02-30"}`, "start_date"},
				{`{"name":"X","color":"#000000","start_date":"2022-02-01","end_date":"2022-01-01"}`, "end_date cannot be before start_date"},
				{`{"name":"berlin","color":"#000000","start_date":"2022-01-01","end_date":"2022-01-02"}`, `named "Berlin" already exists`},
				{`{"name":"X","color":"#000000","start_date":"2020-12-31","end_date":"2021-01-31"}`, fmt.Sprintf(`overlaps %s "Berlin" (2020-01-01 to 2020-12-31)`, kind.name)},
				{`{"name":"X","color":"#000000","start_date":"2022-01-01"}`, fmt.Sprintf(`overlaps %s "Now" (2024-01-01 to ongoing)`, kind.name)},
				{`{"name":"X","color":"#000000","start_date":"2022-01-01","kind":"era"}`, `unknown ` + kind.name + ` field "kind"`},
				{`{"name":null,"color":"#000000","start_date":"2022-01-01"}`, "name must be a string"},
				{`{"name":"X","color":"#000000","start_date":"2022-01-01","end_date":7}`, "end_date must be a string or null"},
				{`[]`, "invalid " + kind.name + " JSON"},
				{``, "JSON is required"},
			} {
				assertError(t, habitRequest(t, h, http.MethodPost, kind.path, tc.body), http.StatusBadRequest, invalid, tc.message)
			}

			itemPath := fmt.Sprintf("%s/%d", kind.path, berlin.ID)
			var patched periodJSON
			decodeJSON(t, habitRequest(t, h, http.MethodPatch, itemPath, `{"name":"Berlin years","color":"#ffffff"}`), &patched)
			if patched.ID != berlin.ID || patched.Name != "Berlin years" || patched.Color != "#ffffff" || patched.StartDate != "2020-01-01" || patched.EndDate == nil || *patched.EndDate != "2020-12-31" {
				t.Fatalf("patched = %#v, want dates left unchanged", patched)
			}
			decodeJSON(t, habitRequest(t, h, http.MethodPatch, itemPath, `{"end_date":"2021-06-30"}`), &patched)
			if patched.EndDate == nil || *patched.EndDate != "2021-06-30" || patched.Name != "Berlin years" {
				t.Fatalf("patched end = %#v", patched)
			}
			// end_date null would make it ongoing, straight into "Now".
			assertError(t, habitRequest(t, h, http.MethodPatch, itemPath, `{"end_date":null}`), http.StatusBadRequest, invalid, `"Now"`)
			assertError(t, habitRequest(t, h, http.MethodPatch, itemPath, `{}`), http.StatusBadRequest, invalid, "patch cannot be empty")
			assertError(t, habitRequest(t, h, http.MethodPatch, itemPath, `{"name":"now"}`), http.StatusBadRequest, invalid, "already exists")

			nowPath := fmt.Sprintf("%s/%d", kind.path, now.ID)
			deleted := habitRequest(t, h, http.MethodDelete, nowPath, "")
			if deleted.StatusCode != http.StatusNoContent {
				t.Fatalf("delete status = %d, body = %s", deleted.StatusCode, readEntryBody(t, deleted))
			}
			deleted.Body.Close()
			assertErrorCode(t, habitRequest(t, h, http.MethodDelete, nowPath, ""), http.StatusNotFound, notFound)
			assertErrorCode(t, habitRequest(t, h, http.MethodPatch, nowPath, `{"name":"Gone"}`), http.StatusNotFound, notFound)
			assertErrorCode(t, habitRequest(t, h, http.MethodPatch, kind.path+"/abc", `{"name":"Gone"}`), http.StatusNotFound, notFound)
			assertErrorCode(t, habitRequest(t, h, http.MethodGet, itemPath, ""), http.StatusMethodNotAllowed, "method_not_allowed")
			assertErrorCode(t, habitRequest(t, h, http.MethodPut, kind.path, `{}`), http.StatusMethodNotAllowed, "method_not_allowed")

			// With "Now" gone the remaining period may become ongoing.
			decodeJSON(t, habitRequest(t, h, http.MethodPatch, itemPath, `{"end_date":null}`), &patched)
			if patched.EndDate != nil {
				t.Fatalf("ongoing patch = %#v", patched)
			}
		})
	}
}

func TestEraAndDynastyNamesAndRangesNeverInteract(t *testing.T) {
	h := api.NewTestHarness(t)
	body := `{"name":"Berlin","color":"#112233","start_date":"2020-01-01"}`
	var era, dynasty periodJSON
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/eras", body), &era)
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/dynasties", body), &dynasty)

	// An era's ID is not addressable under /api/dynasties, and vice versa.
	assertErrorCode(t, habitRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/dynasties/%d", era.ID), ""), http.StatusNotFound, "dynasty_not_found")
	assertErrorCode(t, habitRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/eras/%d", dynasty.ID), ""), http.StatusNotFound, "era_not_found")
}

func TestGridDaysExposeEraAndDynastyIDs(t *testing.T) {
	h := api.NewTestHarness(t)
	var era, dynasty periodJSON
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/eras", `{"name":"March","color":"#112233","start_date":"2021-03-01","end_date":"2021-03-31"}`), &era)
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/dynasties", `{"name":"Long","color":"#445566","start_date":"2021-03-15"}`), &dynasty)

	grid := gridRequest(t, h, 2021, "rating")
	for date, want := range map[string][2]int64{
		"2021-02-28": {0, 0},
		"2021-03-01": {era.ID, 0},
		"2021-03-15": {era.ID, dynasty.ID},
		"2021-03-31": {era.ID, dynasty.ID},
		"2021-04-01": {0, dynasty.ID},
		"2021-12-31": {0, dynasty.ID},
	} {
		day := gridDay(t, grid, date)
		var got [2]int64
		if day.EraID != nil {
			got[0] = *day.EraID
		}
		if day.DynastyID != nil {
			got[1] = *day.DynastyID
		}
		if got != want {
			t.Errorf("%s era/dynasty ids = %v, want %v", date, got, want)
		}
		if day.HasEntry {
			t.Errorf("%s has_entry = true, want an empty day", date)
		}
	}

	// Days outside every period carry explicit nulls rather than no field.
	raw := readEntryBody(t, habitRequest(t, h, http.MethodGet, "/api/grid?year=2020", ""))
	if !strings.Contains(string(raw), `"era_id":null`) || !strings.Contains(string(raw), `"dynasty_id":null`) {
		t.Fatalf("2020 grid lacks null era_id/dynasty_id")
	}
}
