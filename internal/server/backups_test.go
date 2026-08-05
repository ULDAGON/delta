package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/api"
	"github.com/ferriskleier/delta/internal/server"
	"github.com/ferriskleier/delta/internal/service"
	"github.com/ferriskleier/delta/internal/storage"
)

func TestEntryWritesCreateOneDailyBackupPerCalendarDay(t *testing.T) {
	h := api.NewTestHarness(t)
	today := "2026-08-02"

	putJSON(t, h, today, map[string]any{"text": "first write"})
	dailyNames := backupNames(t, h.DBPath)
	if len(dailyNames) != 1 {
		t.Fatalf("daily snapshots after first write = %v, want one", dailyNames)
	}
	daily := filepath.Join(storage.BackupDirectory(h.DBPath), dailyNames[0])
	dailyStore, err := storage.Open(t.Context(), daily, h.Key)
	if err != nil {
		t.Fatalf("daily snapshot = %s: %v", daily, err)
	}
	if err := dailyStore.Close(); err != nil {
		t.Fatal(err)
	}

	putJSON(t, h, today, map[string]any{"text": "second write"})
	if names := backupNames(t, h.DBPath); len(names) != 1 {
		t.Fatalf("daily snapshots = %v, want exactly one", names)
	}
}

func TestManualBackupNeverOverwritesAndSnapshotReadsThroughREST(t *testing.T) {
	h := api.NewTestHarness(t)
	today := "2026-08-02"
	putJSON(t, h, today, map[string]any{"text": "portable snapshot"})

	first := entryRequest(t, h, http.MethodPost, "/api/backup", nil)
	firstBody := readEntryBody(t, first)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first manual backup status = %d, body=%s", first.StatusCode, firstBody)
	}
	second := entryRequest(t, h, http.MethodPost, "/api/backup", nil)
	secondBody := readEntryBody(t, second)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second manual backup status = %d, body=%s", second.StatusCode, secondBody)
	}

	var firstResult, secondResult struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(firstBody, &firstResult); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(secondBody, &secondResult); err != nil {
		t.Fatal(err)
	}
	if names := backupNames(t, h.DBPath); len(names) != 3 {
		t.Fatalf("all snapshots = %v, want daily plus two manual files", names)
	}
	for _, filename := range []string{firstResult.Filename, secondResult.Filename} {
		snapshotPath := filepath.Join(storage.BackupDirectory(h.DBPath), filename)
		snapshot, err := storage.Open(t.Context(), snapshotPath, h.Key)
		if err != nil {
			t.Fatalf("open snapshot %s: %v", snapshotPath, err)
		}
		snapshotHandler := server.NewHandler(service.New(snapshot), h.Token)
		snapshotServer := httptest.NewServer(snapshotHandler)
		request, err := http.NewRequest(http.MethodGet, snapshotServer.URL+"/api/entries/"+today, nil)
		if err != nil {
			_ = snapshot.Close()
			snapshotServer.Close()
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+h.Token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			_ = snapshot.Close()
			snapshotServer.Close()
			t.Fatal(err)
		}
		var entry service.Entry
		decodeErr := json.NewDecoder(response.Body).Decode(&entry)
		response.Body.Close()
		snapshotServer.Close()
		if err := snapshot.Close(); err != nil {
			t.Fatal(err)
		}
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if response.StatusCode != http.StatusOK || entry.Text != "portable snapshot" {
			t.Fatalf("snapshot entry = status %d %#v", response.StatusCode, entry)
		}
	}

	for _, result := range []struct {
		filename string
	}{
		{filename: firstResult.Filename},
		{filename: secondResult.Filename},
	} {
		if !strings.HasSuffix(result.filename, ".db") || result.filename == "" || result.filename == "delta-"+today+".db" {
			t.Fatalf("manual backup filename = %q", result.filename)
		}
	}
}

func backupNames(t *testing.T, databasePath string) []string {
	t.Helper()
	entries, err := os.ReadDir(storage.BackupDirectory(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "delta-") && strings.HasSuffix(entry.Name(), ".db") {
			names = append(names, entry.Name())
		}
	}
	return names
}
