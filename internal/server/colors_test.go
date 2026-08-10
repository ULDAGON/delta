package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/api"
)

const colorsPath = "/api/settings/colors"

func TestColorsAreEmptyUntilStoredAndRoundTrip(t *testing.T) {
	h := api.NewTestHarness(t)

	var unset map[string]json.RawMessage
	decodeJSON(t, settingsRequest(t, h, http.MethodGet, colorsPath, nil, h.Token), &unset)
	if len(unset) != 0 {
		t.Fatalf("unset colors = %#v, want {}", unset)
	}

	payload := colorsPayload()
	var saved map[string]json.RawMessage
	decodeJSON(t, settingsRequest(t, h, http.MethodPut, colorsPath, []byte(payload), h.Token), &saved)
	if string(saved["ratings"]) != colorsRatings || string(saved["habits"]) != colorsHabits() {
		t.Fatalf("PUT colors = %#v, want the stored palette", saved)
	}
	var read map[string]json.RawMessage
	decodeJSON(t, settingsRequest(t, h, http.MethodGet, colorsPath, nil, h.Token), &read)
	if string(read["ratings"]) != colorsRatings || string(read["habits"]) != colorsHabits() {
		t.Fatalf("GET colors = %#v, want the stored palette", read)
	}
}

func TestColorsRejectInvalidPalettes(t *testing.T) {
	h := api.NewTestHarness(t)
	tests := []struct {
		name    string
		payload string
	}{
		{name: "empty body", payload: ""},
		{name: "not an object", payload: "[]"},
		{name: "unknown field", payload: `{"ratings":` + colorsRatings + `,"habits":` + colorsHabits() + `,"pixels":[]}`},
		{name: "missing ratings", payload: `{"habits":` + colorsHabits() + `}`},
		{name: "too few habits", payload: `{"ratings":` + colorsRatings + `,"habits":["#111111"]}`},
		{name: "bad color", payload: strings.Replace(colorsPayload(), `"#111111"`, `"#11111"`, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := settingsRequest(t, h, http.MethodPut, colorsPath, []byte(tt.payload), h.Token)
			assertErrorCode(t, response, http.StatusBadRequest, "invalid_ui_colors")
		})
	}
	var stored map[string]json.RawMessage
	decodeJSON(t, settingsRequest(t, h, http.MethodGet, colorsPath, nil, h.Token), &stored)
	if len(stored) != 0 {
		t.Fatalf("colors after rejected writes = %#v, want {}", stored)
	}
}

func TestColorsDeleteRestoresTheDefaultPalette(t *testing.T) {
	h := api.NewTestHarness(t)
	response := settingsRequest(t, h, http.MethodPut, colorsPath, []byte(colorsPayload()), h.Token)
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("PUT colors status = %d, want 200", response.StatusCode)
	}
	response.Body.Close()

	var cleared struct {
		OK bool `json:"ok"`
	}
	decodeJSON(t, settingsRequest(t, h, http.MethodDelete, colorsPath, nil, h.Token), &cleared)
	if !cleared.OK {
		t.Fatalf("DELETE colors = %#v, want ok", cleared)
	}
	var stored map[string]json.RawMessage
	decodeJSON(t, settingsRequest(t, h, http.MethodGet, colorsPath, nil, h.Token), &stored)
	if len(stored) != 0 {
		t.Fatalf("colors after delete = %#v, want {}", stored)
	}
}

func TestColorsRejectOtherMethods(t *testing.T) {
	h := api.NewTestHarness(t)
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		response := settingsRequest(t, h, method, colorsPath, []byte(colorsPayload()), h.Token)
		assertErrorCode(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

const colorsRatings = `{"1":"#111111","2":"#222222","3":"#333333","4":"#444444","5":"#555555"}`

func colorsPayload() string {
	return `{"ratings":` + colorsRatings + `,"habits":` + colorsHabits() + `}`
}

func colorsHabits() string {
	colors := make([]string, 0, 20)
	for index := 0; index < 20; index++ {
		colors = append(colors, fmt.Sprintf("%q", fmt.Sprintf("#0000%02x", index)))
	}
	return "[" + strings.Join(colors, ",") + "]"
}
