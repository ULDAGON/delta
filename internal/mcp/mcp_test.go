package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/api"
	deltacli "github.com/ferriskleier/delta/internal/cli"
	"github.com/ferriskleier/delta/internal/config"
	deltamcp "github.com/ferriskleier/delta/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPEntryAndHabitLifecycleOverStdio(t *testing.T) {
	h := api.NewTestHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := deltamcp.NewServer(h.Server.URL, h.Token, h.Server.Client())
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "delta-test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = session.Close()
		cancel()
		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
			t.Fatal("MCP server did not stop after session close")
		}
	}()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertExactTools(t, tools)
	assertEntrySetSchemaDocumentsWorkHours(t, tools)

	date := time.Now().Format("2006-01-02")
	created := callTool(t, ctx, session, "entry_set", map[string]any{
		"date":    date,
		"text":    "MCP-created entry",
		"ratings": map[string]any{"total": 4},
	})
	if created.IsError {
		t.Fatalf("entry_set returned tool error: %s", toolText(created))
	}
	read := callTool(t, ctx, session, "entry_get", map[string]any{"date": date})
	if read.IsError || !containsText(read, "MCP-created entry") {
		t.Fatalf("entry_get = %s", toolText(read))
	}
	updated := callTool(t, ctx, session, "entry_set", map[string]any{
		"date": date,
		"text": "MCP-updated entry",
	})
	if updated.IsError {
		t.Fatalf("entry_set update returned tool error: %s", toolText(updated))
	}
	read = callTool(t, ctx, session, "entry_get", map[string]any{"date": date})
	if read.IsError || !containsText(read, "MCP-updated entry") || containsText(read, "MCP-created entry") {
		t.Fatalf("entry_get after update = %s", toolText(read))
	}
	clearedGoals := callTool(t, ctx, session, "entry_set", map[string]any{"date": date, "goals": nil})
	if clearedGoals.IsError {
		t.Fatalf("entry_set goals clear returned tool error: %s", toolText(clearedGoals))
	}

	workHours := callTool(t, ctx, session, "entry_set", map[string]any{"date": date, "work_hours": 7.5})
	if workHours.IsError || !containsText(workHours, `"work_hours":7.5`) {
		t.Fatalf("entry_set work_hours = %s", toolText(workHours))
	}
	read = callTool(t, ctx, session, "entry_get", map[string]any{"date": date})
	if read.IsError || !containsText(read, `"work_hours":7.5`) {
		t.Fatalf("entry_get after work_hours = %s", toolText(read))
	}
	clearedWorkHours := callTool(t, ctx, session, "entry_set", map[string]any{"date": date, "work_hours": nil})
	if clearedWorkHours.IsError || containsText(clearedWorkHours, "work_hours") {
		t.Fatalf("entry_set work_hours clear = %s", toolText(clearedWorkHours))
	}
	invalidWorkHours := callTool(t, ctx, session, "entry_set", map[string]any{"date": date, "work_hours": 25})
	if !invalidWorkHours.IsError || !containsText(invalidWorkHours, `"code":"invalid_entry"`) {
		t.Fatalf("entry_set work_hours above 24 = %s", toolText(invalidWorkHours))
	}

	habit := callTool(t, ctx, session, "habit_add", map[string]any{"name": "MCP habit"})
	if habit.IsError {
		t.Fatalf("habit_add returned tool error: %s", toolText(habit))
	}
	var habitValue struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(toolText(habit)), &habitValue); err != nil || habitValue.ID == 0 {
		t.Fatalf("habit_add payload = %q, err = %v", toolText(habit), err)
	}
	check := callTool(t, ctx, session, "habit_check", map[string]any{"date": date, "habit_id": habitValue.ID})
	if check.IsError || !containsText(check, "MCP-updated entry") {
		t.Fatalf("habit_check = %s", toolText(check))
	}
	if !containsText(check, "\"checkoffs\":[\"1\"]") {
		t.Fatalf("habit_check did not return the checked habit: %s", toolText(check))
	}

	invalid := callTool(t, ctx, session, "entry_get", map[string]any{"date": "2026-02-30"})
	if !invalid.IsError || !containsText(invalid, `"code":"invalid_date"`) {
		t.Fatalf("invalid date result = %s", toolText(invalid))
	}
	invalidHabit := callTool(t, ctx, session, "habit_check", map[string]any{
		"date": date, "habit_id": "not-an-integer",
	})
	if !invalidHabit.IsError || !containsText(invalidHabit, `"code":"invalid_habit"`) {
		t.Fatalf("invalid habit_id result = %s", toolText(invalidHabit))
	}
	invalidStats := callTool(t, ctx, session, "stats", map[string]any{"agg": 42})
	if !invalidStats.IsError || !containsText(invalidStats, `"code":"invalid_stats"`) {
		t.Fatalf("invalid stats agg result = %s", toolText(invalidStats))
	}
	emptySearch := callTool(t, ctx, session, "search", map[string]any{})
	if emptySearch.IsError {
		t.Fatalf("search without q returned tool error: %s", toolText(emptySearch))
	}
}

