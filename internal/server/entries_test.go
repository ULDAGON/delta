package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/api"
)

func TestEntryPutUpsertsAndGetReturnsFullDocument(t *testing.T) {
	h := api.NewTestHarness(t)
	date := "2026-08-02"
	body := []byte(`{"text":"A useful day"}`)

	first := entryRequest(t, h, http.MethodPut, "/api/entries/"+date, body)
	firstBody := readEntryBody(t, first)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first PUT status = %d, want 200; body=%s", first.StatusCode, firstBody)
	}
	var created map[string]any
	if err := json.Unmarshal(firstBody, &created); err != nil {
		t.Fatal(err)
	}
	if created["date"] != date || created["text"] != "A useful day" {
		t.Fatalf("created entry = %#v", created)
	}
	goals, ok := created["goals"].([]any)
	if !ok || len(goals) != 5 {
		t.Fatalf("goals = %#v, want five lines", created["goals"])
	}
	gratitudes, ok := created["gratitudes"].([]any)
	if !ok || len(gratitudes) != 3 {
		t.Fatalf("gratitudes = %#v, want three lines", created["gratitudes"])
	}

	repeated := entryRequest(t, h, http.MethodPut, "/api/entries/"+date, body)
	repeatedBody := readEntryBody(t, repeated)
	if repeated.StatusCode != http.StatusOK || !bytes.Equal(repeatedBody, firstBody) {
		t.Fatalf("identical PUT changed response: first=%s repeated=%s", firstBody, repeatedBody)
	}
	got := entryRequest(t, h, http.MethodGet, "/api/entries/"+date, nil)
	gotBody := readEntryBody(t, got)
	if got.StatusCode != http.StatusOK || !bytes.Equal(gotBody, firstBody) {
		t.Fatalf("GET = %d %s, want first PUT document", got.StatusCode, gotBody)
	}
	for i := 0; i < 6; i++ {
		patch := map[string]any{"text": "A useful day"}
		if i%2 == 1 {
			patch["ws"] = map[string]any{"went_well": fmt.Sprintf("iteration %d", i)}
		}
		putJSON(t, h, date, patch)
	}
	rangeResponse := entryRequest(t, h, http.MethodGet, "/api/entries?from="+date+"&to="+date, nil)
	var rangeEntries []map[string]any
	if err := json.NewDecoder(rangeResponse.Body).Decode(&rangeEntries); err != nil {
		rangeResponse.Body.Close()
		t.Fatal(err)
	}
	rangeResponse.Body.Close()
	if rangeResponse.StatusCode != http.StatusOK || len(rangeEntries) != 1 || rangeEntries[0]["date"] != date {
		t.Fatalf("range after overlapping PUTs = status %d entries %#v, want one entry for %s", rangeResponse.StatusCode, rangeEntries, date)
	}
}

