package service

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/storage"
)

func TestGridUsesInjectableCurrentDateForFutureDays(t *testing.T) {
	const today = "2099-06-15"
	const future = "2099-06-16"
	habitToday = func() string { return today }
	t.Cleanup(func() { habitToday = localToday })

	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "diary.db"), strings.Repeat("b2", storage.KeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	svc := New(store)
	habit, err := svc.CreateHabit(context.Background(), "Future habit")
	if err != nil {
		t.Fatal(err)
	}
	ranges := []HabitRange{{ActiveFrom: future}}
	if _, err := svc.PatchHabit(context.Background(), habit.ID, HabitPatch{Ranges: &ranges}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpsertEntry(context.Background(), future, EntryPatch{
		Text:    OptionalString{Set: true, Value: "future prose"},
		Ratings: RatingsPatch{Total: OptionalRating{Set: true, Value: intPointer(5)}},
	}); err != nil {
		t.Fatal(err)
	}

	grid, err := svc.Grid(context.Background(), 2099, GridViewRating)
	if err != nil {
		t.Fatal(err)
	}
	if grid.CurrentYear != 2099 {
		t.Fatalf("current year = %d, want 2099", grid.CurrentYear)
	}
	var futureDay GridDay
	for _, day := range grid.Days {
		if day.Date == future {
			futureDay = day
			break
		}
	}
	if futureDay.Date == "" {
		t.Fatal("future day missing from grid")
	}
	if futureDay.HasEntry || futureDay.Journal || futureDay.Rating != nil || futureDay.HabitScore != nil || futureDay.Body != nil || futureDay.Mind != nil || futureDay.Spirit != nil {
		t.Fatalf("future day = %#v, want empty no-data fields", futureDay)
	}
	if futureDay.Pixel != 0 {
		t.Fatalf("future day pixel = %d, want 0", futureDay.Pixel)
	}
	if grid.Summary.Entries != 0 || grid.Summary.Characters != 0 || grid.Summary.AverageRating != nil || grid.Summary.HabitPercent != nil {
		t.Fatalf("future entry leaked into summary = %#v", grid.Summary)
	}
}

func TestGridMonthQuickViewCoversCurrentMonthOnly(t *testing.T) {
	type entryFixture struct {
		date    string
		total   *int
		checked bool
	}
	tests := []struct {
		name            string
		today           string
		year            int
		ranges          []HabitRange
		entries         []entryFixture
		wantYearRating  *float64
		wantYearHabit   *float64
		wantMonthRating *float64
		wantMonthHabit  *float64
	}{
		{
			name:   "month window skips earlier months and future days",
			today:  "2099-06-03",
			year:   2099,
			ranges: []HabitRange{{ActiveFrom: "2099-06-01"}},
			entries: []entryFixture{
				{date: "2099-05-20", total: intPointer(5)},
				{date: "2099-06-01", total: intPointer(4), checked: true},
				{date: "2099-06-02", total: intPointer(2)},
				{date: "2099-06-10", total: intPointer(1)},
			},
			wantYearRating:  floatPointer(11.0 / 3.0),
			wantYearHabit:   floatPointer(100.0 / 3.0),
			wantMonthRating: floatPointer(3),
			wantMonthHabit:  floatPointer(100.0 / 3.0),
		},
		{
			name:   "month without entries or active habits has no quick view",
			today:  "2099-06-15",
			year:   2099,
			ranges: []HabitRange{{ActiveFrom: "2099-05-01", ActiveTo: statsStringPointer("2099-05-31")}},
			entries: []entryFixture{
				{date: "2099-05-10", total: intPointer(4), checked: true},
			},
			wantYearRating:  floatPointer(4),
			wantYearHabit:   floatPointer(100.0 / 31.0),
			wantMonthRating: nil,
			wantMonthHabit:  nil,
		},
		{
			name:   "past year in view keeps the current month quick view",
			today:  "2099-06-03",
			year:   2098,
			ranges: []HabitRange{{ActiveFrom: "2099-06-01"}},
			entries: []entryFixture{
				{date: "2098-03-01", total: intPointer(5)},
				{date: "2098-03-02", total: intPointer(3)},
				{date: "2099-06-01", total: intPointer(2), checked: true},
				{date: "2099-06-02", total: intPointer(4)},
			},
			wantYearRating:  floatPointer(4),
			wantYearHabit:   nil,
			wantMonthRating: floatPointer(3),
			wantMonthHabit:  floatPointer(100.0 / 3.0),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			habitToday = func() string { return test.today }
			t.Cleanup(func() { habitToday = localToday })

			store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "diary.db"), strings.Repeat("c3", storage.KeyBytes))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := storage.Migrate(context.Background(), store.DB); err != nil {
				t.Fatal(err)
			}
			svc := New(store)
			habit, err := svc.CreateHabit(context.Background(), "Quick view")
			if err != nil {
				t.Fatal(err)
			}
			ranges := test.ranges
			if _, err := svc.PatchHabit(context.Background(), habit.ID, HabitPatch{Ranges: &ranges}); err != nil {
				t.Fatal(err)
			}
			for _, fixture := range test.entries {
				if _, err := svc.UpsertEntry(context.Background(), fixture.date, EntryPatch{
					Ratings: RatingsPatch{Total: OptionalRating{Set: true, Value: fixture.total}},
				}); err != nil {
					t.Fatal(err)
				}
				if fixture.checked {
					if _, err := svc.SetCheckoff(context.Background(), fixture.date, habit.ID, true); err != nil {
						t.Fatal(err)
					}
				}
			}

			grid, err := svc.Grid(context.Background(), test.year, GridViewRating)
			if err != nil {
				t.Fatal(err)
			}
			assertAverage(t, "year rating", grid.Summary.AverageRating, test.wantYearRating)
			assertAverage(t, "year habit", grid.Summary.HabitPercent, test.wantYearHabit)
			assertAverage(t, "month rating", grid.Summary.MonthAverageRating, test.wantMonthRating)
			assertAverage(t, "month habit", grid.Summary.MonthHabitPercent, test.wantMonthHabit)
		})
	}
}

