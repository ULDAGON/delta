package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/api"
	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/cli"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/service"
)

func TestPeriodCLIHelpDocumentsFlagsAndIdentifiers(t *testing.T) {
	for _, kind := range []string{"era", "dynasty"} {
		for subcommand, wants := range map[string][]string{
			"":       {"delta " + kind + " list|add|edit|delete"},
			"add":    {"--color", "--start", "--end", "ongoing"},
			"edit":   {kind + "-id-or-exact-name", "--ongoing", "exact " + kind + " name"},
			"delete": {kind + "-id-or-exact-name"},
		} {
			args := []string{kind, "--help"}
			if subcommand != "" {
				args = []string{kind, subcommand, "--help"}
			}
			var stdout, stderr bytes.Buffer
			if err := cli.Run(context.Background(), args, bytes.NewBuffer(nil), &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			for _, want := range wants {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("delta %v help = %q, missing %q", args, stdout.String(), want)
				}
			}
		}
	}
}

func TestPeriodCLICommandsUseRunningHTTPServerAndJSON(t *testing.T) {
	for _, kind := range []string{"era", "dynasty"} {
		t.Run(kind, func(t *testing.T) {
			h := api.NewTestHarness(t)
			t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
			if err := config.Save(config.Config{DatabasePath: h.DBPath, Key: h.Key, APIToken: h.Token, APIAddress: h.Server.URL}); err != nil {
				t.Fatal(err)
			}

			var added service.Period
			addedOutput := runCLI(t, []string{kind, "add", "Berlin", "years", "--color", "#112233", "--start", "2020-01-01", "--end", "2020-12-31", "--json"})
			if err := json.Unmarshal(addedOutput, &added); err != nil {
				t.Fatalf("%s add JSON = %q: %v", kind, addedOutput, err)
			}
			if added.ID == 0 || added.Name != "Berlin years" || added.Color != "#112233" || added.StartDate != "2020-01-01" || added.EndDate == nil || *added.EndDate != "2020-12-31" {
				t.Fatalf("added = %#v", added)
			}
			human := runCLI(t, []string{kind, "add", "--name", "Now", "--color=#abcdef", "--start", "2024-01-01"})
			if !strings.Contains(string(human), kind+" 2 \"Now\" added (2024-01-01 to ongoing)") {
				t.Fatalf("human add = %q", human)
			}

			var listed []service.Period
			if err := json.Unmarshal(runCLI(t, []string{kind, "list", "--json"}), &listed); err != nil {
				t.Fatal(err)
			}
			if len(listed) != 2 || listed[0].Name != "Now" || listed[1].ID != added.ID {
				t.Fatalf("listed = %#v, want newest start first", listed)
			}
			if got, want := string(runCLI(t, []string{kind, "list"})), "2024-01-01 to ongoing  #abcdef  Now (id 2)\n2020-01-01 to 2020-12-31  #112233  Berlin years (id 1)\n"; got != want {
				t.Fatalf("human list = %q, want %q", got, want)
			}

			// The server's overlap message reaches the CLI caller unchanged.
			err := runCLIError(t, []string{kind, "add", "Clash", "--color", "#000000", "--start", "2020-06-01", "--end", "2020-06-30"})
			if apperror.Code(err) != "invalid_"+kind || !strings.Contains(apperror.Message(err), `"Berlin years"`) {
				t.Fatalf("overlapping add = %v", err)
			}

			var edited service.Period
			if err := json.Unmarshal(runCLI(t, []string{kind, "edit", "Berlin years", "--name", "Berlin", "--end", "2021-06-30", "--json"}), &edited); err != nil {
				t.Fatal(err)
			}
			if edited.ID != added.ID || edited.Name != "Berlin" || edited.Color != "#112233" || edited.EndDate == nil || *edited.EndDate != "2021-06-30" {
				t.Fatalf("edited = %#v", edited)
			}

			if got := string(runCLI(t, []string{kind, "delete", "Now", "--json"})); got != "{\"ok\":true,\"id\":2}\n" {
				t.Fatalf("delete JSON = %q", got)
			}
			if err := json.Unmarshal(runCLI(t, []string{kind, "edit", "1", "--ongoing", "--json"}), &edited); err != nil {
				t.Fatal(err)
			}
			if edited.EndDate != nil {
				t.Fatalf("--ongoing left end_date %q", *edited.EndDate)
			}

			if err := runCLIError(t, []string{kind, "delete", "Now"}); apperror.Code(err) != kind+"_not_found" {
				t.Fatalf("deleting a missing %s = %v", kind, err)
			}
			for _, args := range [][]string{
				{kind},
				{kind, "rename"},
				{kind, "add", "No color", "--start", "2020-01-01"},
				{kind, "edit", "Berlin"},
				{kind, "edit", "Berlin", "--end", "2022-01-01", "--ongoing"},
				{kind, "delete"},
			} {
				if err := runCLIError(t, args); err == nil {
					t.Fatalf("delta %v succeeded, want a usage error", args)
				}
			}
		})
	}
}

func runCLIError(t *testing.T, args []string) error {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := cli.Run(context.Background(), args, bytes.NewBuffer(nil), &stdout, &stderr)
	if err == nil {
		t.Fatalf("delta %v succeeded with %q, want an error", args, stdout.String())
	}
	return err
}
