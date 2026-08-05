// Package mcp implements DELTA's native MCP machine surface.
//
// MCP is deliberately a thin client of delta serve. It reads the same
// per-machine config as the CLI and sends every operation through the
// authenticated REST API; it never opens the diary database itself.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/client"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer builds a DELTA MCP server backed by an already-running REST
// server. The HTTP client is injectable for hermetic seam tests.
func NewServer(baseURL, token string, transportClient *http.Client) *protocol.Server {
	if transportClient == nil {
		transportClient = http.DefaultClient
	}
	client := client.New(baseURL, token, transportClient)
	return newServer(client)
}

// Run serves MCP over newline-delimited JSON on stdin/stdout. Config loading
// is deferred into tool calls when it fails so initialize/tools/list still
// work and a caller receives a structured server_unavailable result that
// points at delta serve.
func Run(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	client := clientFromConfig()
	server := newServer(client)
	transport := &protocol.IOTransport{
		Reader: io.NopCloser(stdin),
		Writer: nopCloser{Writer: stdout},
	}
	return server.Run(ctx, transport)
}

func clientFromConfig() *client.Client {
	c, err := client.FromConfig()
	if err != nil {
		return client.Unavailable()
	}
	return c
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

func newServer(client *client.Client) *protocol.Server {
	server := protocol.NewServer(&protocol.Implementation{Name: "delta", Version: "0.1.0"}, nil)
	add := func(name, description, schema string, handler func(context.Context, map[string]json.RawMessage) (any, error)) {
		server.AddTool(&protocol.Tool{
			Name:        name,
			Description: description,
			InputSchema: json.RawMessage(schema),
		}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			args, err := requestArguments(request)
			if err == nil {
				var value any
				value, err = handler(ctx, args)
				if err == nil {
					return successResult(value), nil
				}
			}
			return errorResult(err), nil
		})
	}

	add("entry_get", "Read one full diary entry by its YYYY-MM-DD date.", entryGetSchema, func(ctx context.Context, args map[string]json.RawMessage) (any, error) {
		date, err := requiredDate(args, "date")
		if err != nil {
			return nil, err
		}
		return clientGet(client, ctx, "/api/entries/"+url.PathEscape(date))
	})
	add("entry_set", "Create or partially update one diary entry by YYYY-MM-DD date.", entrySetSchema, func(ctx context.Context, args map[string]json.RawMessage) (any, error) {
		date, err := requiredDate(args, "date")
		if err != nil {
			return nil, err
		}
		delete(args, "date")
		body, err := json.Marshal(args)
		if err != nil {
			return nil, apperror.Wrap(apperror.CodeInvalidEntry, "invalid entry JSON", err)
		}
		return clientJSON(client, ctx, http.MethodPut, "/api/entries/"+url.PathEscape(date), body)
	})
	add("entry_delete", "Delete one diary entry by its YYYY-MM-DD date.", entryDateSchema, func(ctx context.Context, args map[string]json.RawMessage) (any, error) {
		date, err := requiredDate(args, "date")
		if err != nil {
			return nil, err
		}
		if _, err := clientJSON(client, ctx, http.MethodDelete, "/api/entries/"+url.PathEscape(date), nil); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "date": date}, nil
	})
	add("entries_range", "Read full diary entries in an inclusive YYYY-MM-DD range.", entriesRangeSchema, func(ctx context.Context, args map[string]json.RawMessage) (any, error) {
		query, err := optionalStringQuery(args,
			stringQueryParameter{name: "from", invalidCode: apperror.CodeInvalidDate},
			stringQueryParameter{name: "to", invalidCode: apperror.CodeInvalidDate},
		)
		if err != nil {
			return nil, err
		}
		return clientGet(client, ctx, "/api/entries"+query)
	})

	add("habit_list", "List habits with validity ranges and manual order.", emptySchema, func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		return clientGet(client, ctx, "/api/habits")
	})
	add("habit_add", "Create a name-only habit active from today.", habitAddSchema, func(ctx context.Context, args map[string]json.RawMessage) (any, error) {
		name, err := requiredString(args, "name", apperror.CodeInvalidHabit)
		if err != nil {
			return nil, err
		}
		body, _ := json.Marshal(map[string]string{"name": name})
		return clientJSON(client, ctx, http.MethodPost, "/api/habits", body)
	})
	add("habit_patch", "Patch a habit name, order, validity ranges, or archive state.", habitPatchSchema, func(ctx context.Context, args map[string]json.RawMessage) (any, error) {
		habitID, err := requiredID(args, "habit_id")
		if err != nil {
			return nil, err
		}
		delete(args, "habit_id")
		body, err := json.Marshal(args)
		if err != nil {
			return nil, apperror.Wrap(apperror.CodeInvalidHabit, "invalid habit JSON", err)
		}
		return clientJSON(client, ctx, http.MethodPatch, "/api/habits/"+strconv.FormatInt(habitID, 10), body)
	})
	add("habit_check", "Atomically check off a habit for a YYYY-MM-DD date.", habitCheckSchema, func(ctx context.Context, args map[string]json.RawMessage) (any, error) {
		return clientCheckoff(client, ctx, args, true)
	})
	add("habit_uncheck", "Atomically remove a habit check-off for a YYYY-MM-DD date.", habitCheckSchema, func(ctx context.Context, args map[string]json.RawMessage) (any, error) {
		return clientCheckoff(client, ctx, args, false)
	})
	add("habit_archive", "Archive a habit, closing its active range today.", habitIDSchema, func(ctx context.Context, args map[string]json.RawMessage) (any, error) {
		habitID, err := requiredID(args, "habit_id")
		if err != nil {
			return nil, err
		}
		body := json.RawMessage(`{"archived":true}`)
		return clientJSON(client, ctx, http.MethodPatch, "/api/habits/"+strconv.FormatInt(habitID, 10), body)
	})

	add("grid", "Read the selected calendar year's rating or habit-score grid.", gridSchema, func(ctx context.Context, args map[string]json.RawMessage) (any, error) {
		query, err := optionalIntAndStringQuery(args, "year", "view")
		if err != nil {
			return nil, err
		}
		return clientGet(client, ctx, "/api/grid"+query)
	})
	add("stats", "Read rating and habit-score statistics for an inclusive date range.", statsSchema, func(ctx context.Context, args map[string]json.RawMessage) (any, error) {
		query, err := optionalStringQuery(args,
			stringQueryParameter{name: "from", invalidCode: apperror.CodeInvalidDate},
			stringQueryParameter{name: "to", invalidCode: apperror.CodeInvalidDate},
			stringQueryParameter{name: "agg", invalidCode: apperror.CodeInvalidStats},
		)
		if err != nil {
			return nil, err
		}
		return clientGet(client, ctx, "/api/stats"+query)
	})
	add("search", "Search textual diary fields with DELTA's plain-word search behavior.", searchSchema, func(ctx context.Context, args map[string]json.RawMessage) (any, error) {
		q := ""
		if raw, ok := args["q"]; ok {
			if err := json.Unmarshal(raw, &q); err != nil {
				return nil, apperror.Wrap(apperror.CodeInvalidEntry, "invalid q", err)
			}
		}
		return clientGet(client, ctx, "/api/search?"+url.Values{"q": []string{q}}.Encode())
	})
	add("backup", "Create an encrypted DELTA database snapshot now.", emptySchema, func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		return clientJSON(client, ctx, http.MethodPost, "/api/backup", nil)
	})
	return server
}

