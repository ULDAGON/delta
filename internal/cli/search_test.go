package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/api"
	"github.com/ferriskleier/delta/internal/cli"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/service"
)

func TestSearchCLIJSONIsThinHTTPClient(t *testing.T) {
	h := api.NewTestHarness(t)
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	if err := config.Save(config.Config{DatabasePath: h.DBPath, Key: h.Key, APIToken: h.Token, APIAddress: h.Server.URL}); err != nil {
		t.Fatal(err)
	}
	putSearchEntry(t, h, "2026-08-02", `{"text":"CLI needle result"}`)

	var stdout, stderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"search", "needle", "--json"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var results []service.SearchResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("search JSON = %q: %v", stdout.String(), err)
	}
	if len(results) != 1 || results[0].Date != "2026-08-02" || results[0].Field != "freeform" {
		t.Fatalf("search results = %#v", results)
	}
}

func putSearchEntry(t *testing.T, h *api.Harness, date, body string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, h.Server.URL+"/api/entries/"+date, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+h.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := h.Server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("PUT %s status = %d", date, response.StatusCode)
	}
}
