package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/storage"
)

// The importer's own safety net must land where the diary's backups are
// configured to live, exactly like `delta serve`.
func TestRunSnapshotsIntoTheConfiguredBackupsDirectory(t *testing.T) {
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	directory := filepath.Join(t.TempDir(), "elsewhere", "backups")
	databasePath := filepath.Join(t.TempDir(), "diary.db")
	c, err := config.New(databasePath, strings.Repeat("a1", storage.KeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	c.BackupsPath = directory
	if err := config.Save(c); err != nil {
		t.Fatal(err)
	}
	valuesPath := filepath.Join(t.TempDir(), "values.json")
	if err := os.WriteFile(valuesPath, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := run("values", []string{valuesPath}); err != nil {
		t.Fatal(err)
	}

	names, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read configured backups directory: %v", err)
	}
	var migration, importBackup bool
	for _, name := range names {
		if strings.HasPrefix(name.Name(), "pre-migrate-v0-") {
			migration = true
		}
		if strings.HasPrefix(name.Name(), "delta-") {
			importBackup = true
		}
	}
	if !migration || !importBackup {
		listed := make([]string, 0, len(names))
		for _, name := range names {
			listed = append(listed, name.Name())
		}
		t.Fatalf("configured backups directory holds %v, want the pre-migrate and pre-import snapshots", listed)
	}
	if _, err := os.Stat(storage.BackupDirectory(databasePath)); !os.IsNotExist(err) {
		t.Fatalf("derived backups directory stat = %v, want nothing written beside the database", err)
	}
}
