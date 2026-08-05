package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/api"
)

type habitRangeJSON struct {
	ActiveFrom string  `json:"active_from"`
	ActiveTo   *string `json:"active_to"`
}

type habitJSON struct {
	ID       int64            `json:"id"`
	Name     string           `json:"name"`
	Position int              `json:"position"`
	Ranges   []habitRangeJSON `json:"ranges"`
}

func TestHabitLifecycleAndSameDayArchiveResumeUndo(t *testing.T) {
	h := api.NewTestHarness(t)
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	created := habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"Meditate"}`)
	if created.StatusCode != http.StatusCreated {
		body := readEntryBody(t, created)
		t.Fatalf("create status = %d, body = %s", created.StatusCode, body)
	}
	var habit habitJSON
	decodeJSON(t, created, &habit)
	if habit.ID == 0 || habit.Name != "Meditate" || habit.Position != 0 || len(habit.Ranges) != 1 {
		t.Fatalf("created habit = %#v", habit)
	}
	if habit.Ranges[0].ActiveFrom != today || habit.Ranges[0].ActiveTo != nil {
		t.Fatalf("created range = %#v, want %s through open", habit.Ranges[0], today)
	}
	backdated := fmt.Sprintf(`{"ranges":[{"active_from":%q,"active_to":null}]}`, yesterday)
	decodeJSON(t, habitRequest(t, h, http.MethodPatch, habitPath(habit.ID), backdated), &habit)
	wantOpenRanges := []habitRangeJSON{{ActiveFrom: yesterday}}
	if !reflect.DeepEqual(habit.Ranges, wantOpenRanges) {
		t.Fatalf("backdated ranges = %#v, want %#v", habit.Ranges, wantOpenRanges)
	}
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	missingUncheck := habitRequest(t, h, http.MethodDelete, checkoffPath(tomorrow, habit.ID), "")
	if missingUncheck.StatusCode != http.StatusOK {
		t.Fatalf("uncheck on missing entry status = %d", missingUncheck.StatusCode)
	}
	missingUncheck.Body.Close()
	missingEntry := habitRequest(t, h, http.MethodGet, "/api/entries/"+tomorrow, "")
	assertErrorCode(t, missingEntry, http.StatusNotFound, "entry_not_found")

	listed := habitRequest(t, h, http.MethodGet, "/api/habits", "")
	var habits []habitJSON
	decodeJSON(t, listed, &habits)
	if len(habits) != 1 || habits[0].ID != habit.ID {
		t.Fatalf("listed habits = %#v", habits)
	}

	for round := 0; round < 4; round++ {
		archived := habitRequest(t, h, http.MethodPatch, habitPath(habit.ID), `{"archived":true}`)
		decodeJSON(t, archived, &habit)
		if len(habit.Ranges) != 1 || habit.Ranges[0].ActiveFrom != yesterday || habit.Ranges[0].ActiveTo == nil || *habit.Ranges[0].ActiveTo != today {
			t.Fatalf("round %d archived ranges = %#v", round, habit.Ranges)
		}
		resumed := habitRequest(t, h, http.MethodPatch, habitPath(habit.ID), `{"archived":false}`)
		decodeJSON(t, resumed, &habit)
		if !reflect.DeepEqual(habit.Ranges, wantOpenRanges) {
			t.Fatalf("round %d resumed ranges = %#v, want %#v", round, habit.Ranges, wantOpenRanges)
		}
	}

	checkoff := habitRequest(t, h, http.MethodPost, checkoffPath(today, habit.ID), "")
	var entry map[string]any
	decodeJSON(t, checkoff, &entry)
	if got := entry["checkoffs"].([]any); len(got) != 1 || got[0] != strconv.FormatInt(habit.ID, 10) {
		t.Fatalf("check-off = %#v, want stable habit ID", got)
	}
	repeated := habitRequest(t, h, http.MethodPost, checkoffPath(today, habit.ID), "")
	var repeatedEntry map[string]any
	decodeJSON(t, repeated, &repeatedEntry)
	if got := repeatedEntry["checkoffs"].([]any); len(got) != 1 {
		t.Fatalf("repeated check-off = %#v, want one item", got)
	}

	uncheck := habitRequest(t, h, http.MethodDelete, checkoffPath(today, habit.ID), "")
	var unchecked map[string]any
	decodeJSON(t, uncheck, &unchecked)
	if got := unchecked["checkoffs"].([]any); len(got) != 0 {
		t.Fatalf("uncheck = %#v, want empty", got)
	}
	repeatedUncheck := habitRequest(t, h, http.MethodDelete, checkoffPath(today, habit.ID), "")
	if repeatedUncheck.StatusCode != http.StatusOK {
		t.Fatalf("repeated uncheck status = %d", repeatedUncheck.StatusCode)
	}
	repeatedUncheck.Body.Close()
}

func TestHabitCheckoffsHideOutsideRangeAndRestoreAfterRangeEdit(t *testing.T) {
	h := api.NewTestHarness(t)
	today := time.Now()
	todayKey := today.Format("2006-01-02")
	tomorrowKey := today.AddDate(0, 0, 1).Format("2006-01-02")

	created := habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"Read"}`)
	var habit habitJSON
	decodeJSON(t, created, &habit)
	edit := fmt.Sprintf(`{"ranges":[{"active_from":%q,"active_to":%q}]}`, todayKey, todayKey)
	updated := habitRequest(t, h, http.MethodPatch, habitPath(habit.ID), edit)
	decodeJSON(t, updated, &habit)

	outside := habitRequest(t, h, http.MethodPost, checkoffPath(tomorrowKey, habit.ID), "")
	var outsideEntry map[string]any
	decodeJSON(t, outside, &outsideEntry)
	if got := outsideEntry["checkoffs"].([]any); len(got) != 0 {
		t.Fatalf("out-of-range check-off response = %#v, want hidden", got)
	}
	readOutside := habitRequest(t, h, http.MethodGet, "/api/entries/"+tomorrowKey, "")
	var readEntry map[string]any
	decodeJSON(t, readOutside, &readEntry)
	if got := readEntry["checkoffs"].([]any); len(got) != 0 {
		t.Fatalf("out-of-range entry read = %#v, want hidden", got)
	}

	restore := habitRequest(t, h, http.MethodPatch, habitPath(habit.ID), fmt.Sprintf(`{"ranges":[{"active_from":%q,"active_to":null}]}`, todayKey))
	decodeJSON(t, restore, &habit)
	restored := habitRequest(t, h, http.MethodGet, "/api/entries/"+tomorrowKey, "")
	var restoredEntry map[string]any
	decodeJSON(t, restored, &restoredEntry)
	if got := restoredEntry["checkoffs"].([]any); len(got) != 1 || got[0] != strconv.FormatInt(habit.ID, 10) {
		t.Fatalf("restored entry = %#v, want preserved check-off", got)
	}

	rename := habitRequest(t, h, http.MethodPatch, habitPath(habit.ID), `{"name":"Read every day"}`)
	decodeJSON(t, rename, &habit)
	renamedEntry := habitRequest(t, h, http.MethodGet, "/api/entries/"+tomorrowKey, "")
	var renamed map[string]any
	decodeJSON(t, renamedEntry, &renamed)
	if got := renamed["checkoffs"].([]any); len(got) != 1 || got[0] != strconv.FormatInt(habit.ID, 10) {
		t.Fatalf("renamed past entry = %#v, want stable habit ID", got)
	}

	var duplicate habitJSON
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"Read every day"}`), &duplicate)
	duplicateCheckoff := habitRequest(t, h, http.MethodPost, checkoffPath(tomorrowKey, duplicate.ID), "")
	var duplicateEntry map[string]any
	decodeJSON(t, duplicateCheckoff, &duplicateEntry)
	got := duplicateEntry["checkoffs"].([]any)
	wantIDs := []any{strconv.FormatInt(habit.ID, 10), strconv.FormatInt(duplicate.ID, 10)}
	if !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("same-named check-offs = %#v, want both stable IDs %#v", got, wantIDs)
	}
}

func TestHabitPatchReordersWithContiguousPositions(t *testing.T) {
	h := api.NewTestHarness(t)
	var first, second, third habitJSON
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"First"}`), &first)
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"Second"}`), &second)
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"Third"}`), &third)
	decodeJSON(t, habitRequest(t, h, http.MethodPatch, habitPath(third.ID), `{"position":-1}`), &third)
	negative := habitRequest(t, h, http.MethodGet, "/api/habits", "")
	var negativeOrder []habitJSON
	decodeJSON(t, negative, &negativeOrder)
	if len(negativeOrder) != 3 || negativeOrder[0].Name != "Third" || negativeOrder[1].Name != "First" || negativeOrder[2].Name != "Second" {
		t.Fatalf("negative position order = %#v, want Third, First, Second", negativeOrder)
	}
	decodeJSON(t, habitRequest(t, h, http.MethodPatch, habitPath(third.ID), `{"position":99}`), &third)

	response := habitRequest(t, h, http.MethodGet, "/api/habits", "")
	var habits []habitJSON
	decodeJSON(t, response, &habits)
	if len(habits) != 3 || habits[0].Name != "First" || habits[1].Name != "Second" || habits[2].Name != "Third" {
		t.Fatalf("high position order = %#v, want First, Second, Third", habits)
	}
	for position, habit := range habits {
		if habit.Position != position {
			t.Fatalf("habit %q position = %d, want %d", habit.Name, habit.Position, position)
		}
	}
}

