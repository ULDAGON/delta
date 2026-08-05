// Package config reads and writes DELTA's per-machine configuration.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/ferriskleier/delta/internal/storage"
)

const (
	ConfigEnv         = "DELTA_CONFIG"
	DefaultAPIAddress = "http://127.0.0.1:7331"
	NoConfig          = "no DELTA config — run delta init first"
)

var ErrNotFound = errors.New(NoConfig)

var configWriteMu sync.Mutex

type Config struct {
	DatabasePath string
	Key          string
	APIToken     string
	APIAddress   string
}

func Path() (string, error) {
	if path := strings.TrimSpace(os.Getenv(ConfigEnv)); path != "" {
		return expandHome(path)
	}
	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "delta", "config.toml"), nil
}

func DefaultDatabasePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "delta", "delta.db"), nil
	}
	dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "delta", "delta.db"), nil
}

// ExpandPath expands the small path syntax accepted by CLI flags while
// leaving relative paths relative to the current working directory.
func ExpandPath(path string) (string, error) { return expandHome(path) }

func New(databasePath, key string) (Config, error) {
	key = storage.NormalizeKey(key)
	if err := storage.ValidateKey(key); err != nil {
		return Config{}, err
	}
	token, err := randomHex(32)
	if err != nil {
		return Config{}, fmt.Errorf("generate API token: %w", err)
	}
	databasePath, err = expandHome(databasePath)
	if err != nil {
		return Config{}, err
	}
	databasePath, err = filepath.Abs(databasePath)
	if err != nil {
		return Config{}, fmt.Errorf("resolve database path: %w", err)
	}
	return Config{DatabasePath: databasePath, Key: key, APIToken: token, APIAddress: DefaultAPIAddress}, nil
}

func NewKey() (string, error) { return randomHex(storage.KeyBytes) }

func NewToken() (string, error) { return randomHex(32) }

func randomHex(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CheckAvailable reports whether init can create the per-machine config
// without replacing an existing diary pointer.
func CheckAvailable() error {
	path, err := Path()
	if err != nil {
		return err
	}
	return checkAvailable(path)
}

func checkAvailable(path string) error {
	if _, err := os.Stat(path); err == nil {
		return configExistsError(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check config: %w", err)
	}
	return nil
}

func configExistsError(path string) error {
	return fmt.Errorf("config already points at a diary: %s", path)
}

func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	return LoadAt(path)
}

// LoadAt reads a specific config path without consulting DELTA_CONFIG again.
// Serving instances use this to keep updates attached to their own config
// file even if the process environment changes later.
func LoadAt(path string) (Config, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrNotFound
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	values, err := parseTOML(string(contents))
	if err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	c := Config{DatabasePath: values["database_path"], Key: storage.NormalizeKey(values["key"]), APIToken: values["api_token"], APIAddress: values["api_address"]}
	if c.APIAddress == "" {
		c.APIAddress = DefaultAPIAddress
	}
	if c.DatabasePath == "" || c.Key == "" || c.APIToken == "" {
		return Config{}, fmt.Errorf("config is missing database_path, key, or api_token")
	}
	if err := storage.ValidateKey(c.Key); err != nil {
		return Config{}, err
	}
	if c.DatabasePath, err = expandHome(c.DatabasePath); err != nil {
		return Config{}, err
	}
	return c, nil
}

func Save(c Config) error {
	if err := storage.ValidateKey(c.Key); err != nil {
		return err
	}
	if strings.TrimSpace(c.DatabasePath) == "" || strings.TrimSpace(c.APIToken) == "" {
		return fmt.Errorf("config requires database_path, key, and api_token")
	}
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	return withConfigWriteLock(path, func() error {
		if err := checkAvailable(path); err != nil {
			return err
		}
		if err := atomicWrite(path, format(c)); err != nil {
			return fmt.Errorf("create config: %w", err)
		}
		return nil
	})
}

// UpdateAPIAddress records the actual bound localhost URL. This matters when
// serve uses port 0: later CLI commands discover the ephemeral port through
// the same per-machine config as the bearer token.
func UpdateAPIAddress(_ Config, address string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return fmt.Errorf("API address cannot be empty")
	}
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	path, err := Path()
	if err != nil {
		return err
	}
	return withConfigWriteLock(path, func() error {
		c, err := LoadAt(path)
		if err != nil {
			return fmt.Errorf("load config for address update: %w", err)
		}
		c.APIAddress = address
		if err := atomicWrite(path, format(c)); err != nil {
			return fmt.Errorf("update config: %w", err)
		}
		return nil
	})
}

// UpdateAt loads the config at an explicit path, applies one mutation, and
// replaces the file atomically. It is useful to a serving instance whose
// process-local config path must remain stable while tests or
// another instance adjust the DELTA_CONFIG environment variable.
func UpdateAt(path string, mutate func(*Config) error) (Config, error) {
	if mutate == nil {
		return Config{}, fmt.Errorf("config update requires a mutation")
	}
	if strings.TrimSpace(path) == "" {
		return Config{}, fmt.Errorf("config path cannot be empty")
	}
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	var updated Config
	err := withConfigWriteLock(path, func() error {
		current, err := LoadAt(path)
		if err != nil {
			return err
		}
		if err := mutate(&current); err != nil {
			return err
		}
		current.Key = storage.NormalizeKey(current.Key)
		if err := storage.ValidateKey(current.Key); err != nil {
			return err
		}
		if strings.TrimSpace(current.DatabasePath) == "" || strings.TrimSpace(current.APIToken) == "" {
			return fmt.Errorf("config requires database_path, key, and api_token")
		}
		if err := atomicWrite(path, format(current)); err != nil {
			return fmt.Errorf("update config: %w", err)
		}
		updated = current
		return nil
	})
	if err != nil {
		return Config{}, err
	}
	return updated, nil
}

func withConfigWriteLock(path string, write func() error) error {
	lock, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open config lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock config: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return write()
}

func atomicWrite(path, contents string) (err error) {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set temporary config permissions: %w", err)
	}
	if _, err := temp.WriteString(contents); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set config permissions: %w", err)
	}
	return nil
}

func format(c Config) string {
	return "# DELTA per-machine configuration\n" +
		"database_path = " + strconv.Quote(c.DatabasePath) + "\n" +
		"key = " + strconv.Quote(storage.NormalizeKey(c.Key)) + "\n" +
		"api_token = " + strconv.Quote(c.APIToken) + "\n" +
		"api_address = " + strconv.Quote(c.APIAddress) + "\n"
}

func expandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return filepath.Clean(path), nil
}

func parseTOML(input string) (map[string]string, error) {
	values := make(map[string]string)
	for lineNumber, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("line %d is not a key/value", lineNumber+1)
		}
		key := strings.TrimSpace(parts[0])
		value, err := strconv.Unquote(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber+1, err)
		}
		values[key] = value
	}
	return values, nil
}
