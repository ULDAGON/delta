package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/storage"
)

func TestHabitResumeOnNextDayStartsSecondRange(t *testing.T) {
	dayOne := "2026-08-02"
	dayTwo := "2026-08-03"
	today := dayOne
	previousNow := serviceNow
	serviceNow = func() time.Time {
		parsed, err := time.ParseInLocation("2006-01-02", today, time.Local)
		if err != nil {
			t.Fatalf("parse test date: %v", err)
		}
		return parsed
	}
	t.Cleanup(func() { serviceNow = previousNow })

	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "diary.db"), strings.Repeat("a1", storage.KeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	svc := New(store)
	habit, err := svc.CreateHabit(context.Background(), "Cross-day")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.PatchHabit(context.Background(), habit.ID, HabitPatch{Archived: boolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	today = dayTwo
	resumed, err := svc.PatchHabit(context.Background(), habit.ID, HabitPatch{Archived: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	wantEnd := dayOne
	wantRanges := []HabitRange{
		{ActiveFrom: dayOne, ActiveTo: &wantEnd},
		{ActiveFrom: dayTwo},
	}
	if len(resumed.Ranges) != len(wantRanges) {
		t.Fatalf("resumed ranges = %#v, want %#v", resumed.Ranges, wantRanges)
	}
	for index := range wantRanges {
		if resumed.Ranges[index].ActiveFrom != wantRanges[index].ActiveFrom || !sameOptionalString(resumed.Ranges[index].ActiveTo, wantRanges[index].ActiveTo) {
			t.Fatalf("resumed ranges = %#v, want %#v", resumed.Ranges, wantRanges)
		}
	}
}

func boolPtr(value bool) *bool { return &value }

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
