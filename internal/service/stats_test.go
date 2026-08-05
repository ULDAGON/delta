package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/storage"
)

func TestHabitStreaksSkipInactiveDaysButBreakOnActiveUncheckedDays(t *testing.T) {
	const today = "2099-06-03"
	pauseGapHabitID := "1"
	activeUncheckedHabitID := "2"
	habits := []Habit{
		{ID: 1, Name: "Pause gap"},
		{ID: 2, Name: "Unchecked gap"},
	}
	schedules := []habitSchedule{
		{
			ID: pauseGapHabitID,
			Ranges: []HabitRange{
				{ActiveFrom: "2099-06-01", ActiveTo: statsStringPointer("2099-06-01")},
				{ActiveFrom: today, ActiveTo: statsStringPointer(today)},
			},
		},
		{
			ID:     activeUncheckedHabitID,
			Ranges: []HabitRange{{ActiveFrom: "2099-06-01", ActiveTo: statsStringPointer(today)}},
		},
	}
	entryByDate := map[string]Entry{
		"2099-06-01": {Date: "2099-06-01", Checkoffs: []string{pauseGapHabitID, activeUncheckedHabitID}},
		today:        {Date: today, Checkoffs: []string{pauseGapHabitID, activeUncheckedHabitID}},
	}

	streaks := calculateHabitStreaks(habits, schedules, entryByDate, today)
	if streaks[0].Current != 2 || streaks[0].Best != 2 {
		t.Fatalf("pause-gap streak = %#v, want current/best 2 / 2", streaks[0])
	}
	if streaks[1].Current != 1 || streaks[1].Best != 1 {
		t.Fatalf("active-unchecked streak = %#v, want current/best 1 / 1", streaks[1])
	}
}

func statsStringPointer(value string) *string { return &value }

func TestStatsExcludesFutureDatesAtInjectedToday(t *testing.T) {
	oldToday, oldNow := habitToday, serviceNow
	habitToday = func() string { return "2099-12-30" }
	serviceNow = func() time.Time { return time.Date(2099, time.December, 30, 12, 0, 0, 0, time.Local) }
	t.Cleanup(func() {
		habitToday = oldToday
		serviceNow = oldNow
	})

	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "diary.db"), strings.Repeat("e7", storage.KeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	svc := New(store)
	if _, err := svc.UpsertEntry(context.Background(), "2099-12-31", EntryPatch{
		Text:    OptionalString{Set: true, Value: "future prose"},
		Ratings: RatingsPatch{Total: OptionalRating{Set: true, Value: intPointer(5)}},
	}); err != nil {
		t.Fatal(err)
	}

	stats, err := svc.Stats(context.Background(), "2099-01-01", "2099-12-31", "month")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Entries != 0 || stats.Characters != 0 || stats.Averages.Total != nil {
		t.Fatalf("future entry leaked into stats = %#v", stats)
	}
}

func TestStatsStreakClockGraceExpiresAtInjectedMidnight(t *testing.T) {
	oldToday, oldNow := habitToday, serviceNow
	habitToday = func() string { return "2099-06-15" }
	serviceNow = func() time.Time { return time.Date(2099, time.June, 15, 21, 0, 0, 0, time.Local) }
	t.Cleanup(func() {
		habitToday = oldToday
		serviceNow = oldNow
	})

	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "diary.db"), strings.Repeat("d4", storage.KeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	svc := New(store)
	habit, err := svc.CreateHabit(context.Background(), "Clocked")
	if err != nil {
		t.Fatal(err)
	}
	ranges := []HabitRange{{ActiveFrom: "2099-06-14"}}
	if _, err := svc.PatchHabit(context.Background(), habit.ID, HabitPatch{Ranges: &ranges}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetCheckoff(context.Background(), "2099-06-14", habit.ID, true); err != nil {
		t.Fatal(err)
	}
	stats, err := svc.Stats(context.Background(), "2099-06-01", "2099-06-30", "month")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Streaks) != 1 || stats.Streaks[0].Current != 1 {
		t.Fatalf("before midnight streak = %#v, want current 1", stats.Streaks)
	}

	habitToday = func() string { return "2099-06-16" }
	stats, err = svc.Stats(context.Background(), "2099-06-01", "2099-06-30", "month")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Streaks) != 1 || stats.Streaks[0].Current != 0 || stats.Streaks[0].Best != 1 {
		t.Fatalf("after midnight streak = %#v, want current 0 and best 1", stats.Streaks)
	}
}

func TestStatsOmitsHabitsInactiveForWholeRange(t *testing.T) {
	oldToday, oldNow := habitToday, serviceNow
	habitToday = func() string { return "2099-06-15" }
	serviceNow = func() time.Time { return time.Date(2099, time.June, 15, 12, 0, 0, 0, time.Local) }
	t.Cleanup(func() {
		habitToday = oldToday
		serviceNow = oldNow
	})

	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "diary.db"), strings.Repeat("a1", storage.KeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	svc := New(store)

	ended, err := svc.CreateHabit(context.Background(), "Ended last year")
	if err != nil {
		t.Fatal(err)
	}
	endedRanges := []HabitRange{{ActiveFrom: "2098-01-01", ActiveTo: statsStringPointer("2098-12-31")}}
	if _, err := svc.PatchHabit(context.Background(), ended.ID, HabitPatch{Ranges: &endedRanges}); err != nil {
		t.Fatal(err)
	}
	living, err := svc.CreateHabit(context.Background(), "Still going")
	if err != nil {
		t.Fatal(err)
	}
	livingRanges := []HabitRange{{ActiveFrom: "2098-01-01"}}
	if _, err := svc.PatchHabit(context.Background(), living.ID, HabitPatch{Ranges: &livingRanges}); err != nil {
		t.Fatal(err)
	}

	stats, err := svc.Stats(context.Background(), "2099-01-01", "2099-12-31", "month")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Completion) != 1 || stats.Completion[0].ID != living.ID {
		t.Fatalf("completion = %#v, want only the still-active habit", stats.Completion)
	}
	if len(stats.Streaks) != 1 || stats.Streaks[0].ID != living.ID {
		t.Fatalf("streaks = %#v, want only the still-active habit", stats.Streaks)
	}

	previous, err := svc.Stats(context.Background(), "2098-01-01", "2098-12-31", "month")
	if err != nil {
		t.Fatal(err)
	}
	if len(previous.Completion) != 2 {
		t.Fatalf("2098 completion = %#v, want both habits", previous.Completion)
	}
}

func TestNextDateAdvancesAcrossMidnightDST(t *testing.T) {
	// In America/Havana, midnight of 2026-03-08 does not exist (spring
	// forward at 00:00). A midnight-anchored AddDate normalizes back onto
	// the 7th, making nextDate return its input and stalling every
	// date-walking loop above; a local midnight parse of the 8th itself
	// lands on the 7th too. Guards the UTC-parse + noon-anchor fix.
	zone, err := time.LoadLocation("America/Havana")
	if err != nil {
		t.Skipf("America/Havana tzdata unavailable: %v", err)
	}
	previousLocal := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = previousLocal })
	if got := nextDate("2026-03-07"); got != "2026-03-08" {
		t.Fatalf(`nextDate("2026-03-07") = %q, want "2026-03-08"`, got)
	}
	if got := nextDate("2026-03-08"); got != "2026-03-09" {
		t.Fatalf(`nextDate("2026-03-08") = %q, want "2026-03-09"`, got)
	}
	if got := nextMonth("2026-03-08"); got != "2026-04-08" {
		t.Fatalf(`nextMonth("2026-03-08") = %q, want "2026-04-08"`, got)
	}
}
