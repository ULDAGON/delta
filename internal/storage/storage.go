// Package storage owns the encrypted SQLite connection and schema migrations.
package storage

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/ferriskleier/delta/internal/apperror"
	searchschema "github.com/ferriskleier/delta/internal/search"
	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
	_ "github.com/ncruces/go-sqlite3/vfs/adiantum"
)

const (
	KeyBytes     = 32
	KeyHexLength = KeyBytes * 2
)

// Store is one encrypted DELTA database. The connection pool is deliberately
// limited to one connection: the server is a single writer and this also
// keeps short DELETE-journal transactions easy to reason about.
type Store struct {
	DB   *sql.DB
	Path string
	Key  string
}

// NormalizeKey removes whitespace so pasted/grouped keys can be used safely.
func NormalizeKey(input string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, input)
}

func ValidateKey(input string) error {
	key := NormalizeKey(input)
	if len(key) != KeyHexLength {
		return apperror.New(apperror.CodeWrongKey, apperror.WrongKeyMessage)
	}
	if _, err := hex.DecodeString(key); err != nil {
		return apperror.Wrap(apperror.CodeWrongKey, apperror.WrongKeyMessage, err)
	}
	return nil
}

// Open opens an encrypted database. It does not run migrations; callers
// should call Migrate at the process startup boundary.
func Open(ctx context.Context, path, key string) (*Store, error) {
	key = NormalizeKey(key)
	if err := ValidateKey(key); err != nil {
		return nil, err
	}

	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	dsn := databaseURI(path, key)
	db, err := driver.Open(dsn, fts5.Register)
	if err != nil {
		return nil, wrongKeyIfSQLite(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, wrongKeyIfSQLite(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set database permissions: %w", err)
	}
	return &Store{DB: db, Path: path, Key: key}, nil
}

func databaseURI(path, key string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("vfs", "adiantum")
	q.Set("textkey", key)
	q.Add("_pragma", "journal_mode(DELETE)")
	q.Add("_pragma", "temp_store(MEMORY)")
	u.RawQuery = q.Encode()
	return u.String()
}

func wrongKeyIfSQLite(err error) error {
	if err == nil {
		return nil
	}
	var sqliteErr *sqlite3.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.NOTADB {
		return apperror.Wrap(apperror.CodeWrongKey, apperror.WrongKeyMessage, err)
	}
	var sqliteCode sqlite3.ErrorCode
	if errors.As(err, &sqliteCode) && sqliteCode == sqlite3.NOTADB {
		return apperror.Wrap(apperror.CodeWrongKey, apperror.WrongKeyMessage, err)
	}
	return err
}

// Migration is one forward-only schema change. It runs inside the
// transaction supplied by the migration runner.
type Migration func(context.Context, *sql.Tx) error

var migrations = []Migration{
	func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS delta_metadata (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL
			)`)
		return err
	},
	func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS entries (
				date TEXT PRIMARY KEY NOT NULL,
				text TEXT NOT NULL DEFAULT '',
				goal1_text TEXT NOT NULL DEFAULT '',
				goal1_checked INTEGER NOT NULL DEFAULT 0,
				goal2_text TEXT NOT NULL DEFAULT '',
				goal2_checked INTEGER NOT NULL DEFAULT 0,
				goal3_text TEXT NOT NULL DEFAULT '',
				goal3_checked INTEGER NOT NULL DEFAULT 0,
				goal4_text TEXT NOT NULL DEFAULT '',
				goal4_checked INTEGER NOT NULL DEFAULT 0,
				goal5_text TEXT NOT NULL DEFAULT '',
				goal5_checked INTEGER NOT NULL DEFAULT 0,
				gratitude1 TEXT NOT NULL DEFAULT '',
				gratitude2 TEXT NOT NULL DEFAULT '',
				gratitude3 TEXT NOT NULL DEFAULT '',
				went_well TEXT NOT NULL DEFAULT '',
				could_have_gone_better TEXT NOT NULL DEFAULT '',
				goal_for_tomorrow TEXT NOT NULL DEFAULT '',
				rating_total INTEGER,
				rating_body INTEGER,
				rating_mind INTEGER,
				rating_spirit INTEGER,
				checkoffs TEXT NOT NULL DEFAULT '[]'
			)`)
		return err
	},
	func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS habits (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				position INTEGER NOT NULL
			);
			CREATE TABLE IF NOT EXISTS habit_ranges (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				habit_id INTEGER NOT NULL,
				active_from TEXT NOT NULL,
				active_to TEXT,
				FOREIGN KEY (habit_id) REFERENCES habits(id) ON DELETE CASCADE,
				CHECK (active_to IS NULL OR active_from <= active_to)
			);
			CREATE INDEX IF NOT EXISTS habit_ranges_by_habit ON habit_ranges(habit_id, active_from);
			CREATE INDEX IF NOT EXISTS habit_ranges_by_day ON habit_ranges(active_from, active_to);
		`)
		return err
	},
	func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, fmt.Sprintf(`
			CREATE VIRTUAL TABLE IF NOT EXISTS fts USING fts5(
				date UNINDEXED,
				%s,
				content='entries',
				content_rowid='rowid'
			);`, strings.Join(ftsSearchColumns(), ",\n\t\t\t\t")))
		return err
	},
	func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, createFTSTriggersSQL())
		return err
	},
	func(ctx context.Context, tx *sql.Tx) error {
		var hasPixel int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('entries') WHERE name = 'pixel'`).Scan(&hasPixel); err != nil {
			return err
		}
		if hasPixel > 0 {
			return nil
		}
		_, err := tx.ExecContext(ctx, `
			ALTER TABLE entries ADD COLUMN pixel INTEGER NOT NULL DEFAULT 0`)
		return err
	},
	func(ctx context.Context, tx *sql.Tx) error {
		var hasWorkHours int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('entries') WHERE name = 'work_hours'`).Scan(&hasWorkHours); err != nil {
			return err
		}
		if hasWorkHours > 0 {
			return nil
		}
		// Nullable with no default on purpose: every pre-existing entry keeps an
		// absent value, which never means a recorded zero-hour day.
		_, err := tx.ExecContext(ctx, `
			ALTER TABLE entries ADD COLUMN work_hours REAL`)
		return err
	},
}

func CurrentVersion() int { return len(migrations) }

// Migrate brings db forward to the binary's schema version. Each migration
// and its user_version bump are one transaction. A newer database is refused
// before any write occurs.
func Migrate(ctx context.Context, db *sql.DB) error {
	return RunMigrations(ctx, db, migrations)
}

// MigrateStore snapshots the encrypted database before applying pending
// migrations, then runs the transactional migration runner. A fresh database
// also gets a snapshot of schema version zero, which makes every migration
// boundary restorable.
func MigrateStore(ctx context.Context, store *Store) error {
	if store == nil || store.DB == nil {
		return apperror.New(apperror.CodeInternalError, "storage is not open")
	}
	version, err := userVersion(ctx, store.DB)
	if err != nil {
		return wrongKeyIfSQLite(err)
	}
	if err := checkSchemaVersion(version, CurrentVersion()); err != nil {
		return err
	}
	if version < CurrentVersion() {
		if err := store.Snapshot(ctx, migrationSnapshotPath(store.Path, version)); err != nil {
			return fmt.Errorf("create pre-migration snapshot: %w", err)
		}
	}
	return runMigrationsAtVersion(ctx, store.DB, migrations, version)
}

func userVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

func checkSchemaVersion(version, supported int) error {
	if version > supported {
		return apperror.New(apperror.CodeUpgrade,
			"this database was created by a newer DELTA; upgrade delta before opening it")
	}
	return nil
}

func migrationSnapshotPath(databasePath string, version int) string {
	stamp := time.Now().Format("20060102-150405")
	return filepath.Join(BackupDirectory(databasePath), fmt.Sprintf("pre-migrate-v%d-%s.db", version, stamp))
}

// BackupDirectory is the shared location for automatic, manual, and
// pre-migration snapshots belonging to one database.
func BackupDirectory(databasePath string) string {
	return filepath.Join(filepath.Dir(databasePath), "backups")
}

// Snapshot writes a same-key encrypted VACUUM INTO copy. The destination is
// never overwritten; this preserves every recovery point.
func (s *Store) Snapshot(ctx context.Context, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("snapshot already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	dsn := databaseURI(destination, s.Key)
	literal := "'" + strings.ReplaceAll(dsn, "'", "''") + "'"
	if _, err := s.DB.ExecContext(ctx, "VACUUM INTO "+literal); err != nil {
		return err
	}
	return os.Chmod(destination, 0o600)
}

func RunMigrations(ctx context.Context, db *sql.DB, steps []Migration) error {
	version, err := userVersion(ctx, db)
	if err != nil {
		return wrongKeyIfSQLite(err)
	}
	if err := checkSchemaVersion(version, len(steps)); err != nil {
		return err
	}
	return runMigrationsAtVersion(ctx, db, steps, version)
}

func runMigrationsAtVersion(ctx context.Context, db *sql.DB, steps []Migration, version int) error {
	for i := version; i < len(steps); i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return wrongKeyIfSQLite(err)
		}
		if err := steps[i](ctx, tx); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if version == len(steps) {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return wrongKeyIfSQLite(err)
	}
	if err := rebuildFTSIfPresent(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func ftsSearchColumns() []string {
	columns := make([]string, 0, len(searchschema.Fields))
	for _, field := range searchschema.Fields {
		columns = append(columns, field.Column)
	}
	return columns
}

func ftsContentColumns() []string {
	return append([]string{"date"}, ftsSearchColumns()...)
}

func ftsInsertTriggerSQL(rowPrefix string) string {
	columns := strings.Join(ftsContentColumns(), ", ")
	values := make([]string, 0, len(ftsContentColumns())+1)
	values = append(values, rowPrefix+".rowid")
	for _, column := range ftsContentColumns() {
		values = append(values, rowPrefix+"."+column)
	}
	return fmt.Sprintf("INSERT INTO fts(rowid, %s) VALUES (%s);", columns, strings.Join(values, ", "))
}

func ftsDeleteTriggerSQL(rowPrefix string) string {
	columns := strings.Join(ftsContentColumns(), ", ")
	values := make([]string, 0, len(ftsContentColumns())+1)
	values = append(values, rowPrefix+".rowid")
	for _, column := range ftsContentColumns() {
		values = append(values, rowPrefix+"."+column)
	}
	return fmt.Sprintf("INSERT INTO fts(fts, rowid, %s) VALUES ('delete', %s);", columns, strings.Join(values, ", "))
}

func createFTSTriggersSQL() string {
	columns := strings.Join(ftsContentColumns(), ", ")
	return fmt.Sprintf(`
		DROP TRIGGER IF EXISTS entries_fts_after_insert;
		DROP TRIGGER IF EXISTS entries_fts_after_delete;
		DROP TRIGGER IF EXISTS entries_fts_after_update;

		CREATE TRIGGER entries_fts_after_insert
		AFTER INSERT ON entries BEGIN
			%s
		END;

		CREATE TRIGGER entries_fts_after_delete
		AFTER DELETE ON entries BEGIN
			%s
		END;

		CREATE TRIGGER entries_fts_after_update
		AFTER UPDATE OF %s ON entries BEGIN
			%s
			%s
		END;
	`, ftsInsertTriggerSQL("new"), ftsDeleteTriggerSQL("old"), columns,
		ftsDeleteTriggerSQL("old"), ftsInsertTriggerSQL("new"))
}

func rebuildFTSIfPresent(ctx context.Context, tx *sql.Tx) error {
	var present bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name = 'fts')`).Scan(&present); err != nil {
		return err
	}
	if !present {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO fts(fts) VALUES ('rebuild')`)
	return err
}

func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

func IsWrongKey(err error) bool {
	return apperror.Code(err) == apperror.CodeWrongKey
}

func IsUpgradeRequired(err error) bool {
	return apperror.Code(err) == apperror.CodeUpgrade
}
