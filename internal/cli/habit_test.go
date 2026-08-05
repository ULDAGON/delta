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

func TestHabitCLIHelpDocumentsIdentifierAndDateSemantics(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"habit", "check", "--help"}, bytes.NewBuffer(nil), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"habit-id-or-exact-name", "exact habit name", "local today", "--date"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("help = %q, missing %q", stdout.String(), want)
		}
	}
}

func TestHabitCLICommandsUseRunningHTTPServerAndJSON(t *testing.T) {
	h := api.NewTestHarness(t)
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	if err := config.Save(config.Config{DatabasePath: h.DBPath, Key: h.Key, APIToken: h.Token, APIAddress: h.Server.URL}); err != nil {
		t.Fatal(err)
	}
	today := time.Now().Format("2006-01-02")

	addedOutput := runCLI(t, []string{"habit", "add", "Walk", "--json"})
	var added service.Habit
	if err := json.Unmarshal(addedOutput, &added); err != nil {
		t.Fatalf("habit add JSON = %q: %v", addedOutput, err)
	}
	if added.ID == 0 || added.Name != "Walk" || len(added.Ranges) != 1 || added.Ranges[0].ActiveFrom != today {
		t.Fatalf("added habit = %#v", added)
	}

	listedOutput := runCLI(t, []string{"habit", "list", "--json"})
	var listed []service.Habit
	if err := json.Unmarshal(listedOutput, &listed); err != nil {
		t.Fatalf("habit list JSON = %q: %v", listedOutput, err)
	}
	if len(listed) != 1 || listed[0].ID != added.ID {
		t.Fatalf("listed habits = %#v", listed)
	}

	checkedOutput := runCLI(t, []string{"habit", "check", today, "Walk", "--json"})
	var checked map[string]any
	if err := json.Unmarshal(checkedOutput, &checked); err != nil {
		t.Fatalf("habit check JSON = %q: %v", checkedOutput, err)
	}
	if got := checked["checkoffs"].([]any); len(got) != 1 || got[0] != strconv.FormatInt(added.ID, 10) {
		t.Fatalf("checked entry = %#v", checked)
	}
	humanOutput := runCLI(t, []string{"entry", "show", today})
	if !strings.Contains(string(humanOutput), "Check-offs:\n  - Walk") {
		t.Fatalf("human entry show = %q, want resolved habit name", humanOutput)
	}

	uncheckOutput := runCLI(t, []string{"habit", "uncheck", "Walk", today, "--json"})
	var unchecked map[string]any
	if err := json.Unmarshal(uncheckOutput, &unchecked); err != nil {
		t.Fatalf("habit uncheck JSON = %q: %v", uncheckOutput, err)
	}
	if got := unchecked["checkoffs"].([]any); len(got) != 0 {
		t.Fatalf("unchecked entry = %#v", unchecked)
	}

	archivedOutput := runCLI(t, []string{"habit", "archive", "Walk", "--json"})
	var archived service.Habit
	if err := json.Unmarshal(archivedOutput, &archived); err != nil {
		t.Fatalf("habit archive JSON = %q: %v", archivedOutput, err)
	}
	if archived.ID != added.ID || archived.Ranges[0].ActiveTo == nil || *archived.Ranges[0].ActiveTo != today {
		t.Fatalf("archived habit = %#v", archived)
	}
}

func runCLI(t *testing.T, args []string) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := cli.Run(context.Background(), args, bytes.NewBuffer(nil), &stdout, &stderr); err != nil {
		t.Fatalf("delta %v: %v (stderr=%q)", args, err, stderr.String())
	}
	return stdout.Bytes()
}