func TestMCPUnavailableServeIsAStableToolError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := deltamcp.NewServer("http://127.0.0.1:1", "token", &http.Client{Timeout: 200 * time.Millisecond})
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "delta-test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := callTool(t, ctx, session, "entry_get", map[string]any{"date": "2026-08-02"})
	if !result.IsError || !containsText(result, `"code":"server_unavailable"`) || !containsText(result, "delta serve") {
		t.Fatalf("unavailable server result = %s", toolText(result))
	}
	_ = session.Close()
	cancel()
	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("MCP server did not stop after session close")
	}
}

func TestDeltaMCPCommandUsesConfigAndStdio(t *testing.T) {
	h := api.NewTestHarness(t)
	if _, err := config.UpdateAt(h.ConfigPath, func(value *config.Config) error {
		value.APIAddress = h.Server.URL
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientToMCPReader, clientToMCPWriter := io.Pipe()
	mcpToClientReader, mcpToClientWriter := io.Pipe()
	commandDone := make(chan error, 1)
	go func() {
		commandDone <- deltacli.Run(ctx, []string{"mcp"}, clientToMCPReader, mcpToClientWriter, io.Discard)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "delta-test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: mcpToClientReader, Writer: clientToMCPWriter}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("delta mcp tools/list: %v", err)
	}
	assertExactTools(t, tools)

	date := time.Now().Format("2006-01-02")
	created := callTool(t, ctx, session, "entry_set", map[string]any{
		"date": date,
		"text": "real stdio entry",
	})
	if created.IsError {
		t.Fatalf("delta mcp entry_set = %s", toolText(created))
	}
	read := callTool(t, ctx, session, "entry_get", map[string]any{"date": date})
	if read.IsError || !containsText(read, "real stdio entry") {
		t.Fatalf("delta mcp entry_get = %s", toolText(read))
	}
	_ = session.Close()
	cancel()
	select {
	case <-commandDone:
	case <-time.After(2 * time.Second):
		t.Fatal("delta mcp command did not stop after session close")
	}
}

func callTool(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("tools/call %s: %v", name, err)
	}
	return result
}

func assertExactTools(t *testing.T, result *mcp.ListToolsResult) {
	t.Helper()
	want := map[string]struct{}{
		"entry_get": {}, "entry_set": {}, "entry_delete": {}, "entries_range": {},
		"habit_list": {}, "habit_add": {}, "habit_patch": {}, "habit_check": {},
		"habit_uncheck": {}, "habit_archive": {}, "grid": {}, "stats": {},
		"search": {}, "backup": {},
	}
	got := make(map[string]struct{}, len(result.Tools))
	for _, tool := range result.Tools {
		got[tool.Name] = struct{}{}
	}
	if len(got) != len(want) || len(result.Tools) != len(want) {
		t.Fatalf("tools/list returned %d tools (%v), want exactly %d (%v)", len(result.Tools), sortedToolNames(got), len(want), sortedToolNames(want))
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("tools/list omitted %q", name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("tools/list returned unexpected %q", name)
		}
	}
}

func assertEntrySetSchemaDocumentsWorkHours(t *testing.T, result *mcp.ListToolsResult) {
	t.Helper()
	for _, tool := range result.Tools {
		if tool.Name != "entry_set" {
			continue
		}
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"work_hours"`, `"maximum":24`} {
			if !strings.Contains(string(schema), want) {
				t.Fatalf("entry_set schema = %s, missing %s", schema, want)
			}
		}
		return
	}
	t.Fatal("tools/list omitted entry_set")
}

func sortedToolNames(values map[string]struct{}) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func toolText(result *mcp.CallToolResult) string {
	if result == nil {
		return "<nil>"
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	return ""
}

func containsText(result *mcp.CallToolResult, want string) bool {
	return strings.Contains(toolText(result), want)
}
