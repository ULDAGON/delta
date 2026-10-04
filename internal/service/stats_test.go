package service

import (
	"context"
	"encoding/json"
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

// newStatsTestService pins today and opens a fresh service. Each test passes
// its own key byte so the temp databases stay distinct.
func newStatsTestService(t *testing.T, today, key string) *Service {
	t.Helper()
	oldToday, oldNow := habitToday, serviceNow
	habitToday = func() string { return today }
	serviceNow = func() time.Time {
		value, _ := time.ParseInLocation(serviceDateFormat, today, time.Local)
		return value.Add(12 * time.Hour)
	}
	t.Cleanup(func() {
		habitToday = oldToday
		serviceNow = oldNow
	})
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "diary.db"), strings.Repeat(key, storage.KeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	return New(store)
}

func writeStatsEntry(t *testing.T, svc *Service, date string, characters int, total *int, workHours *float64) {
	t.Helper()
	patch := EntryPatch{Text: OptionalString{Set: true, Value: strings.Repeat("x", characters)}}
	if total != nil {
		patch.Ratings = RatingsPatch{Total: OptionalRating{Set: true, Value: total}}
	}
	if workHours != nil {
		patch.WorkHours = OptionalWorkHours{Set: true, Value: workHours}
	}
	if _, err := svc.UpsertEntry(context.Background(), date, patch); err != nil {
		t.Fatal(err)
	}
}

func statsForYear(t *testing.T, svc *Service, year string) StatsResponse {
	t.Helper()
	stats, err := svc.Stats(context.Background(), year+"-01-01", year+"-12-31", "month")
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

func TestStatsCharactersRecordCurrentYearBehindNeedsPerDayPace(t *testing.T) {
	// 22 Dec through 31 Dec inclusive is 10 days left.
	svc := newStatsTestService(t, "2099-12-22", "b1")
	writeStatsEntry(t, svc, "2097-05-05", 400000, nil, nil)
	writeStatsEntry(t, svc, "2098-05-05", 1000000, nil, nil)
	writeStatsEntry(t, svc, "2099-01-05", 980000, nil, nil)

	record := statsForYear(t, svc, "2099").CharactersRecord
	if record == nil || record.State != CharactersRecordBehind {
		t.Fatalf("record = %#v, want behind", record)
	}
	if *record.RecordYear != 2098 || *record.RecordCharacters != 1000000 || *record.PerDay != 2000 {
		t.Fatalf("record = %#v, want record 2098 / 1000000 and 2000 per day", record)
	}
	if record.Difference != nil || record.PreviousYear != nil {
		t.Fatalf("behind record carries unused numbers = %#v", record)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"state":"behind","record_year":2098,"record_characters":1000000,"per_day":2000}`; string(encoded) != want {
		t.Fatalf("record JSON = %s, want %s", encoded, want)
	}
}

func TestStatsCharactersRecordPerDayRoundsUpAndNeverReadsZero(t *testing.T) {
	// 31 Dec leaves exactly one day.
	svc := newStatsTestService(t, "2099-12-31", "b2")
	writeStatsEntry(t, svc, "2098-05-05", 1000, nil, nil)
	writeStatsEntry(t, svc, "2099-01-05", 1000, nil, nil)
	record := statsForYear(t, svc, "2099").CharactersRecord
	if record == nil || record.State != CharactersRecordBehind || *record.PerDay != 1 {
		t.Fatalf("level totals record = %#v, want behind at 1 per day", record)
	}

	svc = newStatsTestService(t, "2099-12-29", "b3")
	writeStatsEntry(t, svc, "2098-05-05", 1000, nil, nil)
	writeStatsEntry(t, svc, "2099-01-05", 996, nil, nil)
	record = statsForYear(t, svc, "2099").CharactersRecord
	if record == nil || *record.PerDay != 2 {
		t.Fatalf("record = %#v, want ceil(4/3) = 2 per day", record)
	}
}

func TestStatsCharactersRecordCurrentYearAhead(t *testing.T) {
	svc := newStatsTestService(t, "2099-12-22", "b4")
	writeStatsEntry(t, svc, "2098-05-05", 1000000, nil, nil)
	writeStatsEntry(t, svc, "2099-01-05", 1000500, nil, nil)

	record := statsForYear(t, svc, "2099").CharactersRecord
	if record == nil || record.State != CharactersRecordAhead {
		t.Fatalf("record = %#v, want ahead", record)
	}
	if *record.RecordYear != 2098 || *record.RecordCharacters != 1000000 || *record.Difference != 500 {
		t.Fatalf("record = %#v, want record 2098 / 1000000 and +500", record)
	}
	if record.PerDay != nil {
		t.Fatalf("ahead record carries per_day = %d", *record.PerDay)
	}
}

func TestStatsCharactersRecordPastYears(t *testing.T) {
	svc := newStatsTestService(t, "2099-06-15", "b5")
	writeStatsEntry(t, svc, "2096-05-05", 100, nil, nil)
	writeStatsEntry(t, svc, "2097-05-05", 300, nil, nil)
	writeStatsEntry(t, svc, "2098-05-05", 500, nil, nil)
	writeStatsEntry(t, svc, "2099-01-05", 50, nil, nil)

	past := statsForYear(t, svc, "2097").CharactersRecord
	if past == nil || past.State != CharactersRecordPast || *past.PreviousYear != 2096 || *past.Difference != 200 {
		t.Fatalf("2097 record = %#v, want past vs 2096 at +200", past)
	}
	if past.RecordYear != nil || past.RecordCharacters != nil || past.PerDay != nil {
		t.Fatalf("past record carries unused numbers = %#v", past)
	}
	record := statsForYear(t, svc, "2098").CharactersRecord
	if record == nil || record.State != CharactersRecordRecord || record.Difference != nil || record.PreviousYear != nil {
		t.Fatalf("2098 record = %#v, want a bare record state", record)
	}
	// The current year counts as a competitor for the record.
	writeStatsEntry(t, svc, "2099-02-05", 900, nil, nil)
	if got := statsForYear(t, svc, "2098").CharactersRecord; got == nil || got.State != CharactersRecordPast || *got.Difference != 200 {
		t.Fatalf("2098 record after a bigger current year = %#v, want past at +200", got)
	}
}

func TestStatsCharactersRecordNegativePastDifference(t *testing.T) {
	svc := newStatsTestService(t, "2099-06-15", "b6")
	writeStatsEntry(t, svc, "2097-05-05", 500, nil, nil)
	writeStatsEntry(t, svc, "2098-05-05", 450, nil, nil)
	writeStatsEntry(t, svc, "2099-01-05", 1, nil, nil)
	record := statsForYear(t, svc, "2098").CharactersRecord
	if record == nil || record.State != CharactersRecordPast || *record.Difference != -50 {
		t.Fatalf("record = %#v, want past at -50", record)
	}
}

func TestStatsCharactersRecordIsNilWithoutAComparison(t *testing.T) {
	svc := newStatsTestService(t, "2099-06-15", "b7")
	writeStatsEntry(t, svc, "2098-05-05", 100, nil, nil)
	writeStatsEntry(t, svc, "2099-01-05", 500, nil, nil)

	for _, year := range []string{"2098", "2097", "2100"} {
		if record := statsForYear(t, svc, year).CharactersRecord; record != nil {
			t.Fatalf("%s record = %#v, want nil (first, before first and future year)", year, record)
		}
	}

	onlyCurrent := newStatsTestService(t, "2099-06-15", "b8")
	writeStatsEntry(t, onlyCurrent, "2099-01-05", 50, nil, nil)
	if record := statsForYear(t, onlyCurrent, "2099").CharactersRecord; record != nil {
		t.Fatalf("lone current year record = %#v, want nil", record)
	}
}

func TestStatsCharactersRecordIgnoresFutureEntries(t *testing.T) {
	svc := newStatsTestService(t, "2099-06-15", "b9")
	writeStatsEntry(t, svc, "2098-05-05", 100, nil, nil)
	writeStatsEntry(t, svc, "2099-01-05", 50, nil, nil)
	writeStatsEntry(t, svc, "2100-01-05", 5000, nil, nil)
	record := statsForYear(t, svc, "2099").CharactersRecord
	if record == nil || record.State != CharactersRecordBehind || *record.RecordYear != 2098 {
		t.Fatalf("record = %#v, want behind the 2098 record", record)
	}
}

func TestStatsComparisonsAreNilOutsideWholeCalendarYears(t *testing.T) {
	svc := newStatsTestService(t, "2099-06-15", "c1")
	writeStatsEntry(t, svc, "2098-05-05", 100, nil, nil)
	writeStatsEntry(t, svc, "2099-01-05", 50, nil, nil)

	for _, window := range [][2]string{
		{"2099-01-01", "2099-06-30"},
		{"2099-02-01", "2099-12-31"},
		{"2098-07-01", "2099-06-30"},
	} {
		stats, err := svc.Stats(context.Background(), window[0], window[1], "month")
		if err != nil {
			t.Fatal(err)
		}
		if stats.Changes != nil || stats.CharactersRecord != nil {
			t.Fatalf("%v changes/record = %#v / %#v, want both nil", window, stats.Changes, stats.CharactersRecord)
		}
		encoded, err := json.Marshal(stats)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"changes":null`) || !strings.Contains(string(encoded), `"characters_record":null`) {
			t.Fatalf("stats JSON = %s, want explicit null changes and characters_record", encoded)
		}
	}
}

