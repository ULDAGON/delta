package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/api"
)

func TestAPIRequiresBearerToken(t *testing.T) {
	h := api.NewTestHarness(t)
	tests := []struct {
		name  string
		token string
		want  int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong", token: strings.Repeat("f", 64), want: http.StatusUnauthorized},
		{name: "correct", token: h.Token, want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, h.Server.URL+"/api/health", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			resp, err := h.Server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			if tt.want == http.StatusUnauthorized {
				var envelope struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Error.Code != "unauthorized" || envelope.Error.Message != "unauthorized" {
					t.Fatalf("error envelope = %#v", envelope.Error)
				}
			}
		})
	}
}