func TestEntryPutIsPartialAndGoalsAreDateBound(t *testing.T) {
	h := api.NewTestHarness(t)
	dayOne := "2026-08-01"
	dayTwo := "2026-08-02"
	initialGoals := []map[string]any{
		{"text": "one", "checked": false}, {"text": "two", "checked": true},
		{"text": "three", "checked": false}, {"text": "four", "checked": false}, {"text": "five", "checked": false},
	}
	putJSON(t, h, dayOne, map[string]any{
		"text": "kept text", "goals": initialGoals, "ratings": map[string]any{"total": 5},
	})
	putJSON(t, h, dayTwo, map[string]any{"text": "untouched day"})
	untouchedBefore := readEntryBody(t, entryRequest(t, h, http.MethodGet, "/api/entries/"+dayTwo, nil))
	changedGoals := []map[string]any{
		{"text": "updated one", "checked": true}, {"text": "updated two", "checked": false},
		{"text": "updated three", "checked": false}, {"text": "updated four", "checked": false}, {"text": "updated five", "checked": false},
	}
	putJSON(t, h, dayOne, map[string]any{"goals": changedGoals})
	untouchedAfterGoals := readEntryBody(t, entryRequest(t, h, http.MethodGet, "/api/entries/"+dayTwo, nil))
	if !bytes.Equal(untouchedBefore, untouchedAfterGoals) {
		t.Fatalf("writing goals to %s changed %s: before=%s after=%s", dayOne, dayTwo, untouchedBefore, untouchedAfterGoals)
	}

	partial := putJSON(t, h, dayOne, map[string]any{
		"ws":      map[string]any{"went_well": "partial write"},
		"ratings": map[string]any{"body": 2},
	})
	if partial["text"] != "kept text" || partial["ratings"].(map[string]any)["total"] != float64(5) {
		t.Fatalf("partial update cleared existing fields: %#v", partial)
	}
	if partial["ws"].(map[string]any)["went_well"] != "partial write" {
		t.Fatalf("partial update did not set ws: %#v", partial["ws"])
	}
	if partial["ratings"].(map[string]any)["body"] != float64(2) {
		t.Fatalf("partial update did not set body rating: %#v", partial["ratings"])
	}
	untouchedAfter := readEntryBody(t, entryRequest(t, h, http.MethodGet, "/api/entries/"+dayTwo, nil))
	if !bytes.Equal(untouchedBefore, untouchedAfter) {
		t.Fatalf("writing %s changed %s: before=%s after=%s", dayOne, dayTwo, untouchedBefore, untouchedAfter)
	}
}

func TestEntryPixelMarkerCyclesAndValidates(t *testing.T) {
	h := api.NewTestHarness(t)
	date := "2026-08-02"
	created := putJSON(t, h, date, map[string]any{"text": "marked"})
	if created["pixel"] != float64(0) {
		t.Fatalf("new entry pixel = %v, want 0 (grey)", created["pixel"])
	}
	for _, want := range []float64{1, 2, 0} {
		got := putJSON(t, h, date, map[string]any{"pixel": want})
		if got["pixel"] != want {
			t.Fatalf("pixel after setting %v = %v, want %v", want, got["pixel"], want)
		}
		if got["text"] != "marked" {
			t.Fatalf("pixel write cleared text: %#v", got)
		}
	}
	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "negative", body: `{"pixel":-1}`},
		{name: "above maximum", body: `{"pixel":3}`},
		{name: "null", body: `{"pixel":null}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := entryRequest(t, h, http.MethodPut, "/api/entries/"+date, []byte(tt.body))
			assertError(t, response, http.StatusBadRequest, "invalid_entry", "pixel")
		})
	}
}

func TestEntryWorkHoursAreOptionalDecimalsAndOmittedWhenUnset(t *testing.T) {
	h := api.NewTestHarness(t)
	date := "2026-08-02"

	created := putJSON(t, h, date, map[string]any{"text": "no hours yet"})
	if _, ok := created["work_hours"]; ok {
		t.Fatalf("new entry included work_hours = %#v, want the key omitted", created["work_hours"])
	}
	decimal := putJSON(t, h, date, map[string]any{"work_hours": 7.5})
	if decimal["work_hours"] != float64(7.5) || decimal["text"] != "no hours yet" {
		t.Fatalf("work_hours write = %#v, want 7.5 with text preserved", decimal)
	}
	zero := putJSON(t, h, date, map[string]any{"work_hours": 0})
	if zero["work_hours"] != float64(0) {
		t.Fatalf("work_hours = %#v, want a recorded 0", zero["work_hours"])
	}
	rangeResponse := entryRequest(t, h, http.MethodGet, "/api/entries?from="+date+"&to="+date, nil)
	var rangeEntries []map[string]any
	if err := json.NewDecoder(rangeResponse.Body).Decode(&rangeEntries); err != nil {
		rangeResponse.Body.Close()
		t.Fatal(err)
	}
	rangeResponse.Body.Close()
	if len(rangeEntries) != 1 || rangeEntries[0]["work_hours"] != float64(0) {
		t.Fatalf("range entries = %#v, want the recorded 0 for %s", rangeEntries, date)
	}

	cleared := putJSON(t, h, date, map[string]any{"work_hours": nil})
	if _, ok := cleared["work_hours"]; ok {
		t.Fatalf("cleared entry included work_hours = %#v, want the key omitted", cleared["work_hours"])
	}
	got := readEntryBody(t, entryRequest(t, h, http.MethodGet, "/api/entries/"+date, nil))
	if strings.Contains(string(got), "work_hours") {
		t.Fatalf("GET after clear = %s, want no work_hours key", got)
	}

	for _, tt := range []struct {
		name    string
		body    string
		message string
	}{
		{name: "negative", body: `{"work_hours":-0.5}`, message: "work hours must be between 0 and 24"},
		{name: "above maximum", body: `{"work_hours":24.5}`, message: "work hours must be between 0 and 24"},
		{name: "not a number", body: `{"work_hours":"7.5"}`, message: "invalid work_hours"},
		{name: "camel case alias", body: `{"workHours":7.5}`, message: `unknown entry field "workHours"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := entryRequest(t, h, http.MethodPut, "/api/entries/"+date, []byte(tt.body))
			assertError(t, response, http.StatusBadRequest, "invalid_entry", tt.message)
		})
	}
	for _, boundary := range []string{`{"work_hours":0}`, `{"work_hours":24}`, `{"work_hours":0.25}`} {
		response := entryRequest(t, h, http.MethodPut, "/api/entries/"+date, []byte(boundary))
		body := readEntryBody(t, response)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("PUT %s status = %d, want 200; body=%s", boundary, response.StatusCode, body)
		}
	}
}

