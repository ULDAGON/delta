package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/storage"
)

func TestSaveLoadAndPermissions(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "nested", "config.toml")
	t.Setenv(config.ConfigEnv, configPath)
	key := strings.Repeat("ab", storage.KeyBytes)
	c, err := config.New(filepath.Join(t.TempDir(), "diary.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(c); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded != c {
		t.Fatalf("loaded config %#v, want %#v", loaded, c)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 600", got)
	}
}

func TestMissingConfig(t *testing.T) {
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "missing.toml"))
	if _, err := config.Load(); err != config.ErrNotFound {
		t.Fatalf("Load error = %v, want ErrNotFound", err)
	}
}

func TestSaveRefusesToOverwriteExistingConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv(config.ConfigEnv, configPath)
	key := strings.Repeat("cd", storage.KeyBytes)
	first, err := config.New(filepath.Join(t.TempDir(), "first.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(first); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := config.New(filepath.Join(t.TempDir(), "second.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	err = config.Save(second)
	if err == nil || !strings.Contains(err.Error(), "config already points at a diary") {
		t.Fatalf("second Save error = %v", err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("config changed: before=%q after=%q", before, after)
	}
}

func TestUpdateAPIAddressReloadsFreshConfigAndLeavesACompleteFile(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.toml")
	t.Setenv(config.ConfigEnv, configPath)
	key := strings.Repeat("ef", storage.KeyBytes)
	c, err := config.New(filepath.Join(t.TempDir(), "original.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(c); err != nil {
		t.Fatal(err)
	}
	bootConfig, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	changedDatabasePath := filepath.Join(t.TempDir(), "changed-on-disk.db")
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	contentsString := strings.Replace(string(contents), c.DatabasePath, changedDatabasePath, 1)
	if contentsString == string(contents) {
		t.Fatal("test did not change the on-disk database path")
	}
	if err := os.WriteFile(configPath, []byte(contentsString), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, address := range []string{"http://127.0.0.1:8001", "http://127.0.0.1:8002"} {
		if err := config.UpdateAPIAddress(bootConfig, address); err != nil {
			t.Fatal(err)
		}
		loaded, err := config.Load()
		if err != nil {
			t.Fatalf("Load after address update: %v", err)
		}
		if loaded.DatabasePath != changedDatabasePath {
			t.Fatalf("database path = %q, want fresh on-disk value %q", loaded.DatabasePath, changedDatabasePath)
		}
		if loaded.Key != c.Key || loaded.APIToken != c.APIToken {
			t.Fatalf("address update changed protected config fields: %#v", loaded)
		}
		if loaded.APIAddress != address {
			t.Fatalf("API address = %q, want %q", loaded.APIAddress, address)
		}
	}

	if _, err := os.Stat(configPath); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 600", got)
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(configDir, ".config.toml.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary config files remain: %v", temporaryFiles)
	}
}
