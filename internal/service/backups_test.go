package service

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/storage"
)

func TestBackupFailureDoesNotFailEntryWrite(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 34, 56, 0, time.Local)
	svc, store := newBackupTestService(t, &now)
	if err := os.WriteFile(storage.BackupDirectory(store.Path), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	entry, err := svc.UpsertEntry(t.Context(), "2026-08-02", EntryPatch{
		Text: OptionalString{Set: true, Value: "saved despite backup failure"},
	})
	if err != nil {
		t.Fatalf("entry write = %v, want success", err)
	}
	if entry.Text != "saved despite backup failure" {
		t.Fatalf("entry text = %q", entry.Text)
	}
	if svc.LastBackupError() == nil {
		t.Fatal("LastBackupError() = nil, want recorded backup failure")
	}
	if _, err := svc.GetEntry(t.Context(), "2026-08-02"); err != nil {
		t.Fatalf("read committed entry = %v", err)
	}
}

func TestDailySnapshotCapturesStateBeforeFirstMutation(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 34, 56, 0, time.Local)
	svc, store := newBackupTestService(t, &now)
	if _, err := svc.UpsertEntry(t.Context(), "2026-08-02", EntryPatch{
		Text: OptionalString{Set: true, Value: "state at day end"},
	}); err != nil {
		t.Fatal(err)
	}

	now = time.Date(2026, 8, 3, 0, 0, 1, 0, time.Local)
	if err := svc.DeleteEntry(t.Context(), "2026-08-02"); err != nil {
		t.Fatal(err)
	}

	dailyPath := filepath.Join(storage.BackupDirectory(store.Path), "delta-2026-08-03.db")
	snapshot, err := storage.Open(t.Context(), dailyPath, store.Key)
	if err != nil {
		t.Fatalf("open daily snapshot = %v", err)
	}
	t.Cleanup(func() { _ = snapshot.Close() })
	snapshotService := New(snapshot)
	entry, err := snapshotService.GetEntry(t.Context(), "2026-08-02")
	if err != nil {
		t.Fatalf("read pre-delete snapshot = %v", err)
	}
	if entry.Text != "state at day end" {
		t.Fatalf("snapshot entry text = %q", entry.Text)
	}
}

func TestManualBackupsUseDeterministicTimestampCollisionSuffix(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 34, 56, 0, time.Local)
	svc, _ := newBackupTestService(t, &now)

	first, err := svc.CreateBackup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateBackup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first.Filename != "delta-2026-08-02-123456.db" {
		t.Fatalf("first manual filename = %q", first.Filename)
	}
	if second.Filename != "delta-2026-08-02-123456-1.db" {
		t.Fatalf("second manual filename = %q", second.Filename)
	}
}

func TestConcurrentFirstWritesCreateOneDailySnapshot(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 34, 56, 0, time.Local)
	svc, store := newBackupTestService(t, &now)

	var wait sync.WaitGroup
	errs := make(chan error, 2)
	for _, date := range []string{"2026-08-02", "2026-08-03"} {
		wait.Add(1)
		go func(date string) {
			defer wait.Done()
			_, err := svc.UpsertEntry(t.Context(), date, EntryPatch{
				Text: OptionalString{Set: true, Value: date},
			})
			errs <- err
		}(date)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent entry write = %v", err)
		}
	}

	files := backupFiles(t, store)
	if len(files) != 1 || files[0] != "delta-2026-08-02.db" {
		t.Fatalf("daily snapshots = %v, want one daily snapshot", files)
	}
}

func TestConfiguredBackupsPathReceivesEverySnapshot(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 34, 56, 0, time.Local)
	directory := filepath.Join(t.TempDir(), "elsewhere", "backups")
	svc, store := newBackupTestService(t, &now, WithBackupsPath(directory))
	if _, err := svc.UpsertEntry(t.Context(), "2026-08-02", EntryPatch{
		Text: OptionalString{Set: true, Value: "configured backups"},
	}); err != nil {
		t.Fatal(err)
	}
	manual, err := svc.CreateBackup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(manual.Path) != directory {
		t.Fatalf("manual backup path = %q, want a file in %q", manual.Path, directory)
	}
	if _, err := os.Stat(filepath.Join(directory, "delta-2026-08-02.db")); err != nil {
		t.Fatalf("daily snapshot in configured directory: %v", err)
	}
	if _, err := os.Stat(storage.BackupDirectory(store.Path)); !os.IsNotExist(err) {
		t.Fatalf("derived backups directory stat = %v, want not exist", err)
	}
}

func TestSetBackupsPathMovesLaterSnapshotsAndReseedsLastBackup(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 34, 56, 0, time.Local)
	svc, store := newBackupTestService(t, &now)
	if _, err := svc.CreateBackup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if svc.LastBackup() == nil {
		t.Fatal("LastBackup() = nil after a manual backup")
	}

	directory := filepath.Join(t.TempDir(), "elsewhere", "backups")
	svc.SetBackupsPath(directory)
	if last := svc.LastBackup(); last != nil {
		t.Fatalf("LastBackup() = %v, want none seeded from the empty new directory", last)
	}
	manual, err := svc.CreateBackup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(manual.Path) != directory {
		t.Fatalf("manual backup path = %q, want a file in %q", manual.Path, directory)
	}
	if _, err := svc.UpsertEntry(t.Context(), "2026-08-02", EntryPatch{
		Text: OptionalString{Set: true, Value: "written after the switch"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "delta-2026-08-02.db")); err != nil {
		t.Fatalf("daily snapshot in the new directory: %v", err)
	}

	// Returning to the derived directory reports the snapshot it still holds,
	// which is what the next start would read.
	svc.SetBackupsPath("")
	if svc.LastBackup() == nil {
		t.Fatalf("LastBackup() = nil, want the snapshot retained in %q", storage.BackupDirectory(store.Path))
	}
}

func newBackupTestService(t *testing.T, now *time.Time, options ...Option) (*Service, *storage.Store) {
	t.Helper()
	previousNow := serviceNow
	serviceNow = func() time.Time { return *now }
	t.Cleanup(func() { serviceNow = previousNow })

	path := filepath.Join(t.TempDir(), "diary.db")
	key := strings.Repeat("a1", storage.KeyBytes)
	store, err := storage.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := storage.Migrate(t.Context(), store.DB); err != nil {
		t.Fatal(err)
	}
	return New(store, options...), store
}

func backupFiles(t *testing.T, store *storage.Store) []string {
	t.Helper()
	entries, err := os.ReadDir(storage.BackupDirectory(store.Path))
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "delta-") && strings.HasSuffix(entry.Name(), ".db") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	return files
}
