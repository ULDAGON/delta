package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/api"
	"github.com/ferriskleier/delta/internal/cli"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/service"
)

func TestStatsCLIJSONIsThinHTTPClient(t *testing.T) {
	h := api.NewTestHarness(t)
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	if err := config.Save(config.Config{DatabasePath: h.DBPath, Key: h.Key, APIToken: h.Token, APIAddress: h.Server.URL}); err != nil {
		t.Fatal(err)
	}
	year := time.Now().In(time.Local).Year()
	var stdout, stderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"stats", "--year", strconv.Itoa(year), "--json"}, bytes.NewBuffer(nil), &stdout, &stderr); err != nil {
		t.Fatalf("stats CLI: %v", err)
	}
	var stats service.StatsResponse
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("stats JSON = %q: %v", stdout.String(), err)
	}
	if stats.Year != year || stats.Aggregation != "month" || len(stats.Rating) != 12 || len(stats.HabitScore) != 12 {
		t.Fatalf("stats response = %#v", stats)
	}
	if len(stats.WorkHours) != 12 {
		t.Fatalf("work hours series = %d months, want 12", len(stats.WorkHours))
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestStatsCLIHumanLineReportsWorkHours(t *testing.T) {
	h := api.NewTestHarness(t)
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	if err := config.Save(config.Config{DatabasePath: h.DBPath, Key: h.Key, APIToken: h.Token, APIAddress: h.Server.URL}); err != nil {
		t.Fatal(err)
	}
	year := time.Now().In(time.Local).Year()
	var empty, emptyStderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"stats", "--year", strconv.Itoa(year)}, bytes.NewBuffer(nil), &empty, &emptyStderr); err != nil {
		t.Fatalf("stats CLI: %v", err)
	}
	if !strings.Contains(empty.String(), "work —") {
		t.Fatalf("stats line = %q, want an absent work average", empty.String())
	}

	var set, setStderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{
		"entry", "set", time.Now().In(time.Local).Format("2006-01-02"), "--work-hours", "7.5",
	}, bytes.NewBuffer(nil), &set, &setStderr); err != nil {
		t.Fatalf("entry set: %v", err)
	}
	var recorded, recordedStderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"stats", "--year", strconv.Itoa(year)}, bytes.NewBuffer(nil), &recorded, &recordedStderr); err != nil {
		t.Fatalf("stats CLI: %v", err)
	}
	if !strings.Contains(recorded.String(), "work 7.5h") {
		t.Fatalf("stats line = %q, want the recorded work average", recorded.String())
	}
}