func TestStatsChangesCompareYearToDateWithSamePeriodLastYear(t *testing.T) {
	svc := newStatsTestService(t, "2099-06-15", "c2")
	writeStatsEntry(t, svc, "2097-05-01", 50, intPointer(2), nil)
	writeStatsEntry(t, svc, "2098-03-01", 100, intPointer(4), floatPointer(5))
	// After 15 June 2098: part of the full previous year, not of the same period.
	writeStatsEntry(t, svc, "2098-09-01", 900, intPointer(1), floatPointer(5))
	writeStatsEntry(t, svc, "2099-03-01", 200, intPointer(5), floatPointer(6))

	current := statsForYear(t, svc, "2099").Changes
	if current == nil {
		t.Fatal("current year changes = nil")
	}
	assertChange(t, "current characters", current.Characters, 100)
	assertChange(t, "current total", current.Total, 25)
	assertChange(t, "current work hours", current.WorkHours, 20)
	assertChange(t, "current habit score", current.HabitScore, nil)

	// A past year is compared with the whole previous year.
	past := statsForYear(t, svc, "2098").Changes
	if past == nil {
		t.Fatal("past year changes = nil")
	}
	assertChange(t, "past characters", past.Characters, 1900)
	assertChange(t, "past total", past.Total, 25)
	assertChange(t, "past work hours", past.WorkHours, nil)
}

