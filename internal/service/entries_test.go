package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/storage"
)

func TestWorkHoursRoundTripKeepsAbsentApartFromZero(t *testing.T) {
	svc := newEntriesTestService(t, "c3")
	ctx := context.Background()

	created, err := svc.UpsertEntry(ctx, "2026-08-02", EntryPatch{Text: OptionalString{Set: true, Value: "no hours yet"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkHours != nil {
		t.Fatalf("new entry work hours = %v, want absent", *created.WorkHours)
	}

	decimal, err := svc.UpsertEntry(ctx, "2026-08-02", EntryPatch{WorkHours: OptionalWorkHours{Set: true, Value: workHoursPointer(7.5)}})
	if err != nil {
		t.Fatal(err)
	}
	if decimal.WorkHours == nil || *decimal.WorkHours != 7.5 {
		t.Fatalf("work hours = %v, want 7.5", decimal.WorkHours)
	}
	if decimal.Text != "no hours yet" {
		t.Fatalf("work-hours write cleared text: %#v", decimal)
	}
	reread, err := svc.GetEntry(ctx, "2026-08-02")
	if err != nil {
		t.Fatal(err)
	}
	if reread.WorkHours == nil || *reread.WorkHours != 7.5 {
		t.Fatalf("stored work hours = %v, want 7.5", reread.WorkHours)
	}

	zero, err := svc.UpsertEntry(ctx, "2026-08-02", EntryPatch{WorkHours: OptionalWorkHours{Set: true, Value: workHoursPointer(0)}})
	if err != nil {
		t.Fatal(err)
	}
	if zero.WorkHours == nil || *zero.WorkHours != 0 {
		t.Fatalf("work hours = %v, want a recorded 0 rather than absent", zero.WorkHours)
	}

	untouched, err := svc.UpsertEntry(ctx, "2026-08-02", EntryPatch{Text: OptionalString{Set: true, Value: "still zero hours"}})
	if err != nil {
		t.Fatal(err)
	}
	if untouched.WorkHours == nil || *untouched.WorkHours != 0 {
		t.Fatalf("omitted work hours = %v, want the stored 0 preserved", untouched.WorkHours)
	}

	cleared, err := svc.UpsertEntry(ctx, "2026-08-02", EntryPatch{WorkHours: OptionalWorkHours{Set: true}})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.WorkHours != nil {
		t.Fatalf("cleared work hours = %v, want absent", *cleared.WorkHours)
	}
	entries, err := svc.ListEntries(ctx, "2026-08-01", "2026-08-03")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].WorkHours != nil {
		t.Fatalf("range entries = %#v, want one entry with absent work hours", entries)
	}
}

func TestWorkHoursRejectsValuesOutsideTheDay(t *testing.T) {
	svc := newEntriesTestService(t, "c4")
	for _, tt := range []struct {
		name  string
		value float64
	}{
		{name: "negative", value: -0.5},
		{name: "above twenty-four", value: 24.5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.UpsertEntry(context.Background(), "2026-08-02", EntryPatch{
				WorkHours: OptionalWorkHours{Set: true, Value: workHoursPointer(tt.value)},
			})
			if apperror.Code(err) != apperror.CodeInvalidEntry || !strings.Contains(apperror.Message(err), "between 0 and 24") {
				t.Fatalf("work hours %v error = %v (code %q)", tt.value, err, apperror.Code(err))
			}
		})
	}
	for _, value := range []float64{0, 0.25, 24} {
		entry, err := svc.UpsertEntry(context.Background(), "2026-08-03", EntryPatch{
			WorkHours: OptionalWorkHours{Set: true, Value: workHoursPointer(value)},
		})
		if err != nil {
			t.Fatalf("work hours %v = %v, want an accepted boundary value", value, err)
		}
		if entry.WorkHours == nil || *entry.WorkHours != value {
			t.Fatalf("work hours = %v, want %v", entry.WorkHours, value)
		}
	}
}

func TestListEntryDatesMatchesListEntriesFilteringAndOrder(t *testing.T) {
	svc := newEntriesTestService(t, "c5")
	ctx := context.Background()
	for _, date := range []string{"2026-08-03", "2026-08-01", "2026-08-05"} {
		if _, err := svc.UpsertEntry(ctx, date, EntryPatch{Text: OptionalString{Set: true, Value: date}}); err != nil {
			t.Fatal(err)
		}
	}

	for _, tt := range []struct {
		name string
		from string
		to   string
		want []string
	}{
		{name: "unbounded", want: []string{"2026-08-01", "2026-08-03", "2026-08-05"}},
		{name: "from", from: "2026-08-03", want: []string{"2026-08-03", "2026-08-05"}},
		{name: "to", to: "2026-08-03", want: []string{"2026-08-01", "2026-08-03"}},
		{name: "range", from: "2026-08-02", to: "2026-08-04", want: []string{"2026-08-03"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dates, err := svc.ListEntryDates(ctx, tt.from, tt.to)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(dates))
			for _, date := range dates {
				got = append(got, date.Date)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("entry dates = %v, want %v", got, tt.want)
			}
			entries, err := svc.ListEntries(ctx, tt.from, tt.to)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != len(dates) {
				t.Fatalf("entries = %d, dates = %d, want the same rows", len(entries), len(dates))
			}
			for index := range entries {
				if entries[index].Date != dates[index].Date {
					t.Fatalf("row %d date = %q, want %q", index, dates[index].Date, entries[index].Date)
				}
			}
		})
	}

	if _, err := svc.ListEntryDates(ctx, "2026-02-30", ""); apperror.Code(err) != apperror.CodeInvalidDate {
		t.Fatalf("invalid from date error = %v (code %q)", err, apperror.Code(err))
	}
	if _, err := svc.ListEntryDates(ctx, "2026-08-05", "2026-08-01"); apperror.Code(err) != apperror.CodeInvalidDate {
		t.Fatalf("reversed range error = %v (code %q)", err, apperror.Code(err))
	}
}

func newEntriesTestService(t *testing.T, keyByte string) *Service {
	t.Helper()
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "diary.db"), strings.Repeat(keyByte, storage.KeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		t.Fatal(err)
	}
	return New(store)
}

func workHoursPointer(value float64) *float64 { return &value }