func clientCheckoff(client *client.Client, ctx context.Context, args map[string]json.RawMessage, checked bool) (any, error) {
	date, err := requiredDate(args, "date")
	if err != nil {
		return nil, err
	}
	habitID, err := requiredID(args, "habit_id")
	if err != nil {
		return nil, err
	}
	method := http.MethodDelete
	if checked {
		method = http.MethodPost
	}
	route := "/api/entries/" + url.PathEscape(date) + "/checkoffs/" + strconv.FormatInt(habitID, 10)
	return clientJSON(client, ctx, method, route, nil)
}

func clientGet(c *client.Client, ctx context.Context, route string) (any, error) {
	return clientJSON(c, ctx, http.MethodGet, route, nil)
}

func clientJSON(c *client.Client, ctx context.Context, method, route string, body json.RawMessage) (any, error) {
	status, response, err := c.Do(ctx, method, route, body)
	if err != nil {
		return nil, apperror.Wrap(apperror.CodeServerUnavailable, "cannot reach delta serve; start delta serve", err)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, client.ErrorFromResponse(status, response)
	}
	if len(strings.TrimSpace(string(response))) == 0 {
		return map[string]any{}, nil
	}
	var value any
	if err := json.Unmarshal(response, &value); err != nil {
		return nil, apperror.Wrap(apperror.CodeInternalError, "delta serve returned invalid JSON", err)
	}
	return value, nil
}