func TestStatsChangesNullWhenPreviousIsMissingOrZero(t *testing.T) {
	svc := newStatsTestService(t, "2099-06-15", "c3")
	// Previous year has entries, but no rating, no work hours and no text.
	writeStatsEntry(t, svc, "2098-03-01", 0, nil, nil)
	writeStatsEntry(t, svc, "2099-03-01", 10, intPointer(3), floatPointer(8))

	changes := statsForYear(t, svc, "2099").Changes
	if changes == nil {
		t.Fatal("changes = nil")
	}
	assertChange(t, "characters", changes.Characters, nil)
	assertChange(t, "total", changes.Total, nil)
	assertChange(t, "work hours", changes.WorkHours, nil)

	// A habit that was never checked last year averages 0, which is no base.
	habit, err := svc.CreateHabit(context.Background(), "Never checked")
	if err != nil {
		t.Fatal(err)
	}
	ranges := []HabitRange{{ActiveFrom: "2098-01-01"}}
	if _, err := svc.PatchHabit(context.Background(), habit.ID, HabitPatch{Ranges: &ranges}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetCheckoff(context.Background(), "2099-03-01", habit.ID, true); err != nil {
		t.Fatal(err)
	}
	stats := statsForYear(t, svc, "2099")
	if stats.Averages.HabitScore == nil || *stats.Averages.HabitScore <= 0 {
		t.Fatalf("habit average = %v, want a positive value", stats.Averages.HabitScore)
	}
	assertChange(t, "habit score", stats.Changes.HabitScore, nil)

	// The current value missing is null as well.
	empty := statsForYear(t, svc, "2098").Changes
	assertChange(t, "empty-year total", empty.Total, nil)
}

func TestStatsChangesHabitScoreAndFutureYear(t *testing.T) {
	svc := newStatsTestService(t, "2099-01-10", "c4")
	habit, err := svc.CreateHabit(context.Background(), "Walk")
	if err != nil {
		t.Fatal(err)
	}
	ranges := []HabitRange{{ActiveFrom: "2098-01-01"}}
	if _, err := svc.PatchHabit(context.Background(), habit.ID, HabitPatch{Ranges: &ranges}); err != nil {
		t.Fatal(err)
	}
	// 2098 to 10 Jan: 1 of 10 days checked (10%). 2099 to 10 Jan: 5 of 10 (50%).
	if _, err := svc.SetCheckoff(context.Background(), "2098-01-02", habit.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"2099-01-01", "2099-01-02", "2099-01-03", "2099-01-04", "2099-01-05"} {
		if _, err := svc.SetCheckoff(context.Background(), date, habit.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	assertChange(t, "habit score", statsForYear(t, svc, "2099").Changes.HabitScore, 400)

	future := statsForYear(t, svc, "2100").Changes
	if future == nil || *future != (StatsChanges{}) {
		t.Fatalf("future year changes = %#v, want all nil", future)
	}
}

func TestStatsChangesLeapDayComparesWithFebruary28(t *testing.T) {
	svc := newStatsTestService(t, "2096-02-29", "c5")
	writeStatsEntry(t, svc, "2095-02-28", 10, nil, nil)
	writeStatsEntry(t, svc, "2095-03-01", 1000, nil, nil)
	writeStatsEntry(t, svc, "2096-01-10", 20, nil, nil)
	assertChange(t, "characters", statsForYear(t, svc, "2096").Changes.Characters, 100)
}

func assertChange(t *testing.T, name string, got *float64, want any) {
	t.Helper()
	switch want := want.(type) {
	case nil:
		if got != nil {
			t.Fatalf("%s change = %v, want nil", name, *got)
		}
	case int:
		if got == nil || *got < float64(want)-1e-9 || *got > float64(want)+1e-9 {
			t.Fatalf("%s change = %v, want %d", name, got, want)
		}
	}
}
