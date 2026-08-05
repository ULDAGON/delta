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

func TestStatsMonthlyMathCharactersAndCompletionUseDerivedDays(t *testing.T) {
	h := api.NewTestHarness(t)
	year := time.Now().In(time.Local).Year() - 1
	day := func(number int) string { return fmt.Sprintf("%04d-01-%02d", year, number) }

	var habit habitJSON
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"Read"}`), &habit)
	decodeJSON(t, habitRequest(t, h, http.MethodPatch, habitPath(habit.ID), fmt.Sprintf(`{"ranges":[{"active_from":%q,"active_to":%q}]}`, day(1), day(2))), &habit)

	putJSON(t, h, day(1), map[string]any{
		"text": "abc",
		"goals": []map[string]any{
			{"text": "goal one", "checked": true}, {"text": "goal two"}, {"text": "goal three"},
			{"text": "goal four"}, {"text": "goal five"},
		},
		"gratitudes": []string{"gratitude one", "gratitude two", "gratitude three"},
		"ws": map[string]any{
			"went_well": "went well", "could_have_gone_better": "could improve", "goal_for_tomorrow": "tomorrow goal",
		},
		"ratings": map[string]any{"total": 4},
	})
	decodeJSON(t, habitRequest(t, h, http.MethodPost, checkoffPath(day(1), habit.ID), ""), new(map[string]any))
	putJSON(t, h, day(3), map[string]any{"text": "unrated", "ratings": map[string]any{"total": 2}})
	putJSON(t, h, day(4), map[string]any{"text": "no rating"})

	response := habitRequest(t, h, http.MethodGet, fmt.Sprintf("/api/stats?from=%s&to=%s&agg=month", day(1), day(31)), "")
	var stats service.StatsResponse
	decodeJSON(t, response, &stats)
	if stats.Aggregation != "month" || len(stats.Rating) != 1 || len(stats.HabitScore) != 1 {
		t.Fatalf("stats shape = %#v", stats)
	}
	if stats.Averages.Total == nil || math.Abs(*stats.Averages.Total-3) > 0.000001 {
		t.Fatalf("total average = %v, want 3 from rated entries only", stats.Averages.Total)
	}
	if stats.Averages.HabitScore == nil || math.Abs(*stats.Averages.HabitScore-50) > 0.000001 {
		t.Fatalf("habit average = %v, want 50 from 100%% + no-entry 0%%", stats.Averages.HabitScore)
	}
	if stats.Characters != len("abcunratedno rating") {
		t.Fatalf("characters = %d, want %d from freeform only", stats.Characters, len("abcunratedno rating"))
	}
	if stats.Rating[0].Value == nil || *stats.Rating[0].Value != 3 || stats.Rating[0].Samples != 2 {
		t.Fatalf("monthly rating = %#v", stats.Rating[0])
	}
	if stats.HabitScore[0].Value == nil || *stats.HabitScore[0].Value != 50 || stats.HabitScore[0].Samples != 2 {
		t.Fatalf("monthly habit score = %#v", stats.HabitScore[0])
	}
	if len(stats.Completion) != 1 || stats.Completion[0].ActiveDays != 2 || stats.Completion[0].Checked != 1 || stats.Completion[0].Percent != 50 {
		t.Fatalf("completion = %#v", stats.Completion)
	}
}

func TestStatsKeepsGraceAndArchivedStreaks(t *testing.T) {
	h := api.NewTestHarness(t)
	today := time.Now().In(time.Local)
	todayKey := today.Format("2006-01-02")
	yesterday := today.AddDate(0, 0, -1).Format("2006-01-02")

	var grace, archived, archivedUnchecked habitJSON
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"Grace"}`), &grace)
	decodeJSON(t, habitRequest(t, h, http.MethodPatch, habitPath(grace.ID), fmt.Sprintf(`{"ranges":[{"active_from":%q,"active_to":null}]}`, yesterday)), &grace)
	decodeJSON(t, habitRequest(t, h, http.MethodPost, checkoffPath(yesterday, grace.ID), ""), new(map[string]any))
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"Archived"}`), &archived)
	decodeJSON(t, habitRequest(t, h, http.MethodPatch, habitPath(archived.ID), fmt.Sprintf(`{"ranges":[{"active_from":%q,"active_to":%q}]}`, yesterday, yesterday)), &archived)
	decodeJSON(t, habitRequest(t, h, http.MethodPost, checkoffPath(yesterday, archived.ID), ""), new(map[string]any))
	decodeJSON(t, habitRequest(t, h, http.MethodPost, "/api/habits", `{"name":"Archived unchecked"}`), &archivedUnchecked)
	decodeJSON(t, habitRequest(t, h, http.MethodPatch, habitPath(archivedUnchecked.ID), fmt.Sprintf(`{"ranges":[{"active_from":%q,"active_to":%q}]}`, yesterday, yesterday)), &archivedUnchecked)

	response := habitRequest(t, h, http.MethodGet, fmt.Sprintf("/api/stats?from=%s&to=%s&agg=month", todayKey[:4]+"-01-01", todayKey[:4]+"-12-31"), "")
	var stats service.StatsResponse
	decodeJSON(t, response, &stats)
	for _, streak := range stats.Streaks {
		if streak.ID == grace.ID || streak.ID == archived.ID {
			if streak.Current != 1 || streak.Best != 1 {
				t.Fatalf("streak for %d = %#v, want 1 / 1 with today grace and archived final run", streak.ID, streak)
			}
		}
		if streak.ID == archivedUnchecked.ID && (streak.Current != 0 || streak.Best != 0) {
			t.Fatalf("unchecked archived streak = %#v, want 0 / 0", streak)
		}
	}
	if stats.Averages.Total != nil || stats.Characters != 0 {
		t.Fatalf("unexpected stats data: averages=%#v characters=%d", stats.Averages, stats.Characters)
	}
}

func TestStatsMonthlyWorkHoursAverageOnlyCountsRecordedDays(t *testing.T) {
	h := api.NewTestHarness(t)
	year := time.Now().In(time.Local).Year() - 1

	putJSON(t, h, fmt.Sprintf("%04d-01-01", year), map[string]any{"work_hours": 7.5})
	putJSON(t, h, fmt.Sprintf("%04d-01-02", year), map[string]any{"work_hours": 0})
	putJSON(t, h, fmt.Sprintf("%04d-01-03", year), map[string]any{"text": "no hours recorded"})
	putJSON(t, h, fmt.Sprintf("%04d-03-04", year), map[string]any{"work_hours": 6})

	response := habitRequest(t, h, http.MethodGet, fmt.Sprintf("/api/stats?from=%04d-01-01&to=%04d-12-31&agg=month", year, year), "")
	var stats service.StatsResponse
	decodeJSON(t, response, &stats)
	if len(stats.WorkHours) != 12 {
		t.Fatalf("work hours series = %d months, want 12", len(stats.WorkHours))
	}
	january := stats.WorkHours[0]
	if january.Month != fmt.Sprintf("%04d-01", year) || january.Samples != 2 || january.Value == nil || math.Abs(*january.Value-3.75) > 0.000001 {
		t.Fatalf("january work hours = %#v, want 3.75 over the two recorded days", january)
	}
	if february := stats.WorkHours[1]; february.Value != nil || february.Samples != 0 {
		t.Fatalf("february work hours = %#v, want an absent value rather than 0", february)
	}
	if march := stats.WorkHours[2]; march.Value == nil || *march.Value != 6 || march.Samples != 1 {
		t.Fatalf("march work hours = %#v, want 6 from one day", march)
	}
	if stats.Averages.WorkHours == nil || math.Abs(*stats.Averages.WorkHours-4.5) > 0.000001 {
		t.Fatalf("work hours average = %v, want 4.5 over the three recorded days", stats.Averages.WorkHours)
	}
}

func TestStatsWorkHoursAverageIsAbsentWithoutRecordedHours(t *testing.T) {
	h := api.NewTestHarness(t)
	year := time.Now().In(time.Local).Year() - 1
	putJSON(t, h, fmt.Sprintf("%04d-05-05", year), map[string]any{"text": "no hours recorded"})

	response := habitRequest(t, h, http.MethodGet, fmt.Sprintf("/api/stats?from=%04d-01-01&to=%04d-12-31&agg=month", year, year), "")
	var stats service.StatsResponse
	decodeJSON(t, response, &stats)
	if stats.Averages.WorkHours != nil {
		t.Fatalf("work hours average = %v, want absent", *stats.Averages.WorkHours)
	}
	for _, point := range stats.WorkHours {
		if point.Value != nil || point.Samples != 0 {
			t.Fatalf("month %s = %#v, want an absent value", point.Month, point)
		}
	}
}

func TestStatsRejectsNonMonthlyAggregation(t *testing.T) {
	h := api.NewTestHarness(t)
	assertErrorCode(t, habitRequest(t, h, http.MethodGet, "/api/stats?agg=day", ""), http.StatusBadRequest, "invalid_stats")
}
