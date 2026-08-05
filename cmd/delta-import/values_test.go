package main

import "testing"

// The spreadsheet's checkbox columns never moved, but on 2025-12-20 the
// header names shifted left when Duolingo was removed. Column meaning is
// therefore date-dependent.
func TestColumnHabitsBeforeHeaderShift(t *testing.T) {
	m := columnHabits("2025-12-19")
	if m[13] != habitDuolingo {
		t.Errorf("col 13 = %q, want Duolingo", m[13])
	}
	if m[25] != habitShowerCold {
		t.Errorf("col 25 = %q, want Shower cold", m[25])
	}
	if m[14] != habitNoScreen {
		t.Errorf("col 14 = %q, want No screen", m[14])
	}
}

func TestColumnHabitsAfterHeaderShift(t *testing.T) {
	m := columnHabits("2025-12-20")
	if m[13] != habitNoScreen {
		t.Errorf("col 13 = %q, want No screen", m[13])
	}
	if m[25] != habitRosetta {
		t.Errorf("col 25 = %q, want ROSETTA", m[25])
	}
	if _, ok := m[26]; ok {
		t.Error("col 26 must not map to a habit")
	}
}

// Before the 2024-07-02 restructure the single checkbox meant "did all
// habits", which names no specific habit — those days import no checkoffs.
func TestColumnHabitsEmptyBeforeRestructure(t *testing.T) {
	if m := columnHabits("2024-07-01"); len(m) != 0 {
		t.Errorf("pre-restructure map = %v, want empty", m)
	}
	if m := columnHabits("2024-07-02"); m[12] != habitNoFap {
		t.Errorf("restructure day col 12 = %q, want NoFap", m[12])
	}
}

func TestEarliestActiveFrom(t *testing.T) {
	from := earliestActiveFrom("2026-08-02", []string{"2024-07-02", "2025-05-25"})
	if from != "2024-07-02" {
		t.Errorf("earliest = %q, want 2024-07-02", from)
	}
	if got := earliestActiveFrom("2024-01-01", nil); got != "2024-01-01" {
		t.Errorf("earliest with no others = %q, want 2024-01-01", got)
	}
}
