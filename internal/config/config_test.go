package config_test

import (
	"os"
	"path/filepath"
	"strconv"
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

func TestBackupsPathIsOptionalAndRoundTrips(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv(config.ConfigEnv, configPath)
	key := strings.Repeat("12", storage.KeyBytes)
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
	if loaded.BackupsPath != "" {
		t.Fatalf("fresh config backups path = %q, want empty", loaded.BackupsPath)
	}

	for _, value := range []string{filepath.Join(t.TempDir(), "elsewhere"), ""} {
		updated, err := config.UpdateAt(configPath, func(current *config.Config) error {
			current.BackupsPath = value
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if updated.BackupsPath != value {
			t.Fatalf("updated backups path = %q, want %q", updated.BackupsPath, value)
		}
		loaded, err = config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if loaded.BackupsPath != value {
			t.Fatalf("loaded backups path = %q, want %q", loaded.BackupsPath, value)
		}
		if loaded.DatabasePath != c.DatabasePath || loaded.Key != c.Key || loaded.APIToken != c.APIToken {
			t.Fatalf("backups path update changed protected config fields: %#v", loaded)
		}
	}
}

func TestLanIsOptionalAndRoundTripsAsAQuotedBoolean(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv(config.ConfigEnv, configPath)
	key := strings.Repeat("56", storage.KeyBytes)
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
	if loaded.Lan {
		t.Fatal("fresh config LAN = true, want false")
	}

	for _, value := range []bool{true, false} {
		updated, err := config.UpdateAt(configPath, func(current *config.Config) error {
			current.Lan = value
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if updated.Lan != value {
			t.Fatalf("updated LAN = %v, want %v", updated.Lan, value)
		}
		contents, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if want := "lan = " + strconv.Quote(strconv.FormatBool(value)) + "\n"; !strings.Contains(string(contents), want) {
			t.Fatalf("config contents = %q, want a line %q", contents, want)
		}
		loaded, err = config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Lan != value {
			t.Fatalf("loaded LAN = %v, want %v", loaded.Lan, value)
		}
		if loaded.DatabasePath != c.DatabasePath || loaded.Key != c.Key || loaded.APIToken != c.APIToken {
			t.Fatalf("LAN update changed protected config fields: %#v", loaded)
		}
	}

	t.Run("absent", func(t *testing.T) {
		olderConfigPath := filepath.Join(t.TempDir(), "config.toml")
		t.Setenv(config.ConfigEnv, olderConfigPath)
		contents := "database_path = " + strconv.Quote(filepath.Join(t.TempDir(), "diary.db")) + "\n" +
			"key = " + strconv.Quote(key) + "\n" +
			"api_token = \"token\"\n"
		if err := os.WriteFile(olderConfigPath, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		older, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if older.Lan {
			t.Fatal("config without a lan line loaded LAN = true, want false")
		}
	})
}

func TestLoadExpandsHomeInBackupsPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv(config.ConfigEnv, configPath)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	contents := "database_path = " + strconv.Quote(filepath.Join(t.TempDir(), "diary.db")) + "\n" +
		"backups_path = \"~/delta-backups\"\n" +
		"key = " + strconv.Quote(strings.Repeat("34", storage.KeyBytes)) + "\n" +
		"api_token = \"token\"\n"
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "delta-backups"); loaded.BackupsPath != want {
		t.Fatalf("backups path = %q, want %q", loaded.BackupsPath, want)
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
