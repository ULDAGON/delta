package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/api"
	"github.com/ferriskleier/delta/internal/cli"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/storage"
)

func TestBackupCLIUsesRunningHTTPServerAndJSON(t *testing.T) {
	h := api.NewTestHarness(t)
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	if err := config.Save(config.Config{DatabasePath: h.DBPath, Key: h.Key, APIToken: h.Token, APIAddress: h.Server.URL}); err != nil {
		t.Fatal(err)
	}
	today := "2026-08-02"
	request, err := http.NewRequest(http.MethodPut, h.Server.URL+"/api/entries/"+today, strings.NewReader(`{"text":"CLI backup"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+h.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := h.Server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("entry write status = %d", response.StatusCode)
	}

	var first, second bytes.Buffer
	if err := cli.Run(context.Background(), []string{"backup", "--json"}, strings.NewReader(""), &first, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := cli.Run(context.Background(), []string{"backup", "--json"}, strings.NewReader(""), &second, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var results []struct {
		Filename string `json:"filename"`
	}
	for _, output := range []bytes.Buffer{first, second} {
		var result struct {
			Filename string `json:"filename"`
		}
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatalf("backup JSON = %q: %v", output.Bytes(), err)
		}
		if result.Filename == "" {
			t.Fatalf("backup JSON = %q", output.Bytes())
		}
		if result.Filename == "delta-"+today+".db" {
			t.Fatalf("manual backup used reserved daily filename: %q", result.Filename)
		}
		results = append(results, result)
	}
	entries, err := os.ReadDir(storage.BackupDirectory(h.DBPath))
	if err != nil {
		t.Fatal(err)
	}
	var snapshots []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "delta-") && strings.HasSuffix(entry.Name(), ".db") {
			snapshots = append(snapshots, entry.Name())
		}
	}
	if len(snapshots) != 3 {
		t.Fatalf("all backups = %v, want daily plus two manual files", snapshots)
	}
	for _, result := range results {
		if _, err := os.Stat(filepath.Join(storage.BackupDirectory(h.DBPath), result.Filename)); err != nil {
			t.Fatalf("manual backup %q: %v", result.Filename, err)
		}
	}
}