func requestArguments(request *protocol.CallToolRequest) (map[string]json.RawMessage, error) {
	if request == nil || request.Params == nil || len(request.Params.Arguments) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(request.Params.Arguments, &args); err != nil || args == nil {
		return nil, apperror.New(apperror.CodeInvalidEntry, "MCP tool arguments must be a JSON object")
	}
	return args, nil
}

func requiredString(args map[string]json.RawMessage, name, code string) (string, error) {
	raw, ok := args[name]
	if !ok {
		return "", apperror.New(code, name+" is required")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", apperror.Wrap(code, "invalid "+name, err)
	}
	return value, nil
}

func requiredDate(args map[string]json.RawMessage, name string) (string, error) {
	return requiredString(args, name, apperror.CodeInvalidDate)
}

func requiredID(args map[string]json.RawMessage, name string) (int64, error) {
	raw, ok := args[name]
	if !ok {
		return 0, apperror.New(apperror.CodeInvalidHabit, "habit_id is required")
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil || value <= 0 {
		return 0, apperror.New(apperror.CodeInvalidHabit, "invalid habit_id")
	}
	return value, nil
}

type stringQueryParameter struct {
	name        string
	invalidCode string
}

func optionalStringQuery(args map[string]json.RawMessage, parameters ...stringQueryParameter) (string, error) {
	query := url.Values{}
	for _, parameter := range parameters {
		raw, ok := args[parameter.name]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", apperror.New(parameter.invalidCode, "invalid "+parameter.name)
		}
		query.Set(parameter.name, value)
	}
	return queryString(query), nil
}

func optionalIntAndStringQuery(args map[string]json.RawMessage, intName, stringName string) (string, error) {
	query := url.Values{}
	if raw, ok := args[intName]; ok {
		var value int
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", apperror.New(apperror.CodeInvalidGrid, "year must be a four-digit calendar year")
		}
		query.Set(intName, strconv.Itoa(value))
	}
	if raw, ok := args[stringName]; ok {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", apperror.New(apperror.CodeInvalidGrid, "invalid view")
		}
		query.Set(stringName, value)
	}
	return queryString(query), nil
}

func queryString(query url.Values) string {
	if encoded := query.Encode(); encoded != "" {
		return "?" + encoded
	}
	return ""
}

func successResult(value any) *protocol.CallToolResult {
	encoded, err := json.Marshal(value)
	if err != nil {
		return errorResult(apperror.Wrap(apperror.CodeInternalError, "encode MCP result", err))
	}
	return &protocol.CallToolResult{
		Content:           []protocol.Content{&protocol.TextContent{Text: string(encoded)}},
		StructuredContent: value,
	}
}

func errorResult(err error) *protocol.CallToolResult {
	if err == nil {
		err = errors.New("MCP tool failed")
	}
	value := map[string]any{
		"error": map[string]string{
			"code":    apperror.Code(err),
			"message": apperror.Message(err),
		},
	}
	encoded, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		encoded = []byte(`{"error":{"code":"internal_error","message":"internal server error"}}`)
	}
	return &protocol.CallToolResult{
		Content:           []protocol.Content{&protocol.TextContent{Text: string(encoded)}},
		StructuredContent: value,
		IsError:           true,
	}
}

