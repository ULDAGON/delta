package server_test

import (
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/api"
	"github.com/ferriskleier/delta/internal/service"
)

func TestGridDerivesRatingAndHabitValuesAcrossBackfilledAndEmptyDays(t *testing.T) {
	h := api.NewTestHarness(t)
	// Use a completed year so the assertions are independent of the month in
	// which the suite happens to run.
	year := time.Now().In(time.Local).Year() - 1
	day := func(day int) string { return fmt.Sprintf("%04d-01-%02d", year, day) }

	var first, second habitJSON
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"Read"}`), &first)
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"Move"}`), &second)
	rangeJSON := fmt.Sprintf(`{"ranges":[{"active_from":%q,"active_to":%q}]}`, day(1), day(7))
	decodeJSON(t, habitRequest(t, h, http.MethodPatch, habitPath(first.ID), rangeJSON), &first)
	decodeJSON(t, habitRequest(t, h, http.MethodPatch, habitPath(second.ID), rangeJSON), &second)

	putJSON(t, h, day(5), map[string]any{
		"text":    "five",
		"ratings": map[string]any{"total": 5, "body": 4, "mind": 3, "spirit": 2},
		"pixel":   2,
	})
	// An entry with ratings but no prose is still an entry, not a journal.
	putJSON(t, h, day(9), map[string]any{"ratings": map[string]any{"total": 3}})
	// This check-off is retained by storage but is outside the active range and
	// therefore must not affect the derived score.
	decodeJSON(t, habitRequest(t, h, http.MethodPost, checkoffPath(day(8), first.ID), ""), new(map[string]any))
	decodeJSON(t, habitRequest(t, h, http.MethodPost, checkoffPath(day(5), first.ID), ""), new(map[string]any))
	putJSON(t, h, fmt.Sprintf("%04d-12-31", year-1), map[string]any{"text": "old", "ratings": map[string]any{"total": 1}})

	rating := gridRequest(t, h, year, service.GridViewRating)
	if len(rating.Days) != 365 && len(rating.Days) != 366 {
		t.Fatalf("rating day count = %d, want calendar year", len(rating.Days))
	}
	if rating.EarliestYear == nil || *rating.EarliestYear != year-1 {
		t.Fatalf("earliest year = %v, want %d", rating.EarliestYear, year-1)
	}
	if len(rating.Years) != 3 || rating.Years[0] != year-1 || rating.Years[1] != year || rating.Years[2] != year+1 {
		t.Fatalf("year rail metadata = %#v", rating.Years)
	}
	dayFive := gridDay(t, rating, day(5))
	if dayFive.Rating == nil || *dayFive.Rating != 5 || dayFive.HabitScore == nil || *dayFive.HabitScore != 50 {
		t.Fatalf("rated day = %#v", dayFive)
	}
	if dayFive.Body == nil || *dayFive.Body != 4 || dayFive.Mind == nil || *dayFive.Mind != 3 || dayFive.Spirit == nil || *dayFive.Spirit != 2 {
		t.Fatalf("tooltip ratings = %#v", dayFive)
	}
	if dayFive.ActiveHabits != 2 || dayFive.CheckedHabits != 1 || !dayFive.HasEntry || !dayFive.Journal {
		t.Fatalf("rated day habit details = %#v", dayFive)
	}
	if dayFive.Pixel != 2 {
		t.Fatalf("rated day pixel = %d, want 2", dayFive.Pixel)
	}
	daySix := gridDay(t, rating, day(6))
	if daySix.Rating != nil || daySix.HasEntry {
		t.Fatalf("empty day = %#v, want uniform no-data", daySix)
	}
	if daySix.Pixel != 0 {
		t.Fatalf("empty day pixel = %d, want 0", daySix.Pixel)
	}
	dayEight := gridDay(t, rating, day(8))
	if dayEight.CheckedHabits != 0 || dayEight.ActiveHabits != 0 || dayEight.HabitScore != nil {
		t.Fatalf("out-of-range check-off day = %#v, want uncounted and unscored", dayEight)
	}
	dayNine := gridDay(t, rating, day(9))
	if dayNine.Rating == nil || *dayNine.Rating != 3 || !dayNine.HasEntry || dayNine.Journal {
		t.Fatalf("zero-active rated entry = %#v", dayNine)
	}

	habit := gridRequest(t, h, year, service.GridViewHabit)
	dayFive = gridDay(t, habit, day(5))
	if dayFive.Rating == nil || *dayFive.Rating != 5 || dayFive.HabitScore == nil || math.Abs(*dayFive.HabitScore-50) > 0.000001 {
		t.Fatalf("habit score = %#v, want 50%%", dayFive)
	}
	if dayFive.Pixel != 2 {
		t.Fatalf("habit-view day pixel = %d, want 2 (view-independent)", dayFive.Pixel)
	}
	daySix = gridDay(t, habit, day(6))
	if daySix.HabitScore == nil || *daySix.HabitScore != 0 || daySix.HasEntry {
		t.Fatalf("empty active-habit day = %#v, want 0%% derived score and no-data entry state", daySix)
	}
	dayNine = gridDay(t, habit, day(9))
	if dayNine.Rating == nil || *dayNine.Rating != 3 || dayNine.HabitScore != nil {
		t.Fatalf("zero-active day score = %#v, want unscored", dayNine)
	}
	if habit.Summary.Entries != 3 || habit.Summary.Characters != 4 || habit.Summary.AverageRating == nil || *habit.Summary.AverageRating != 4 {
		t.Fatalf("summary = %#v", habit.Summary)
	}
	if habit.Summary.HabitPercent == nil || math.Abs(*habit.Summary.HabitPercent-(50.0/7.0)) > 0.000001 {
		t.Fatalf("habit summary = %v, want %.6f", habit.Summary.HabitPercent, 50.0/7.0)
	}
}

func gridRequest(t *testing.T, h *api.Harness, year int, view string) service.GridResponse {
	t.Helper()
	response := habitRequest(t, h, http.MethodGet, fmt.Sprintf("/api/grid?year=%d&view=%s", year, view), "")
	var grid service.GridResponse
	decodeJSON(t, response, &grid)
	if grid.Year != year || grid.View != view {
		t.Fatalf("grid metadata = %#v", grid)
	}
	return grid
}

func gridDay(t *testing.T, grid service.GridResponse, date string) service.GridDay {
	t.Helper()
	for _, day := range grid.Days {
		if day.Date == date {
			return day
		}
	}
	t.Fatalf("grid has no day %s", date)
	return service.GridDay{}
}

func TestGridRejectsUnknownView(t *testing.T) {
	h := api.NewTestHarness(t)
	assertErrorCode(t, habitRequest(t, h, http.MethodGet, "/api/grid?view=unknown", ""), http.StatusBadRequest, "invalid_grid")
}
