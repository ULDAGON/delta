package server_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/api"
)

type searchResult struct {
	Date    string `json:"date"`
	Field   string `json:"field"`
	Snippet string `json:"snippet"`
}

func TestSearchCoversTextFieldsWithAttributionAndExcludesNonText(t *testing.T) {
	h := api.NewTestHarness(t)
	putJSON(t, h, "2026-08-01", map[string]any{
		"text":       "alpha needlework in freeform",
		"gratitudes": []string{"needle gratitude one", "needle gratitude two", "needle gratitude three"},
		"ws": map[string]any{
			"went_well":              "needle went well",
			"could_have_gone_better": "needle could improve",
			"goal_for_tomorrow":      "needle tomorrow",
		},
		"goals": []map[string]any{
			{"text": "needle goal one"}, {"text": "needle goal two"}, {"text": "needle goal three"},
			{"text": "needle goal four"}, {"text": "needle goal five"},
		},
		"ratings": map[string]any{"total": 5},
	})

	response := searchRequest(t, h, "needle")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("search status = %d", response.StatusCode)
	}
	var results []searchResult
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 12 {
		t.Fatalf("search results = %#v, want one row for each matching textual field", results)
	}
	wantFields := []string{
		"freeform", "gratitude",
		"went well", "could be better", "goal tomorrow",
		"goal",
	}
	seen := make(map[string]bool)
	for _, result := range results {
		seen[result.Field] = true
		if !strings.Contains(result.Snippet, "<mark>") || !strings.Contains(result.Snippet, "</mark>") {
			t.Fatalf("result %#v has no highlighted snippet", result)
		}
		if result.Date != "2026-08-01" {
			t.Fatalf("result date = %q", result.Date)
		}
	}
	for _, field := range wantFields {
		if !seen[field] {
			t.Fatalf("fields = %#v, missing %q", seen, field)
		}
	}
	if seen["rating"] || seen["habit"] {
		t.Fatalf("non-text field appeared in results: %#v", seen)
	}

	habitRequest := postHabitRequest(t, h, `{"name":"needle habit"}`)
	habitRequest.Body.Close()
	if habitRequest.StatusCode != http.StatusCreated && habitRequest.StatusCode != http.StatusOK {
		t.Fatalf("habit create status = %d", habitRequest.StatusCode)
	}
	response = searchRequest(t, h, "habit")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("habit-name search status = %d", response.StatusCode)
	}
	results = nil
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("habit-name search results = %#v, want none", results)
	}
	response = searchRequest(t, h, "5")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("rating search status = %d", response.StatusCode)
	}
	defer response.Body.Close()
	results = nil
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("rating/habit search results = %#v, want none", results)
	}
}

func TestSearchUsesImplicitAndLastWordPrefixNewestFirst(t *testing.T) {
	h := api.NewTestHarness(t)
	putJSON(t, h, "2026-08-01", map[string]any{"text": "alpha needlework"})
	putJSON(t, h, "2026-08-03", map[string]any{"text": "alpha needlebox"})
	putJSON(t, h, "2026-08-02", map[string]any{"text": "alpha only"})

	response := searchRequest(t, h, "alpha needle")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("search status = %d", response.StatusCode)
	}
	var results []searchResult
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Date != "2026-08-03" || results[1].Date != "2026-08-01" {
		t.Fatalf("prefix/AND results = %#v, want 2026-08-03 then 2026-08-01", results)
	}
}

func TestSearchNeverReturnsSyntaxErrorsAndTracksUpdateDelete(t *testing.T) {
	h := api.NewTestHarness(t)
	putJSON(t, h, "2026-08-01", map[string]any{"text": "old search term"})

	for _, query := range []string{`"`, `-foo`, `a:b`, `(x OR y)`, `*`, "unicode café", "😀", "", "!!!"} {
		response := searchRequest(t, h, query)
		if response.StatusCode != http.StatusOK {
			body := readEntryBody(t, response)
			t.Fatalf("query %q status = %d body = %s", query, response.StatusCode, body)
		}
		var results []searchResult
		if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
			t.Fatalf("query %q response = %v", query, err)
		}
		if results == nil || len(results) != 0 {
			t.Fatalf("query %q results = %#v, want an empty array", query, results)
		}
		response.Body.Close()
	}

	response := searchRequest(t, h, "old")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("old-term search status = %d", response.StatusCode)
	}
	var results []searchResult
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("old term before update did not match the seeded entry")
	}
	response.Body.Close()

	putJSON(t, h, "2026-08-01", map[string]any{"text": "new search term"})
	response = searchRequest(t, h, "new")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("new-term search status = %d", response.StatusCode)
	}
	results = nil
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(results) == 0 {
		t.Fatal("new term after update did not match the updated entry")
	}
	response = searchRequest(t, h, "old")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("old-term-after-update search status = %d", response.StatusCode)
	}
	results = nil
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(results) != 0 {
		t.Fatalf("old term after update = %#v", results)
	}

	deleteResponse := entryRequest(t, h, http.MethodDelete, "/api/entries/2026-08-01", nil)
	deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", deleteResponse.StatusCode)
	}
	response = searchRequest(t, h, "new")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("new-term-after-delete search status = %d", response.StatusCode)
	}
	results = nil
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("new term after delete = %#v", results)
	}
}

func TestSearchOnlyPrefixesLastWord(t *testing.T) {
	h := api.NewTestHarness(t)
	putJSON(t, h, "2026-08-01", map[string]any{"text": "needlework done"})

	response := searchRequest(t, h, "needle done")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("non-last-word search status = %d", response.StatusCode)
	}
	var results []searchResult
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(results) != 0 {
		t.Fatalf("non-last-word prefix results = %#v, want none", results)
	}

	response = searchRequest(t, h, "done needle")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("last-word search status = %d", response.StatusCode)
	}
	results = nil
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(results) != 1 || results[0].Date != "2026-08-01" {
		t.Fatalf("last-word prefix results = %#v, want only 2026-08-01", results)
	}
}

func TestSearchBoundsHugeQueries(t *testing.T) {
	h := api.NewTestHarness(t)
	putJSON(t, h, "2026-08-01", map[string]any{"text": "needle"})

	query := strings.Repeat("needle ", 16) + strings.Repeat("ignored ", 4096)
	response := searchRequest(t, h, query)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("huge query status = %d", response.StatusCode)
	}
	var results []searchResult
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatalf("huge query results = %#v, want the bounded first 16 terms to match", results)
	}
}

func TestSearchDoesNotAttributeLiteralMarkers(t *testing.T) {
	h := api.NewTestHarness(t)
	putJSON(t, h, "2026-08-01", map[string]any{
		"text":       "needle <mark> literal text",
		"gratitudes": []string{"", "", ""},
	})

	response := searchRequest(t, h, "needle")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("literal-marker search status = %d", response.StatusCode)
	}
	var results []searchResult
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Field != "freeform" {
		t.Fatalf("literal-marker attribution = %#v, want only freeform", results)
	}
	if !strings.Contains(results[0].Snippet, "&lt;mark&gt;") {
		t.Fatalf("literal-marker snippet = %q, want escaped diary marker", results[0].Snippet)
	}
}

func searchRequest(t *testing.T, h *api.Harness, query string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.Server.URL+"/api/search?q="+url.QueryEscape(query), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+h.Token)
	response, err := h.Server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func postHabitRequest(t *testing.T, h *api.Harness, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, h.Server.URL+"/api/habits", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+h.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := h.Server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