func TestHabitArchiveRejectsInactiveRanges(t *testing.T) {
	today := time.Now()
	cases := []struct {
		name   string
		active string
		to     *string
	}{
		{name: "all future", active: today.AddDate(0, 0, 1).Format("2006-01-02")},
		{name: "all past", active: today.AddDate(0, 0, -2).Format("2006-01-02"), to: stringPtr(today.AddDate(0, 0, -1).Format("2006-01-02"))},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			h := api.NewTestHarness(t)
			var habit habitJSON
			decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"Inactive"}`), &habit)
			to := "null"
			if tt.to != nil {
				to = fmt.Sprintf("%q", *tt.to)
			}
			ranges := fmt.Sprintf(`{"ranges":[{"active_from":%q,"active_to":%s}]}`, tt.active, to)
			decodeJSON(t, habitRequest(t, h, http.MethodPatch, habitPath(habit.ID), ranges), &habit)
			assertErrorCode(t, habitRequest(t, h, http.MethodPatch, habitPath(habit.ID), `{"archived":true}`), http.StatusBadRequest, "habit_not_active")
		})
	}
}

func TestHabitArchiveClosesCurrentlyActiveFiniteRangeOnArchiveDay(t *testing.T) {
	h := api.NewTestHarness(t)
	today := time.Now()
	todayKey := today.Format("2006-01-02")
	tomorrowKey := today.AddDate(0, 0, 1).Format("2006-01-02")
	var habit habitJSON
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"Finite"}`), &habit)
	ranges := fmt.Sprintf(`{"ranges":[{"active_from":%q,"active_to":%q}]}`, todayKey, tomorrowKey)
	decodeJSON(t, habitRequest(t, h, http.MethodPatch, habitPath(habit.ID), ranges), &habit)
	decodeJSON(t, habitRequest(t, h, http.MethodPatch, habitPath(habit.ID), `{"archived":true}`), &habit)
	if len(habit.Ranges) != 1 || habit.Ranges[0].ActiveTo == nil || *habit.Ranges[0].ActiveTo != todayKey {
		t.Fatalf("archived finite range = %#v, want archive day %s", habit.Ranges, todayKey)
	}
}

func habitRequest(t *testing.T, h *api.Harness, method, path, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, h.Server.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+h.Token)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.Server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeJSON(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body := readEntryBody(t, response)
		t.Fatalf("response status = %d, body = %s", response.StatusCode, body)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func habitPath(id int64) string { return "/api/habits/" + strconv.FormatInt(id, 10) }

func checkoffPath(date string, id int64) string {
	return "/api/entries/" + url.PathEscape(date) + "/checkoffs/" + strconv.FormatInt(id, 10)
}

func stringPtr(value string) *string { return &value }
