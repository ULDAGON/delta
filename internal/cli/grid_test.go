package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/api"
	"github.com/ferriskleier/delta/internal/cli"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/service"
)

func TestGridCLIJSONIsThinHTTPClient(t *testing.T) {
	h := api.NewTestHarness(t)
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	if err := config.Save(config.Config{DatabasePath: h.DBPath, Key: h.Key, APIToken: h.Token, APIAddress: h.Server.URL}); err != nil {
		t.Fatal(err)
	}
	year := time.Now().In(time.Local).Year()
	var stdout, stderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"grid", "--year", strconv.Itoa(year), "--view", "habit", "--json"}, bytes.NewBuffer(nil), &stdout, &stderr); err != nil {
		t.Fatalf("grid CLI: %v", err)
	}
	var grid service.GridResponse
	if err := json.Unmarshal(stdout.Bytes(), &grid); err != nil {
		t.Fatalf("grid JSON = %q: %v", stdout.String(), err)
	}
	if grid.Year != year || grid.View != service.GridViewHabit || len(grid.Days) < 365 {
		t.Fatalf("grid response = %#v", grid)
	}
}
