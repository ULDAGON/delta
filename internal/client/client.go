// Package client provides DELTA's shared authenticated REST client.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/config"
)

// Client sends authenticated requests to delta serve.
type Client struct {
	baseURL     string
	token       string
	http        *http.Client
	unavailable bool
}

// New constructs a client. An empty address uses the same default as the CLI
// and config loader so all DELTA machine surfaces behave consistently.
func New(baseURL, token string, transport *http.Client) *Client {
	if transport == nil {
		transport = http.DefaultClient
	}
	return &Client{
		baseURL: NormalizeBaseURL(baseURL),
		token:   token,
		http:    transport,
	}
}

// FromConfig loads the configured delta serve endpoint for CLI commands.
func FromConfig() (*Client, error) {
	c, err := config.Load()
	if err != nil {
		return nil, err
	}
	return New(c.APIAddress, c.APIToken, nil), nil
}

// Unavailable returns a client that defers a config failure until a tool is
// called. MCP uses this so initialize and tools/list remain available.
func Unavailable() *Client {
	return &Client{http: http.DefaultClient, unavailable: true}
}

// NormalizeBaseURL trims a configured trailing slash, adds a scheme when
// needed, and applies DELTA's default API address when empty.
func NormalizeBaseURL(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return config.DefaultAPIAddress
	}
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		value = "http://" + value
	}
	return value
}

// Do performs one authenticated REST request and returns its status and body.
// HTTP status handling is intentionally left to ErrorFromResponse so callers
// can accept endpoint-specific successful statuses such as 204.
func (c *Client) Do(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	if c.unavailable {
		return 0, nil, errors.New("DELTA client configuration is unavailable")
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, nil, err
	}
	return response.StatusCode, responseBody, nil
}

// ErrorFromResponse maps DELTA's REST error envelope to a stable error. The
// fallback status mappings are shared by the CLI and MCP clients.
func ErrorFromResponse(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Code != "" {
		return apperror.New(envelope.Error.Code, envelope.Error.Message)
	}
	if status >= http.StatusInternalServerError {
		return apperror.New(apperror.CodeServerUnavailable, "delta serve returned an unexpected error; check delta serve")
	}
	return apperror.New(apperror.CodeInternalError, fmt.Sprintf("delta serve returned HTTP %d", status))
}
