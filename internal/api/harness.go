// Package api provides the REST seam test harness shared by DELTA tests.
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/server"
	"github.com/ferriskleier/delta/internal/service"
	"github.com/ferriskleier/delta/internal/storage"
)

type Harness struct {
	Server     *httptest.Server
	Handler    http.Handler
	Store      *storage.Store
	Token      string
	Key        string
	DBPath     string
	ConfigPath string
}

// NewTestHarness creates a real encrypted database and drives a real HTTP
// handler. It intentionally contains no mocks so later seam tests exercise
// the same boundary as the browser, CLI, and MCP surfaces.
func NewTestHarness(t testing.TB) *Harness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "diary.db")
	configPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv(config.ConfigEnv, configPath)
	key := strings.Repeat("a1", storage.KeyBytes)
	store, err := storage.Open(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	c, err := config.New(path, key)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := config.Save(c); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	handler := server.NewHandler(service.New(store), c.APIToken, server.WithSettingsConfig(c))
	ts := httptest.NewServer(handler)
	h := &Harness{Server: ts, Handler: handler, Store: store, Token: c.APIToken, Key: key, DBPath: path, ConfigPath: configPath}
	t.Cleanup(func() {
		ts.Close()
		_ = store.Close()
	})
	return h
}
