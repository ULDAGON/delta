package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ferriskleier/delta/internal/storage"
)

// BackupResult identifies the encrypted database snapshot created by a manual
// backup request. Path is included for the Settings surface and CLI callers;
// Filename keeps the retention and naming contract easy to inspect.
type BackupResult struct {
	Filename string `json:"filename"`
	Path     string `json:"path"`
}

type backupManager struct {
	store *storage.Store
	mu    sync.Mutex
	daily map[string]bool
}

func newBackupManager(store *storage.Store) *backupManager {
	return &backupManager{store: store, daily: make(map[string]bool)}
}

// CreateBackup makes the user-requested snapshot. It shares the same
// encrypted VACUUM INTO mechanism as automatic snapshots and never removes an
// existing backup.
func (s *Service) CreateBackup(ctx context.Context) (BackupResult, error) {
	result, err := s.backups.manual(ctx)
	if err != nil {
		s.recordBackupError("manual", err)
		return BackupResult{}, err
	}
	s.recordBackupSuccess(serviceNow())
	return result, nil
}

func (m *backupManager) dailySnapshot(ctx context.Context) (bool, error) {
	date := serviceToday()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.daily[date] {
		return false, nil
	}

	destination := filepath.Join(storage.BackupDirectory(m.store.Path), "delta-"+date+".db")
	if _, err := os.Stat(destination); err == nil {
		m.daily[date] = true
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("check daily backup: %w", err)
	}
	if err := m.store.Snapshot(ctx, destination); err != nil {
		return false, fmt.Errorf("create daily backup: %w", err)
	}
	m.daily[date] = true
	return true, nil
}

func (m *backupManager) manual(ctx context.Context) (BackupResult, error) {
	now := serviceNow().In(time.Local)
	date := now.Format(serviceDateFormat)

	m.mu.Lock()
	defer m.mu.Unlock()

	directory := storage.BackupDirectory(m.store.Path)
	base := filepath.Join(directory, "delta-"+date+".db")
	destination, err := availableManualDestination(base, now)
	if err != nil {
		return BackupResult{}, err
	}
	if err := m.store.Snapshot(ctx, destination); err != nil {
		return BackupResult{}, fmt.Errorf("create manual backup: %w", err)
	}
	return BackupResult{Filename: filepath.Base(destination), Path: destination}, nil
}

func availableManualDestination(base string, now time.Time) (string, error) {
	prefix := strings.TrimSuffix(filepath.Base(base), filepath.Ext(base)) + "-" + now.Format("150405")
	for attempt := 0; ; attempt++ {
		filename := prefix + ".db"
		if attempt > 0 {
			filename = fmt.Sprintf("%s-%d.db", prefix, attempt)
		}
		destination := filepath.Join(filepath.Dir(base), filename)
		if _, err := os.Stat(destination); os.IsNotExist(err) {
			return destination, nil
		} else if err != nil {
			return "", fmt.Errorf("check backup filename: %w", err)
		}
	}
}