func TestEntryRangeDeleteAndStableDateErrors(t *testing.T) {
	h := api.NewTestHarness(t)
	putJSON(t, h, "2026-08-01", map[string]any{"text": "first"})
	putJSON(t, h, "2026-08-03", map[string]any{"text": "third"})

	rangeResponse := entryRequest(t, h, http.MethodGet, "/api/entries?from=2026-08-01&to=2026-08-02", nil)
	var entries []map[string]any
	if rangeResponse.StatusCode != http.StatusOK || json.NewDecoder(rangeResponse.Body).Decode(&entries) != nil {
		rangeResponse.Body.Close()
		t.Fatalf("range response = %d", rangeResponse.StatusCode)
	}
	rangeResponse.Body.Close()
	if len(entries) != 1 || entries[0]["date"] != "2026-08-01" {
		t.Fatalf("range entries = %#v", entries)
	}

	deleted := entryRequest(t, h, http.MethodDelete, "/api/entries/2026-08-01", nil)
	if deleted.StatusCode != http.StatusNoContent {
		body := readEntryBody(t, deleted)
		t.Fatalf("DELETE status = %d, want 204; body=%s", deleted.StatusCode, body)
	}
	deleted.Body.Close()
	missing := entryRequest(t, h, http.MethodGet, "/api/entries/2026-08-01", nil)
	assertErrorCode(t, missing, http.StatusNotFound, "entry_not_found")
	missingDelete := entryRequest(t, h, http.MethodDelete, "/api/entries/2026-08-01", nil)
	assertErrorCode(t, missingDelete, http.StatusNotFound, "entry_not_found")

	for _, path := range []string{"/api/entries/2026-02-30", "/api/entries/2026-8-02", "/api/entries?from=2026-02-30"} {
		response := entryRequest(t, h, http.MethodGet, path, nil)
		assertErrorCode(t, response, http.StatusBadRequest, "invalid_date")
	}
}

