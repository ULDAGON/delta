package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/api"
	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/cli"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/storage"
)

func TestInitPrintsOneKeyAndWritesConfig(t *testing.T) {
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	path := filepath.Join(t.TempDir(), "new", "diary.db")
	var stdout, stderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"init", "--path", path}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^[0-9a-f]{64}\n$`).MatchString(stdout.String()) {
		t.Fatalf("stdout = %q, want one 64-hex key line", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabasePath != path || c.Key != strings.TrimSpace(stdout.String()) {
		t.Fatalf("config = %#v", c)
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(configPath(t)), "config.toml"))
	if err == nil && info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}
}

func TestBareInitRequiresAnExplicitDoor(t *testing.T) {
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	err := cli.Run(context.Background(), []string{"init"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || err.Error() != "usage: delta init --path <p> or delta init --open <p> --key-stdin" {
		t.Fatalf("bare init error = %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(configPath(t)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config stat error = %v, want config to remain absent", err)
	}
	databasePath, err := config.DefaultDatabasePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(databasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default database stat error = %v, want diary to remain absent", err)
	}
}

func TestInitRefusesAnExistingConfigForBothDoors(t *testing.T) {
	t.Run("path", func(t *testing.T) {
		t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
		firstPath := filepath.Join(t.TempDir(), "first.db")
		secondPath := filepath.Join(t.TempDir(), "second.db")
		var stdout, stderr bytes.Buffer
		if err := cli.Run(context.Background(), []string{"init", "--path", firstPath}, strings.NewReader(""), &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(configPath(t))
		if err != nil {
			t.Fatal(err)
		}
		err = cli.Run(context.Background(), []string{"init", "--path", secondPath}, strings.NewReader(""), &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "config already points at a diary") {
			t.Fatalf("re-init error = %v", err)
		}
		after, err := os.ReadFile(configPath(t))
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("config changed during re-init: before=%q after=%q", before, after)
		}
		if _, err := os.Stat(secondPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("second diary stat error = %v, want diary creation to be refused", err)
		}
	})

	t.Run("open", func(t *testing.T) {
		t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
		firstPath := filepath.Join(t.TempDir(), "first.db")
		if err := cli.Run(context.Background(), []string{"init", "--path", firstPath}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		adoptPath := filepath.Join(t.TempDir(), "adopt.db")
		key := strings.Repeat("bc", storage.KeyBytes)
		store, err := storage.Open(context.Background(), adoptPath, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := storage.Migrate(context.Background(), store.DB); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(configPath(t))
		if err != nil {
			t.Fatal(err)
		}
		err = cli.Run(context.Background(), []string{"init", "--open", adoptPath, "--key-stdin"}, strings.NewReader(key), &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "config already points at a diary") {
			t.Fatalf("re-adopt error = %v", err)
		}
		after, err := os.ReadFile(configPath(t))
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("config changed during re-adopt: before=%q after=%q", before, after)
		}
	})
}

func TestOpenKeyStdinToleratesWhitespaceAndRejectsWrongKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diary.db")
	key := strings.Repeat("cd", storage.KeyBytes)
	store, err := storage.Open(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	t.Run("whitespace", func(t *testing.T) {
		t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
		var stdout, stderr bytes.Buffer
		pasted := strings.Join([]string{key[:16], key[16:32], key[32:48], key[48:]}, " \n")
		if err := cli.Run(context.Background(), []string{"init", "--open", path, "--key-stdin"}, strings.NewReader(pasted), &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		if stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	})

	t.Run("wrong", func(t *testing.T) {
		t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
		var stdout, stderr bytes.Buffer
		err := cli.Run(context.Background(), []string{"init", "--open", path, "--key-stdin"}, strings.NewReader(strings.Repeat("e", 64)), &stdout, &stderr)
		if apperror.Code(err) != apperror.CodeWrongKey || apperror.Message(err) != apperror.WrongKeyMessage {
			t.Fatalf("error = %v (code %q, message %q)", err, apperror.Code(err), apperror.Message(err))
		}
	})
}

func TestServeNoConfigAndNonLocalListenAddress(t *testing.T) {
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "missing.toml"))
	withoutBrowser(t)
	var stdout, stderr bytes.Buffer
	err := cli.Run(context.Background(), []string{"serve", "--listen", "0.0.0.0:0"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "only accepts localhost") {
		t.Fatalf("non-local listen error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines := make(chan string, 1)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- cli.Run(ctx, []string{"serve", "--listen", "127.0.0.1:0"}, strings.NewReader(""), lineWriter{lines}, ioDiscard{})
	}()
	var line string
	select {
	case line = <-lines:
	case <-time.After(5 * time.Second):
		t.Fatal("setup server did not start")
	}
	if !strings.Contains(line, "no config") || !strings.Contains(line, configPath(t)) || !strings.Contains(line, "setup:") {
		t.Fatalf("setup banner = %q", line)
	}
	fields := strings.Fields(line)
	endpoint := ""
	for _, field := range fields {
		if strings.HasPrefix(field, "http://127.0.0.1:") {
			endpoint = field
			break
		}
	}
	if endpoint == "" {
		t.Fatalf("setup banner has no URL: %q", line)
	}
	response, err := http.Get(endpoint + "/api/setup")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("setup status = %d, want 200", response.StatusCode)
	}
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("setup server did not stop")
	}
}

func TestServeSetupCreateTransitionsToAuthenticatedServing(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	databasePath := filepath.Join(t.TempDir(), "new", "diary.db")
	t.Setenv(config.ConfigEnv, configPath)
	withoutBrowser(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines := make(chan string, 1)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- cli.Run(ctx, []string{"serve", "--listen", "127.0.0.1:0"}, strings.NewReader(""), lineWriter{lines}, ioDiscard{})
	}()
	line := <-lines
	var endpoint string
	for _, field := range strings.Fields(line) {
		if strings.HasPrefix(field, "http://127.0.0.1:") {
			endpoint = field
			break
		}
	}
	if endpoint == "" {
		t.Fatalf("setup banner = %q", line)
	}

	keyBody, err := json.Marshal(map[string]string{"path": databasePath})
	if err != nil {
		t.Fatal(err)
	}
	keyResponse, err := http.Post(endpoint+"/api/setup/key", "application/json", bytes.NewReader(keyBody))
	if err != nil {
		t.Fatal(err)
	}
	var keyPayload struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(keyResponse.Body).Decode(&keyPayload); err != nil {
		keyResponse.Body.Close()
		t.Fatal(err)
	}
	keyResponse.Body.Close()
	if keyResponse.StatusCode != http.StatusOK || len(keyPayload.Key) != 64 {
		t.Fatalf("setup key response = status %d key %q", keyResponse.StatusCode, keyPayload.Key)
	}

	createBody, err := json.Marshal(map[string]any{"path": databasePath, "key": keyPayload.Key, "confirmed": true})
	if err != nil {
		t.Fatal(err)
	}
	createResponse, err := http.Post(endpoint+"/api/setup/create", "application/json", bytes.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	var done struct {
		Door     string `json:"door"`
		APIToken string `json:"api_token"`
	}
	if err := json.NewDecoder(createResponse.Body).Decode(&done); err != nil {
		createResponse.Body.Close()
		t.Fatal(err)
	}
	createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusOK || done.Door != "create" || done.APIToken == "" {
		t.Fatalf("setup create response = status %d done %#v", createResponse.StatusCode, done)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DatabasePath != databasePath || loaded.Key != keyPayload.Key || loaded.APIAddress != endpoint {
		t.Fatalf("saved setup config = %#v", loaded)
	}
	healthRequest, err := http.NewRequest(http.MethodGet, endpoint+"/api/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	healthRequest.Header.Set("Authorization", "Bearer "+done.APIToken)
	healthResponse, err := http.DefaultClient.Do(healthRequest)
	if err != nil {
		t.Fatal(err)
	}
	healthResponse.Body.Close()
	if healthResponse.StatusCode != http.StatusOK {
		t.Fatalf("post-setup health status = %d", healthResponse.StatusCode)
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("setup serve did not stop")
	}
}

func TestServeMigratesAtStartupAndRefusesNewerSchema(t *testing.T) {
	t.Run("migrates before serving", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.toml")
		t.Setenv(config.ConfigEnv, configPath)
		path := filepath.Join(t.TempDir(), "diary.db")
		key := strings.Repeat("f0", storage.KeyBytes)
		store, err := storage.Open(context.Background(), path, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		c, err := config.New(path, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := config.Save(c); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		lines := make(chan string, 1)
		serveDone := make(chan error, 1)
		go func() {
			serveDone <- cli.Run(ctx, []string{"serve", "--listen", "127.0.0.1:0"}, strings.NewReader(""), lineWriter{lines}, ioDiscard{})
		}()
		var line string
		select {
		case line = <-lines:
		case <-time.After(5 * time.Second):
			t.Fatal("serve did not start")
		}
		if !strings.HasPrefix(line, "delta "+cli.Version+" serving "+c.DatabasePath+" on http://") {
			t.Fatalf("serve banner = %q", line)
		}
		fields := strings.Fields(strings.TrimSpace(line))
		endpoint := fields[len(fields)-1]
		req, err := http.NewRequest(http.MethodGet, endpoint+"/api/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+c.APIToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("health status = %d", resp.StatusCode)
		}
		cancel()
		select {
		case err := <-serveDone:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("serve did not stop")
		}
		matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups", "pre-migrate-v0-*.db"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 {
			t.Fatalf("startup snapshots = %v, want one", matches)
		}
	})

	t.Run("refuses newer schema", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.toml")
		t.Setenv(config.ConfigEnv, configPath)
		path := filepath.Join(t.TempDir(), "diary.db")
		key := strings.Repeat("f1", storage.KeyBytes)
		store, err := storage.Open(context.Background(), path, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB.Exec(`PRAGMA user_version = 999`); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		c, err := config.New(path, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := config.Save(c); err != nil {
			t.Fatal(err)
		}
		err = cli.Run(context.Background(), []string{"serve", "--listen", "127.0.0.1:0"}, strings.NewReader(""), &bytes.Buffer{}, ioDiscard{})
		if apperror.Code(err) != apperror.CodeUpgrade || !strings.Contains(apperror.Message(err), "upgrade delta") {
			t.Fatalf("serve newer-schema error = %v", err)
		}
	})
}

func TestEntryCLISmokeUsesRunningBinaryAndHTTP(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	databasePath := filepath.Join(t.TempDir(), "diary.db")
	t.Setenv(config.ConfigEnv, configPath)
	binaryPath := filepath.Join(t.TempDir(), "delta")
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/delta")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build delta: %v\n%s", err, output)
	}

	env := append(os.Environ(), config.ConfigEnv+"="+configPath)
	init := exec.Command(binaryPath, "init", "--path", databasePath)
	init.Env = env
	if output, err := init.CombinedOutput(); err != nil {
		t.Fatalf("delta init: %v\n%s", err, output)
	}

	serveCtx, stopServe := context.WithCancel(context.Background())
	defer stopServe()
	serve := exec.CommandContext(serveCtx, binaryPath, "serve", "--listen", "127.0.0.1:0")
	serve.Env = env
	serveStdout, err := serve.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var serveStderr bytes.Buffer
	serve.Stderr = &serveStderr
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(serveStdout)
	if !scanner.Scan() {
		t.Fatalf("serve did not print a banner: %s", serveStderr.String())
	}
	line := scanner.Text()
	fields := strings.Fields(line)
	if len(fields) == 0 {
		t.Fatalf("empty serve banner")
	}
	apiAddress := fields[len(fields)-1]
	if loaded, err := config.Load(); err != nil || loaded.APIAddress != apiAddress {
		t.Fatalf("serve did not persist API address: loaded=%#v err=%v", loaded, err)
	}
	entryEnv := env

	set := exec.Command(binaryPath, "entry", "set", "2026-08-02", "--text", "-", "--json")
	set.Env = entryEnv
	set.Stdin = strings.NewReader("piped from stdin")
	setOutput, err := set.CombinedOutput()
	if err != nil {
		t.Fatalf("delta entry set: %v\n%s", err, setOutput)
	}
	var setEntry map[string]any
	if err := json.Unmarshal(setOutput, &setEntry); err != nil {
		t.Fatalf("set JSON = %q: %v", setOutput, err)
	}
	if setEntry["date"] != "2026-08-02" || setEntry["text"] != "piped from stdin" {
		t.Fatalf("set entry = %#v", setEntry)
	}

	show := exec.Command(binaryPath, "entry", "show", "2026-08-02", "--json")
	show.Env = entryEnv
	showOutput, err := show.CombinedOutput()
	if err != nil {
		t.Fatalf("delta entry show: %v\n%s", err, showOutput)
	}
	var shown map[string]any
	if err := json.Unmarshal(showOutput, &shown); err != nil {
		t.Fatalf("show JSON = %q: %v", showOutput, err)
	}
	if shown["date"] != setEntry["date"] || shown["text"] != setEntry["text"] {
		t.Fatalf("show entry = %#v, set entry = %#v", shown, setEntry)
	}
	stopServe()
	_ = serve.Wait()
}

func TestEntrySetFieldFlagsUseHTTPAndPreserveFields(t *testing.T) {
	h := api.NewTestHarness(t)
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	t.Setenv("DELTA_API_ADDRESS", "http://127.0.0.1:1")
	if err := config.Save(config.Config{DatabasePath: h.DBPath, Key: h.Key, APIToken: h.Token, APIAddress: h.Server.URL}); err != nil {
		t.Fatal(err)
	}
	var output, stderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{
		"entry", "set", "2026-08-02", "--text", "field flags",
		"--goal-1", "ship it", "--goal-1-checked", "--goal-2", "keep learning",
		"--gratitude-1", "a good friend", "--went-well", "shipped",
		"--could-have-gone-better", "started late", "--goal-for-tomorrow", "walk",
		"--total", "4", "--body", "2", "--work-hours", "7.5", "--json",
	}, strings.NewReader(""), &output, &stderr); err != nil {
		t.Fatal(err)
	}
	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["text"] != "field flags" || entry["ratings"].(map[string]any)["total"] != float64(4) {
		t.Fatalf("entry = %#v", entry)
	}
	if entry["work_hours"] != float64(7.5) {
		t.Fatalf("work hours = %#v, want 7.5", entry["work_hours"])
	}
	goals := entry["goals"].([]any)
	if goals[0].(map[string]any)["text"] != "ship it" || goals[0].(map[string]any)["checked"] != true {
		t.Fatalf("goal 1 = %#v", goals[0])
	}

	var human, humanStderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"entry", "show", "2026-08-02"}, strings.NewReader(""), &human, &humanStderr); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Date: 2026-08-02",
		"Text: field flags",
		"1. [x] ship it",
		"2. [ ] keep learning",
		"Gratitudes:",
		"1. a good friend",
		"3 Ws:",
		"Went well: shipped",
		"Could have gone better: started late",
		"Goal for tomorrow: walk",
		"Total: 4",
		"Body: 2",
		"Mind: absent",
		"Spirit: absent",
		"Work hours: 7.5",
		"Check-offs:\n  absent",
	} {
		if !strings.Contains(human.String(), want) {
			t.Fatalf("human show = %q, missing %q", human.String(), want)
		}
	}
}

func TestEntryWorkHoursCLIShowsAbsentAndRejectsOutOfRange(t *testing.T) {
	h := api.NewTestHarness(t)
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	if err := config.Save(config.Config{DatabasePath: h.DBPath, Key: h.Key, APIToken: h.Token, APIAddress: h.Server.URL}); err != nil {
		t.Fatal(err)
	}
	var output, stderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"entry", "set", "2026-08-02", "--text", "no hours"}, strings.NewReader(""), &output, &stderr); err != nil {
		t.Fatal(err)
	}
	var human, humanStderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"entry", "show", "2026-08-02"}, strings.NewReader(""), &human, &humanStderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "Work hours: absent") {
		t.Fatalf("human show = %q, want absent work hours", human.String())
	}

	for _, value := range []string{"-1", "24.5"} {
		err := cli.Run(context.Background(), []string{"entry", "set", "2026-08-02", "--work-hours", value}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "between 0 and 24") {
			t.Fatalf("work hours %q error = %v, want a range error", value, err)
		}
	}
	var zero, zeroStderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"entry", "set", "2026-08-02", "--work-hours", "0", "--json"}, strings.NewReader(""), &zero, &zeroStderr); err != nil {
		t.Fatal(err)
	}
	var entry map[string]any
	if err := json.Unmarshal(zero.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["work_hours"] != float64(0) {
		t.Fatalf("work hours = %#v, want a recorded 0", entry["work_hours"])
	}
	var zeroHuman, zeroHumanStderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"entry", "show", "2026-08-02"}, strings.NewReader(""), &zeroHuman, &zeroHumanStderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(zeroHuman.String(), "Work hours: 0") {
		t.Fatalf("human show = %q, want a recorded 0 rather than absent", zeroHuman.String())
	}
}

func TestEntryCLIRejectsUndocumentedFlagAliases(t *testing.T) {
	for _, alias := range []string{"freeform", "goal1", "goal1-text", "goal1-checked", "gratitude1", "rating-total", "rating-body", "rating-mind", "rating-spirit"} {
		t.Run(alias, func(t *testing.T) {
			err := cli.Run(context.Background(), []string{"entry", "set", "2026-08-02", "--" + alias, "value"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
				t.Fatalf("alias %q error = %v, want an unknown flag error", alias, err)
			}
		})
	}
}

type lineWriter struct{ lines chan<- string }

func (w lineWriter) Write(p []byte) (int, error) {
	w.lines <- string(p)
	return len(p), nil
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func withoutBrowser(t *testing.T) {
	t.Helper()
	previous := cli.OpenBrowser
	cli.OpenBrowser = func(string) {}
	t.Cleanup(func() { cli.OpenBrowser = previous })
}

func configPath(t *testing.T) string {
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	return path
}
