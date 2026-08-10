// Package service is the shared application seam used by HTTP, CLI, and MCP.
package service

import (
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ferriskleier/delta/internal/storage"
)

type Service struct {
	Store *storage.Store

	backupsMu       sync.RWMutex
	backups         *backupManager
	lastBackupError error
	lastBackup      time.Time
}

const serviceDateFormat = "2006-01-02"

// Option adjusts one Service at construction time.
type Option func(*serviceOptions)

type serviceOptions struct {
	backupsPath string
}

// WithBackupsPath sends every snapshot to a configured directory. An empty
// path keeps snapshots beside the database.
func WithBackupsPath(path string) Option {
	return func(options *serviceOptions) { options.backupsPath = path }
}

func New(store *storage.Store, options ...Option) *Service {
	var applied serviceOptions
	for _, option := range options {
		if option != nil {
			option(&applied)
		}
	}
	directory := applied.backupsPath
	if store != nil {
		directory = storage.ResolveBackupDirectory(store.Path, applied.backupsPath)
	}
	return &Service{Store: store, backups: newBackupManager(store, directory), lastBackup: latestBackupTime(directory)}
}

// SetBackupsPath repoints automatic and manual snapshots at a configured
// directory while the process keeps running; an empty path returns to the
// directory derived beside the database. The reported last-backup time is
// re-seeded from the new directory, which is what the next start would read.
func (s *Service) SetBackupsPath(path string) {
	directory := path
	if s.Store != nil {
		directory = storage.ResolveBackupDirectory(s.Store.Path, path)
	}
	s.backups.setDirectory(directory)
	s.backupsMu.Lock()
	s.lastBackup = latestBackupTime(directory)
	s.backupsMu.Unlock()
}

func latestBackupTime(directory string) time.Time {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return time.Time{}
	}
	// Snapshot creation records the authoritative service timestamp below. On
	// restart there is no metadata sidecar by design, so the retained file's
	// modification time is the best available prior-snapshot timestamp.
	var latest time.Time
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "delta-") || filepath.Ext(entry.Name()) != ".db" {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}

// LastBackupError returns the most recent automatic or manual backup error.
// It is retained for the Settings surface; a failed automatic backup does not
// make the write that triggered it fail.
func (s *Service) LastBackupError() error {
	s.backupsMu.RLock()
	defer s.backupsMu.RUnlock()
	return s.lastBackupError
}

// LastBackup returns the time of the most recent snapshot created by this
// serving instance. A nil result means no snapshot has succeeded yet.
func (s *Service) LastBackup() *time.Time {
	s.backupsMu.RLock()
	defer s.backupsMu.RUnlock()
	if s.lastBackup.IsZero() {
		return nil
	}
	value := s.lastBackup
	return &value
}

func (s *Service) beforeWrite(ctx context.Context) {
	created, err := s.backups.dailySnapshot(ctx)
	if err != nil {
		s.recordBackupError("automatic", err)
	} else if created {
		s.recordBackupSuccess(serviceNow())
	}
}

func (s *Service) recordBackupError(kind string, err error) {
	s.backupsMu.Lock()
	s.lastBackupError = err
	s.backupsMu.Unlock()
	log.Printf("delta: %s backup failed: %v", kind, err)
}

func (s *Service) recordBackupSuccess(at time.Time) {
	s.backupsMu.Lock()
	s.lastBackup = at.In(time.Local)
	s.lastBackupError = nil
	s.backupsMu.Unlock()
}

var serviceNow = time.Now

func serviceToday() string {
	return serviceNow().In(time.Local).Format(serviceDateFormat)
}

// LocalToday is the shared local-date seam used by server defaults and all
// date-derived service behavior. Tests can replace habitToday to pin time.
func LocalToday() string { return habitToday() }

// LocalCurrentYear derives from the same injectable date as LocalToday.
func LocalCurrentYear() int {
	return yearFromDate(LocalToday())
}

func localToday() string { return time.Now().In(time.Local).Format("2006-01-02") }

var habitToday = localToday

// HabitDayScore is the derived score and its contributing counts for one day.
// VisibleCheckoffs is the unique checked/active intersection used by entry
// reads as well as score calculations.
type HabitDayScore struct {
	Active           int
	Checked          int
	Percent          *float64
	VisibleCheckoffs []string
}

// CalculateDailyHabitScore derives checked ÷ active × 100 and the checked /
// active intersection from one source of truth. A day with no active habits
// has no score.
func CalculateDailyHabitScore(checkoffs []string, active map[string]struct{}) HabitDayScore {
	result := HabitDayScore{
		Active:           len(active),
		VisibleCheckoffs: make([]string, 0, len(checkoffs)),
	}
	seen := make(map[string]struct{}, len(checkoffs))
	for _, checkoff := range checkoffs {
		if _, ok := active[checkoff]; !ok {
			continue
		}
		if _, ok := seen[checkoff]; ok {
			continue
		}
		seen[checkoff] = struct{}{}
		result.VisibleCheckoffs = append(result.VisibleCheckoffs, checkoff)
	}
	result.Checked = len(result.VisibleCheckoffs)
	if result.Active > 0 {
		percent := float64(result.Checked) / float64(result.Active) * 100
		result.Percent = &percent
	}
	return result
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}
