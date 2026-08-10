package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/apperror"
)

const uiRatingsPayload = `{"1":"#111111","2":"#222222","3":"#333333","4":"#444444","5":"#555555"}`

func TestUIColorsRoundTripReplacesAndClears(t *testing.T) {
	svc := newEntriesTestService(t, "d5")
	ctx := context.Background()

	unset, err := svc.UIColors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unset != nil {
		t.Fatalf("unset colors = %s, want nil", unset)
	}

	payload := uiColorsPayload(uiRatingsPayload, uiHabitsPayload(uiHabitColorCount))
	saved, err := svc.SetUIColors(ctx, json.RawMessage(payload))
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != payload {
		t.Fatalf("saved colors = %s, want %s", saved, payload)
	}
	read, err := svc.UIColors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(read) != payload {
		t.Fatalf("stored colors = %s, want %s", read, payload)
	}

	uppercase := uiColorsPayload(`{"1":"#AABBCC","2":"#222222","3":"#333333","4":"#444444","5":"#555555"}`, uiHabitsPayload(uiHabitColorCount))
	if _, err := svc.SetUIColors(ctx, json.RawMessage(uppercase)); err != nil {
		t.Fatalf("uppercase hex colors = %v, want accepted", err)
	}
	read, err = svc.UIColors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(read) != uppercase {
		t.Fatalf("replaced colors = %s, want %s", read, uppercase)
	}
	var rows int
	if err := svc.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM delta_metadata WHERE key = ?", uiColorsKey).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("stored color rows = %d, want 1", rows)
	}

	if err := svc.ClearUIColors(ctx); err != nil {
		t.Fatal(err)
	}
	cleared, err := svc.UIColors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cleared != nil {
		t.Fatalf("cleared colors = %s, want nil", cleared)
	}
	if err := svc.ClearUIColors(ctx); err != nil {
		t.Fatalf("clearing unset colors = %v, want success", err)
	}
}

func TestSetUIColorsRejectsAnythingButTheExactPaletteShape(t *testing.T) {
	svc := newEntriesTestService(t, "d6")
	ctx := context.Background()
	habits := uiHabitsPayload(uiHabitColorCount)
	tests := []struct {
		name    string
		payload string
	}{
		{name: "empty body", payload: ""},
		{name: "null", payload: "null"},
		{name: "array", payload: "[]"},
		{name: "string", payload: `"#111111"`},
		{name: "trailing object", payload: uiColorsPayload(uiRatingsPayload, habits) + "{}"},
		{name: "unknown field", payload: `{"ratings":` + uiRatingsPayload + `,"habits":` + habits + `,"pixels":[]}`},
		{name: "missing ratings", payload: `{"habits":` + habits + `}`},
		{name: "missing habits", payload: `{"ratings":` + uiRatingsPayload + `}`},
		{name: "four ratings", payload: uiColorsPayload(`{"1":"#111111","2":"#222222","3":"#333333","4":"#444444"}`, habits)},
		{name: "six ratings", payload: uiColorsPayload(`{"1":"#111111","2":"#222222","3":"#333333","4":"#444444","5":"#555555","6":"#666666"}`, habits)},
		{name: "rating zero instead of five", payload: uiColorsPayload(`{"0":"#111111","1":"#111111","2":"#222222","3":"#333333","4":"#444444"}`, habits)},
		{name: "rating not a string", payload: uiColorsPayload(`{"1":1,"2":"#222222","3":"#333333","4":"#444444","5":"#555555"}`, habits)},
		{name: "rating without hash", payload: uiColorsPayload(`{"1":"111111","2":"#222222","3":"#333333","4":"#444444","5":"#555555"}`, habits)},
		{name: "rating not hex", payload: uiColorsPayload(`{"1":"#gggggg","2":"#222222","3":"#333333","4":"#444444","5":"#555555"}`, habits)},
		{name: "rating shorthand", payload: uiColorsPayload(`{"1":"#111","2":"#222222","3":"#333333","4":"#444444","5":"#555555"}`, habits)},
		{name: "nineteen habits", payload: uiColorsPayload(uiRatingsPayload, uiHabitsPayload(uiHabitColorCount-1))},
		{name: "twenty-one habits", payload: uiColorsPayload(uiRatingsPayload, uiHabitsPayload(uiHabitColorCount+1))},
		{name: "no habits", payload: uiColorsPayload(uiRatingsPayload, "[]")},
		{name: "habit not a string", payload: uiColorsPayload(uiRatingsPayload, strings.Replace(habits, `"#000000"`, "0", 1))},
		{name: "habit five hex digits", payload: uiColorsPayload(uiRatingsPayload, strings.Replace(habits, `"#000000"`, `"#12345"`, 1))},
		{name: "habit named color", payload: uiColorsPayload(uiRatingsPayload, strings.Replace(habits, `"#000000"`, `"red"`, 1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := svc.SetUIColors(ctx, json.RawMessage(tt.payload)); apperror.Code(err) != apperror.CodeInvalidUIColors {
				t.Fatalf("SetUIColors(%s) = %v (code %q), want %q", tt.payload, err, apperror.Code(err), apperror.CodeInvalidUIColors)
			}
		})
	}
	stored, err := svc.UIColors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stored != nil {
		t.Fatalf("stored colors after rejected writes = %s, want nil", stored)
	}
}

func uiColorsPayload(ratings, habits string) string {
	return `{"ratings":` + ratings + `,"habits":` + habits + `}`
}

func uiHabitsPayload(count int) string {
	colors := make([]string, 0, count)
	for index := 0; index < count; index++ {
		colors = append(colors, fmt.Sprintf("%q", fmt.Sprintf("#0000%02x", index)))
	}
	return "[" + strings.Join(colors, ",") + "]"
}