func TestGridDayLoopsAreDSTSafe(t *testing.T) {
	// America/Havana springs forward at local midnight (2025: March 9), the
	// case where a midnight-anchored day cursor visits the previous date
	// twice. Guards the noon anchor in both the year loop and monthQuickView.
	zone, err := time.LoadLocation("America/Havana")
	if err != nil {
		t.Skipf("America/Havana tzdata unavailable: %v", err)
	}
	previousLocal := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = previousLocal })
	habitToday = func() string { return "2025-03-20" }
	t.Cleanup(func() { habitToday = localToday })

	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "diary.db"), strings.Repeat("b2", storage.KeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	svc := New(store)
	// 2025-03-08 is the date a midnight cursor would double-visit; counting
	// its rating twice would pull both averages from 4.0 to (5+5+3)/3.
	for date, rating := range map[string]int{"2025-03-08": 5, "2025-03-10": 3} {
		if _, err := svc.UpsertEntry(context.Background(), date, EntryPatch{
			Ratings: RatingsPatch{Total: OptionalRating{Set: true, Value: intPointer(rating)}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	grid, err := svc.Grid(context.Background(), 2025, GridViewRating)
	if err != nil {
		t.Fatal(err)
	}
	if len(grid.Days) != 365 {
		t.Fatalf("Havana 2025 grid has %d days, want 365", len(grid.Days))
	}
	seen := make(map[string]bool, len(grid.Days))
	for _, day := range grid.Days {
		if seen[day.Date] {
			t.Fatalf("date %s appears twice in the grid", day.Date)
		}
		seen[day.Date] = true
	}
	assertAverage(t, "year average", grid.Summary.AverageRating, floatPointer(4))
	assertAverage(t, "month average", grid.Summary.MonthAverageRating, floatPointer(4))
}

func assertAverage(t *testing.T, label string, got, want *float64) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Fatalf("%s = %.6f, want nil", label, *got)
	case want != nil && got == nil:
		t.Fatalf("%s = nil, want %.6f", label, *want)
	case want != nil && math.Abs(*got-*want) > 0.000001:
		t.Fatalf("%s = %.6f, want %.6f", label, *got, *want)
	}
}

func intPointer(value int) *int { return &value }

func floatPointer(value float64) *float64 { return &value }