const emptySchema = `{"type":"object","additionalProperties":false}`

const entryDateSchema = `{
  "type":"object",
  "properties":{"date":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$","description":"Calendar date in YYYY-MM-DD format"}},
  "required":["date"],
  "additionalProperties":false
}`

const entryGetSchema = entryDateSchema

const entrySetSchema = `{
  "type":"object",
  "properties":{
    "date":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$","description":"Calendar date in YYYY-MM-DD format"},
    "text":{"type":["string","null"]},
    "goals":{"type":["array","null"],"minItems":5,"maxItems":5,"items":{"type":"object","properties":{"text":{"type":"string"},"checked":{"type":"boolean"}},"required":["text","checked"],"additionalProperties":false}},
    "gratitudes":{"type":["array","null"],"minItems":3,"maxItems":3,"items":{"type":"string"}},
    "ws":{"type":["object","null"],"properties":{"went_well":{"type":["string","null"]},"could_have_gone_better":{"type":["string","null"]},"goal_for_tomorrow":{"type":["string","null"]}},"additionalProperties":false},
    "ratings":{"type":["object","null"],"properties":{"total":{"type":["integer","null"],"minimum":1,"maximum":5},"body":{"type":["integer","null"],"minimum":1,"maximum":5},"mind":{"type":["integer","null"],"minimum":1,"maximum":5},"spirit":{"type":["integer","null"],"minimum":1,"maximum":5}},"additionalProperties":false},
    "pixel":{"type":"integer","minimum":0,"maximum":2,"description":"Per-entry marker: 0 grey, 1 green, 2 orange"},
    "work_hours":{"type":["number","null"],"minimum":0,"maximum":24,"description":"Hours worked that day, 0 to 24, decimals allowed; null clears it, omitted leaves it unchanged"}
  },
  "required":["date"],
  "additionalProperties":false
}`

const entriesRangeSchema = `{
  "type":"object",
  "properties":{
    "from":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},
    "to":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"}
  },
  "additionalProperties":false
}`

const habitAddSchema = `{
  "type":"object",
  "properties":{"name":{"type":"string","description":"Name-only habit label"}},
  "required":["name"],
  "additionalProperties":false
}`

const habitIDSchema = `{
  "type":"object",
  "properties":{"habit_id":{"type":"integer","minimum":1}},
  "required":["habit_id"],
  "additionalProperties":false
}`

const habitCheckSchema = `{
  "type":"object",
  "properties":{
    "date":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},
    "habit_id":{"type":"integer","minimum":1}
  },
  "required":["date","habit_id"],
  "additionalProperties":false
}`

const habitPatchSchema = `{
  "type":"object",
  "properties":{
    "habit_id":{"type":"integer","minimum":1},
    "name":{"type":"string"},
    "position":{"type":"integer"},
    "ranges":{"type":"array","items":{"type":"object","properties":{"active_from":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},"active_to":{"type":["string","null"],"pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"}},"required":["active_from"],"additionalProperties":false}},
    "archived":{"type":"boolean"}
  },
  "required":["habit_id"],
  "additionalProperties":false
}`

const gridSchema = `{
  "type":"object",
  "properties":{
    "year":{"type":"integer","description":"Four-digit calendar year"},
    "view":{"type":"string","enum":["rating","habit"]}
  },
  "additionalProperties":false
}`

const statsSchema = `{
  "type":"object",
  "properties":{
    "from":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},
    "to":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},
    "agg":{"type":"string"}
  },
  "additionalProperties":false
}`

const searchSchema = `{
  "type":"object",
  "properties":{"q":{"type":"string","description":"Optional plain words; implicit AND and last-word prefix matching"}},
  "additionalProperties":false
}`
