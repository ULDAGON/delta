package storage_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/storage"
)

func TestEncryptedFTS5AndDeleteJournal(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "diary.db")
	key := strings.Repeat("ab", storage.KeyBytes)
	store, err := storage.Open(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	_, err = store.DB.Exec(`CREATE VIRTUAL TABLE diary_fts USING fts5(body)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO diary_fts(body) VALUES (?)`, "encrypted search works"); err != nil {
		t.Fatal(err)
	}
	var body string
	if err := store.DB.QueryRow(`SELECT body FROM diary_fts WHERE diary_fts MATCH 'search'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "encrypted search works" {
		t.Fatalf("body = %q", body)
	}

	if raw, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if strings.HasPrefix(string(raw), "SQLite format 3") {
		t.Fatal("database header is plaintext")
	}
	if _, err := os.Stat(path + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("unexpected -wal sidecar, stat error = %v", err)
	}
	if _, err := os.Stat(path + "-shm"); !os.IsNotExist(err) {
		t.Fatalf("unexpected -shm sidecar, stat error = %v", err)
	}
}

func TestWrongAndWhitespaceKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diary.db")
	key := strings.Repeat("01", storage.KeyBytes)
	store, err := storage.Open(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	grouped := strings.Join([]string{key[:16], key[16:32], key[32:48], key[48:]}, " \n ")
	store, err = storage.Open(context.Background(), path, grouped)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	_, err = storage.Open(context.Background(), path, strings.Repeat("02", storage.KeyBytes))
	if apperror.Code(err) != apperror.CodeWrongKey || apperror.Message(err) != apperror.WrongKeyMessage {
		t.Fatalf("wrong key error = %v (code %q, message %q)", err, apperror.Code(err), apperror.Message(err))
	}
	if _, err := storage.Open(context.Background(), path, hex.EncodeToString([]byte("short"))); apperror.Code(err) != apperror.CodeWrongKey {
		t.Fatalf("malformed key error = %v", err)
	}
}

func TestMigrationsRefuseNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diary.db")
	key := strings.Repeat("03", storage.KeyBytes)
	store, err := storage.Open(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	store, err = storage.Open(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	err = storage.Migrate(context.Background(), store.DB)
	if apperror.Code(err) != apperror.CodeUpgrade || !strings.Contains(apperror.Message(err), "upgrade delta") {
		t.Fatalf("newer schema error = %v (code %q, message %q)", err, apperror.Code(err), apperror.Message(err))
	}
}

func TestSnapshotIsEncryptedAndReopenable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diary.db")
	key := strings.Repeat("04", storage.KeyBytes)
	store, err := storage.Open(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`CREATE TABLE notes(body TEXT); INSERT INTO notes(body) VALUES ('snapshot text')`); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "backups", "pre-migrate-v1-test.db")
	if err := store.Snapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(snapshot); err != nil {
		t.Fatal(err)
	} else if strings.HasPrefix(string(raw), "SQLite format 3") {
		t.Fatal("snapshot header is plaintext")
	}
	copyStore, err := storage.Open(context.Background(), snapshot, key)
	if err != nil {
		t.Fatal(err)
	}
	defer copyStore.Close()
	var body string
	if err := copyStore.DB.QueryRow(`SELECT body FROM notes`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "snapshot text" {
		t.Fatalf("snapshot body = %q", body)
	}
}

func TestMigrateStoreCreatesPreMigrationSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diary.db")
	key := strings.Repeat("05", storage.KeyBytes)
	store, err := storage.Open(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.MigrateStore(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups", "pre-migrate-v0-*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("pre-migration snapshots = %v, want one", matches)
	}
	snapshot, err := storage.Open(context.Background(), matches[0], key)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if err := storage.Migrate(context.Background(), snapshot.DB); err != nil {
		t.Fatal(err)
	}
}

func TestFTSRebuildsExistingEntriesWhenMigrationRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diary.db")
	key := strings.Repeat("06", storage.KeyBytes)
	store, err := storage.Open(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`
		DROP TRIGGER entries_fts_after_insert;
		DROP TRIGGER entries_fts_after_delete;
		DROP TRIGGER entries_fts_after_update;
		DROP TABLE fts;
		INSERT INTO entries(date, text) VALUES ('2026-08-02', 'legacy migration text');
		PRAGMA user_version = 3;
	`); err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	var matches int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM fts WHERE fts MATCH 'legacy'`).Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if matches != 1 {
		t.Fatalf("rebuilt FTS matches = %d, want one existing entry", matches)
	}
}

func TestWorkHoursMigrationKeepsLegacyEntriesUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diary.db")
	key := strings.Repeat("08", storage.KeyBytes)
	store, err := storage.Open(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}

	legacyVersion := storage.CurrentVersion() - 1
	if _, err := store.DB.Exec(`
		ALTER TABLE entries DROP COLUMN work_hours;
		INSERT INTO entries(date, text) VALUES ('2026-08-02', 'entry written before work hours');
		PRAGMA user_version = ` + strconv.Itoa(legacyVersion) + `;
	`); err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	var text string
	var workHours sql.NullFloat64
	if err := store.DB.QueryRow(`SELECT text, work_hours FROM entries WHERE date = '2026-08-02'`).Scan(&text, &workHours); err != nil {
		t.Fatal(err)
	}
	if text != "entry written before work hours" || workHours.Valid {
		t.Fatalf("legacy entry = %q work_hours %#v, want unchanged text and NULL work hours", text, workHours)
	}
	if _, err := store.DB.Exec(`UPDATE entries SET work_hours = 7.5 WHERE date = '2026-08-02'`); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(`SELECT work_hours FROM entries WHERE date = '2026-08-02'`).Scan(&workHours); err != nil {
		t.Fatal(err)
	}
	if !workHours.Valid || workHours.Float64 != 7.5 {
		t.Fatalf("stored work hours = %#v, want 7.5 as a decimal", workHours)
	}

	// Re-running the migration over a database that already has the column
	// must be a no-op rather than a duplicate-column failure.
	if _, err := store.DB.Exec(`PRAGMA user_version = ` + strconv.Itoa(legacyVersion)); err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatalf("re-running the work-hours migration = %v, want a no-op", err)
	}
}

func TestFTSUpdateTriggerOnlyListsTextualColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diary.db")
	key := strings.Repeat("07", storage.KeyBytes)
	store, err := storage.Open(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}

	var triggerSQL string
	if err := store.DB.QueryRow(`
		SELECT sql FROM sqlite_master
		WHERE type = 'trigger' AND name = 'entries_fts_after_update'`).Scan(&triggerSQL); err != nil {
		t.Fatal(err)
	}
	wantColumns := "AFTER UPDATE OF date, text, gratitude1, gratitude2, gratitude3, went_well, could_have_gone_better, goal_for_tomorrow, goal1_text, goal2_text, goal3_text, goal4_text, goal5_text ON entries"
	if !strings.Contains(triggerSQL, wantColumns) {
		t.Fatalf("update trigger SQL = %q, want textual column list", triggerSQL)
	}
	for _, nonTextColumn := range []string{"rating_total", "rating_body", "rating_mind", "rating_spirit", "checkoffs"} {
		if strings.Contains(triggerSQL, nonTextColumn) {
			t.Fatalf("update trigger SQL = %q, unexpectedly includes %q", triggerSQL, nonTextColumn)
		}
	}
}
