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

// statsCLIFixture sets entries (date -> character count) through the CLI and
// returns a runner that prints human stats for one year.
func statsCLIFixture(t *testing.T, characters map[string]int) func(year int) string {
	t.Helper()
	h := api.NewTestHarness(t)
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	if err := config.Save(config.Config{DatabasePath: h.DBPath, Key: h.Key, APIToken: h.Token, APIAddress: h.Server.URL}); err != nil {
		t.Fatal(err)
	}
	for date, count := range characters {
		var stdout, stderr bytes.Buffer
		if err := cli.Run(context.Background(), []string{"entry", "set", date, "--text", strings.Repeat("x", count)}, bytes.NewBuffer(nil), &stdout, &stderr); err != nil {
			t.Fatalf("entry set %s: %v", date, err)
		}
	}
	return func(year int) string {
		var stdout, stderr bytes.Buffer
		if err := cli.Run(context.Background(), []string{"stats", "--year", strconv.Itoa(year)}, bytes.NewBuffer(nil), &stdout, &stderr); err != nil {
			t.Fatalf("stats CLI: %v", err)
		}
		return stdout.String()
	}
}

func TestStatsCLIHumanOutputShowsChangesAndRecordCaption(t *testing.T) {
	year := time.Now().In(time.Local).Year()
	stats := statsCLIFixture(t, map[string]int{
		strconv.Itoa(year-1) + "-01-01": 2000,
		strconv.Itoa(year) + "-01-01":   1000,
	})
	behind := stats(year)
	if !strings.Contains(behind, "1000 chars (-50.0%)") {
		t.Fatalf("stats output = %q, want a signed character change", behind)
	}
	lines := strings.Split(strings.TrimSpace(behind), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[1], "/day to break record ("+strconv.Itoa(year-1)+")") {
		t.Fatalf("stats output = %q, want a behind caption on its own line", behind)
	}
	if strings.Contains(behind, "\x1b") {
		t.Fatalf("stats output = %q, want no colour codes", behind)
	}
}

func TestStatsCLIHumanOutputAheadCaption(t *testing.T) {
	year := time.Now().In(time.Local).Year()
	stats := statsCLIFixture(t, map[string]int{
		strconv.Itoa(year-1) + "-01-01": 2000,
		strconv.Itoa(year) + "-01-01":   3500,
	})
	ahead := stats(year)
	if !strings.Contains(ahead, "3500 chars (+75.0%)") || !strings.Contains(ahead, "+1,500 over record ("+strconv.Itoa(year-1)+")\n") {
		t.Fatalf("stats output = %q, want +75.0%% and an ahead caption", ahead)
	}
}

func TestStatsCLIHumanOutputPastAndRecordCaptions(t *testing.T) {
	year := time.Now().In(time.Local).Year()
	stats := statsCLIFixture(t, map[string]int{
		strconv.Itoa(year-3) + "-01-01": 1500,
		strconv.Itoa(year-2) + "-01-01": 200,
		strconv.Itoa(year-1) + "-01-01": 4000,
	})
	if past := stats(year - 2); !strings.Contains(past, "-1,300 vs "+strconv.Itoa(year-3)+"\n") {
		t.Fatalf("stats output = %q, want a negative past caption", past)
	}
	if record := stats(year - 1); !strings.HasSuffix(record, "record year\n") {
		t.Fatalf("stats output = %q, want the record caption", record)
	}
	if first := stats(year - 3); strings.Contains(first, "vs") || strings.Contains(first, "record") {
		t.Fatalf("stats output = %q, want no caption for the first year", first)
	}
}
