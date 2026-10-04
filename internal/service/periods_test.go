package service

import (
	"context"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/apperror"
)

func TestPeriodsListNewestFirstAndKeepKindsApart(t *testing.T) {
	svc := newEntriesTestService(t, "c3")
	ctx := context.Background()

	school := mustCreatePeriod(t, svc, PeriodEra, PeriodInput{Name: "School", Color: "#112233", StartDate: "2010-09-01", EndDate: periodDate("2018-06-30")})
	work := mustCreatePeriod(t, svc, PeriodEra, PeriodInput{Name: "  Work  ", Color: "#AABBCC", StartDate: "2022-01-01"})
	study := mustCreatePeriod(t, svc, PeriodEra, PeriodInput{Name: "Study", Color: "#445566", StartDate: "2018-10-01", EndDate: periodDate("2021-12-31")})
	if work.Name != "Work" || work.EndDate != nil || work.Color != "#AABBCC" {
		t.Fatalf("created era = %#v, want trimmed name, stored color, and no end", work)
	}

	// A dynasty may reuse an era's name and span every era: kinds never interact.
	dynasty := mustCreatePeriod(t, svc, PeriodDynasty, PeriodInput{Name: "school", Color: "#000000", StartDate: "2000-01-01"})

	eras, err := svc.ListPeriods(ctx, PeriodEra)
	if err != nil {
		t.Fatal(err)
	}
	if len(eras) != 3 || eras[0].ID != work.ID || eras[1].ID != study.ID || eras[2].ID != school.ID {
		t.Fatalf("eras = %#v, want newest start first", eras)
	}
	dynasties, err := svc.ListPeriods(ctx, PeriodDynasty)
	if err != nil {
		t.Fatal(err)
	}
	if len(dynasties) != 1 || dynasties[0].ID != dynasty.ID {
		t.Fatalf("dynasties = %#v", dynasties)
	}

	// An ID only addresses a period of its own kind.
	name := "Renamed"
	if _, err := svc.PatchPeriod(ctx, PeriodDynasty, work.ID, PeriodPatch{Name: &name}); apperror.Code(err) != apperror.CodeDynastyNotFound {
		t.Fatalf("patching an era as a dynasty = %v, want dynasty_not_found", err)
	}
	if err := svc.DeletePeriod(ctx, PeriodEra, dynasty.ID); apperror.Code(err) != apperror.CodeEraNotFound {
		t.Fatalf("deleting a dynasty as an era = %v, want era_not_found", err)
	}
	if err := svc.DeletePeriod(ctx, PeriodEra, school.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeletePeriod(ctx, PeriodEra, school.ID); apperror.Code(err) != apperror.CodeEraNotFound {
		t.Fatalf("second delete = %v, want era_not_found", err)
	}
}

func TestPeriodsRejectInvalidFieldsDuplicateNamesAndOverlaps(t *testing.T) {
	svc := newEntriesTestService(t, "d4")
	ctx := context.Background()
	mustCreatePeriod(t, svc, PeriodEra, PeriodInput{Name: "Berlin", Color: "#112233", StartDate: "2020-01-01", EndDate: periodDate("2020-12-31")})
	mustCreatePeriod(t, svc, PeriodEra, PeriodInput{Name: "Now", Color: "#112233", StartDate: "2024-01-01"})

	for _, tc := range []struct {
		name    string
		input   PeriodInput
		message string
	}{
		{"blank name", PeriodInput{Name: "   ", Color: "#112233", StartDate: "2022-01-01"}, "name cannot be empty"},
		{"missing color", PeriodInput{Name: "X", StartDate: "2022-01-01"}, "#rrggbb"},
		{"short color", PeriodInput{Name: "X", Color: "#123", StartDate: "2022-01-01"}, "#rrggbb"},
		{"non-hex color", PeriodInput{Name: "X", Color: "#12345g", StartDate: "2022-01-01"}, "#rrggbb"},
		{"missing start", PeriodInput{Name: "X", Color: "#112233"}, "start_date"},
		{"impossible start", PeriodInput{Name: "X", Color: "#112233", StartDate: "2022-02-30"}, "start_date"},
		{"impossible end", PeriodInput{Name: "X", Color: "#112233", StartDate: "2022-01-01", EndDate: periodDate("2022-13-01")}, "end_date"},
		{"end before start", PeriodInput{Name: "X", Color: "#112233", StartDate: "2022-03-01", EndDate: periodDate("2022-02-28")}, "end_date cannot be before start_date"},
		{"duplicate name", PeriodInput{Name: "bERLIN", Color: "#112233", StartDate: "2022-01-01", EndDate: periodDate("2022-01-02")}, `an era named "Berlin" already exists`},
		{"overlap on the last day", PeriodInput{Name: "X", Color: "#112233", StartDate: "2020-12-31", EndDate: periodDate("2021-06-30")}, `overlaps era "Berlin" (2020-01-01 to 2020-12-31)`},
		{"overlap on the first day", PeriodInput{Name: "X", Color: "#112233", StartDate: "2019-01-01", EndDate: periodDate("2020-01-01")}, `overlaps era "Berlin"`},
		{"contained", PeriodInput{Name: "X", Color: "#112233", StartDate: "2020-03-01", EndDate: periodDate("2020-03-31")}, `overlaps era "Berlin"`},
		{"inside the ongoing era", PeriodInput{Name: "X", Color: "#112233", StartDate: "2030-01-01", EndDate: periodDate("2030-01-31")}, `overlaps era "Now" (2024-01-01 to ongoing)`},
		{"ongoing across a later era", PeriodInput{Name: "X", Color: "#112233", StartDate: "2022-01-01"}, `overlaps era "Now"`},
	} {
		_, err := svc.CreatePeriod(ctx, PeriodEra, tc.input)
		if apperror.Code(err) != apperror.CodeInvalidEra || !strings.Contains(apperror.Message(err), tc.message) {
			t.Errorf("%s: error = %v, want invalid_era containing %q", tc.name, err, tc.message)
		}
	}

	// The same range is free for the other kind, which reports its own code.
	if _, err := svc.CreatePeriod(ctx, PeriodDynasty, PeriodInput{Name: "", Color: "#112233", StartDate: "2020-01-01"}); apperror.Code(err) != apperror.CodeInvalidDynasty {
		t.Fatalf("blank dynasty name = %v, want invalid_dynasty", err)
	}
	// Adjacent days do not overlap.
	mustCreatePeriod(t, svc, PeriodEra, PeriodInput{Name: "Between", Color: "#112233", StartDate: "2021-01-01", EndDate: periodDate("2023-12-31")})
}

func TestPeriodPatchValidatesTheMergedResult(t *testing.T) {
	svc := newEntriesTestService(t, "e5")
	ctx := context.Background()
	first := mustCreatePeriod(t, svc, PeriodDynasty, PeriodInput{Name: "First", Color: "#112233", StartDate: "2020-01-01", EndDate: periodDate("2020-12-31")})
	second := mustCreatePeriod(t, svc, PeriodDynasty, PeriodInput{Name: "Second", Color: "#112233", StartDate: "2021-01-01", EndDate: periodDate("2021-12-31")})

	// Editing a period never conflicts with itself.
	sameName, color := "first", "#abcdef"
	patched, err := svc.PatchPeriod(ctx, PeriodDynasty, first.ID, PeriodPatch{Name: &sameName, Color: &color})
	if err != nil {
		t.Fatal(err)
	}
	if patched.Name != "first" || patched.Color != "#abcdef" || patched.StartDate != "2020-01-01" || !sameOptionalString(patched.EndDate, first.EndDate) {
		t.Fatalf("patched = %#v, want only name and color changed", patched)
	}

	taken := "SECOND"
	if _, err := svc.PatchPeriod(ctx, PeriodDynasty, first.ID, PeriodPatch{Name: &taken}); apperror.Code(err) != apperror.CodeInvalidDynasty || !strings.Contains(apperror.Message(err), `a dynasty named "Second" already exists`) {
		t.Fatalf("rename onto another dynasty = %v", err)
	}
	// Clearing the end makes First ongoing, which would run into Second.
	if _, err := svc.PatchPeriod(ctx, PeriodDynasty, first.ID, PeriodPatch{EndDate: OptionalEndDate{Set: true}}); apperror.Code(err) != apperror.CodeInvalidDynasty || !strings.Contains(apperror.Message(err), `overlaps dynasty "Second"`) {
		t.Fatalf("open-ending into the next dynasty = %v", err)
	}
	// A start moved past the stored end is judged against that stored end.
	late := "2020-12-31"
	early := "2021-06-01"
	if _, err := svc.PatchPeriod(ctx, PeriodDynasty, second.ID, PeriodPatch{StartDate: &late}); apperror.Code(err) != apperror.CodeInvalidDynasty {
		t.Fatalf("start onto the previous dynasty's last day = %v", err)
	}
	if _, err := svc.PatchPeriod(ctx, PeriodDynasty, first.ID, PeriodPatch{StartDate: &early}); apperror.Code(err) != apperror.CodeInvalidDynasty || !strings.Contains(apperror.Message(err), "end_date cannot be before start_date") {
		t.Fatalf("start after stored end = %v", err)
	}

	ongoing, err := svc.PatchPeriod(ctx, PeriodDynasty, second.ID, PeriodPatch{EndDate: OptionalEndDate{Set: true}})
	if err != nil || ongoing.EndDate != nil {
		t.Fatalf("make ongoing = %#v, %v", ongoing, err)
	}
	listed, err := svc.ListPeriods(ctx, PeriodDynasty)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ID != second.ID || listed[0].EndDate != nil || listed[1].Name != "first" {
		t.Fatalf("listed = %#v", listed)
	}
	if _, err := svc.PatchPeriod(ctx, PeriodDynasty, 999, PeriodPatch{Name: &sameName}); apperror.Code(err) != apperror.CodeDynastyNotFound {
		t.Fatalf("missing dynasty = %v, want dynasty_not_found", err)
	}
}

func TestGridDaysCarryEraAndDynastyIDs(t *testing.T) {
	habitToday = func() string { return "2026-03-10" }
	t.Cleanup(func() { habitToday = localToday })
	svc := newEntriesTestService(t, "f6")
	ctx := context.Background()

	spring := mustCreatePeriod(t, svc, PeriodEra, PeriodInput{Name: "Spring", Color: "#112233", StartDate: "2026-03-01", EndDate: periodDate("2026-03-31")})
	ongoingEra := mustCreatePeriod(t, svc, PeriodEra, PeriodInput{Name: "Later", Color: "#445566", StartDate: "2026-11-15"})
	dynasty := mustCreatePeriod(t, svc, PeriodDynasty, PeriodInput{Name: "Long", Color: "#778899", StartDate: "2024-05-01"})
	if _, err := svc.UpsertEntry(ctx, "2026-03-05", EntryPatch{Text: OptionalString{Set: true, Value: "inside spring"}}); err != nil {
		t.Fatal(err)
	}

	grid, err := svc.Grid(ctx, 2026, GridViewRating)
	if err != nil {
		t.Fatal(err)
	}
	days := make(map[string]GridDay, len(grid.Days))
	for _, day := range grid.Days {
		days[day.Date] = day
		// The dynasty began before this year and is ongoing: every day has it.
		if day.DynastyID == nil || *day.DynastyID != dynasty.ID {
			t.Fatalf("%s dynasty_id = %v, want %d", day.Date, day.DynastyID, dynasty.ID)
		}
	}
	for date, want := range map[string]*int64{
		"2026-02-28": nil,
		"2026-03-01": &spring.ID,     // first day, no entry
		"2026-03-05": &spring.ID,     // has an entry
		"2026-03-31": &spring.ID,     // last day, in the future
		"2026-04-01": nil,            // between eras
		"2026-11-14": nil,            // day before the ongoing era
		"2026-11-15": &ongoingEra.ID, // future start of the ongoing era
		"2026-12-31": &ongoingEra.ID, // ongoing runs through the year's end
	} {
		got := days[date].EraID
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("%s era_id = %v, want %v", date, optionalID(got), optionalID(want))
		}
	}

	// A year wholly after an ongoing period's start is covered end to end,
	// and a year before it is not covered at all.
	next, err := svc.Grid(ctx, 2027, GridViewRating)
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range next.Days {
		if day.EraID == nil || *day.EraID != ongoingEra.ID || day.DynastyID == nil {
			t.Fatalf("2027 day = %#v, want the ongoing era and dynasty", day)
		}
	}
	before, err := svc.Grid(ctx, 2023, GridViewRating)
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range before.Days {
		if day.EraID != nil || day.DynastyID != nil {
			t.Fatalf("2023 day = %#v, want no era or dynasty", day)
		}
	}
}

func mustCreatePeriod(t *testing.T, svc *Service, kind PeriodKind, input PeriodInput) Period {
	t.Helper()
	period, err := svc.CreatePeriod(context.Background(), kind, input)
	if err != nil {
		t.Fatalf("create %s %q: %v", kind, input.Name, err)
	}
	return period
}

func periodDate(value string) *string { return &value }

func optionalID(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