func TestEntryListDatesProjectionReturnsOnlyDates(t *testing.T) {
	h := api.NewTestHarness(t)
	putJSON(t, h, "2026-08-01", map[string]any{"text": "first", "gratitudes": []string{"a", "b", "c"}})
	putJSON(t, h, "2026-08-03", map[string]any{"text": "third"})

	var dates []map[string]any
	decodeJSON(t, entryRequest(t, h, http.MethodGet, "/api/entries?fields=date", nil), &dates)
	if len(dates) != 2 || dates[0]["date"] != "2026-08-01" || dates[1]["date"] != "2026-08-03" {
		t.Fatalf("entry dates = %#v, want both dates in order", dates)
	}
	for _, date := range dates {
		if len(date) != 1 {
			t.Fatalf("entry date document = %#v, want the date alone", date)
		}
	}

	decodeJSON(t, entryRequest(t, h, http.MethodGet, "/api/entries?fields=date&from=2026-08-02&to=2026-08-03", nil), &dates)
	if len(dates) != 1 || dates[0]["date"] != "2026-08-03" {
		t.Fatalf("ranged entry dates = %#v, want only 2026-08-03", dates)
	}

	for _, path := range []string{"/api/entries?fields=text", "/api/entries?fields=date,text", "/api/entries?fields=DATE"} {
		response := entryRequest(t, h, http.MethodGet, path, nil)
		assertError(t, response, http.StatusBadRequest, "invalid_entry", "fields must be date")
	}
	invalidRange := entryRequest(t, h, http.MethodGet, "/api/entries?fields=date&from=2026-02-30", nil)
	assertErrorCode(t, invalidRange, http.StatusBadRequest, "invalid_date")
}

func TestEntryPutRejectsInvalidShapesAndUndocumentedFields(t *testing.T) {
	h := api.NewTestHarness(t)
	tests := []struct {
		name    string
		body    string
		message string
	}{
		{name: "rating below minimum", body: `{"ratings":{"total":0}}`},
		{name: "rating above maximum", body: `{"ratings":{"total":6}}`},
		{name: "too few goals", body: `{"goals":[]}`},
		{name: "too many goals", body: `{"goals":[{}, {}, {}, {}, {}, {}]}`},
		{name: "too few gratitudes", body: `{"gratitudes":["one", "two"]}`},
		{name: "too many gratitudes", body: `{"gratitudes":["one", "two", "three", "four"]}`},
		{name: "checkoffs", body: `{"checkoffs":["habit"]}`, message: "check-offs are written via the dedicated check-off endpoints"},
		{name: "freeform alias", body: `{"freeform":"not text"}`},
		{name: "three ws alias", body: `{"three_ws":{"went_well":"not ws"}}`},
		{name: "camel case ws field", body: `{"ws":{"wentWell":"not went_well"}}`},
		{name: "unknown top-level field", body: `{"future_field":"not accepted"}`},
		{name: "field-specific decode error", body: `{"ws":{"went_well":3}}`, message: "invalid ws.went_well"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := entryRequest(t, h, http.MethodPut, "/api/entries/2026-08-02", []byte(tt.body))
			assertError(t, response, http.StatusBadRequest, "invalid_entry", tt.message)
		})
	}
}

func putJSON(t *testing.T, h *api.Harness, date string, value map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	response := entryRequest(t, h, http.MethodPut, "/api/entries/"+date, body)
	defer response.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatalf("PUT %s status=%d: %v", date, response.StatusCode, err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("PUT %s status=%d body=%#v", date, response.StatusCode, decoded)
	}
	return decoded
}

func assertErrorCode(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	assertError(t, response, status, code, "")
}

func assertError(t *testing.T, response *http.Response, status int, code, message string) {
	t.Helper()
	defer response.Body.Close()
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || envelope.Error.Code != code || (message != "" && !strings.Contains(envelope.Error.Message, message)) {
		t.Fatalf("error = status %d code %q message %q, want status %d code %q message containing %q", response.StatusCode, envelope.Error.Code, envelope.Error.Message, status, code, message)
	}
}

func readEntryBody(t *testing.T, response *http.Response) []byte {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func entryRequest(t *testing.T, h *api.Harness, method, path string, body []byte) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, h.Server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+h.Token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.Server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
