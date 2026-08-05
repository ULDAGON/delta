package main

import (
	"context"
	"fmt"
	"slices"

	"github.com/ferriskleier/delta/internal/service"
)

// earliestActiveFrom returns the smallest date among the initial value and
// the given candidates.
func earliestActiveFrom(initial string, candidates []string) string {
	earliest := initial
	for _, candidate := range candidates {
		if candidate != "" && candidate < earliest {
			earliest = candidate
		}
	}
	return earliest
}

// runCleanupPrehistory removes the Duolingo checkoffs imported for days
// before the 2024-07-02 restructure — that single spreadsheet checkmark meant
// "did all habits", not Duolingo — and moves Duolingo's range start to the
// restructure date.
func runCleanupPrehistory(ctx context.Context, svc *service.Service) error {
	habits, err := svc.ListHabits(ctx)
	if err != nil {
		return err
	}
	var duolingo *service.Habit
	for index := range habits {
		if habits[index].Name == habitDuolingo {
			duolingo = &habits[index]
			break
		}
	}
	if duolingo == nil {
		return fmt.Errorf("habit %q not found", habitDuolingo)
	}

	entries, err := svc.ListEntries(ctx, "2023-01-01", "2024-07-01")
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%d", duolingo.ID)
	removed := 0
	for _, entry := range entries {
		if !slices.Contains(entry.Checkoffs, key) {
			continue
		}
		if _, err := svc.SetCheckoff(ctx, entry.Date, duolingo.ID, false); err != nil {
			return fmt.Errorf("%s: %w", entry.Date, err)
		}
		removed++
	}
	fmt.Printf("pre-restructure Duolingo checkoffs removed: %d\n", removed)

	activeTo := "2025-12-19"
	ranges := []service.HabitRange{{ActiveFrom: restructureDate, ActiveTo: &activeTo}}
	if _, err := svc.PatchHabit(ctx, duolingo.ID, service.HabitPatch{Ranges: &ranges}); err != nil {
		return fmt.Errorf("set Duolingo range: %w", err)
	}
	fmt.Printf("Duolingo range set to %s → %s\n", restructureDate, activeTo)
	return nil
}

// runMerge moves every checkoff of the duplicate habit onto the habit the
// user actually keeps, extends the kept habit's range to cover the
// duplicate's history, and deletes the now-empty duplicate.
func runMerge(ctx context.Context, svc *service.Service, fromID, toID int64) error {
	from, err := svc.GetHabit(ctx, fromID)
	if err != nil {
		return fmt.Errorf("duplicate habit %d: %w", fromID, err)
	}
	to, err := svc.GetHabit(ctx, toID)
	if err != nil {
		return fmt.Errorf("kept habit %d: %w", toID, err)
	}
	fmt.Printf("merging %q (id %d) into %q (id %d)\n", from.Name, from.ID, to.Name, to.ID)

	var candidates []string
	for _, habitRange := range slices.Concat(from.Ranges, to.Ranges) {
		candidates = append(candidates, habitRange.ActiveFrom)
	}
	activeFrom := earliestActiveFrom("9999-12-31", candidates)
	if activeFrom == "9999-12-31" {
		return fmt.Errorf("neither habit has an active range")
	}
	ranges := []service.HabitRange{{ActiveFrom: activeFrom}}
	if _, err := svc.PatchHabit(ctx, to.ID, service.HabitPatch{Ranges: &ranges}); err != nil {
		return fmt.Errorf("extend range of %q: %w", to.Name, err)
	}

	entries, err := svc.ListEntries(ctx, activeFrom, "9999-12-31")
	if err != nil {
		return err
	}
	fromKey := fmt.Sprintf("%d", from.ID)
	moved := 0
	for _, entry := range entries {
		if !slices.Contains(entry.Checkoffs, fromKey) {
			continue
		}
		if _, err := svc.SetCheckoff(ctx, entry.Date, to.ID, true); err != nil {
			return fmt.Errorf("%s: %w", entry.Date, err)
		}
		if _, err := svc.SetCheckoff(ctx, entry.Date, from.ID, false); err != nil {
			return fmt.Errorf("%s: %w", entry.Date, err)
		}
		moved++
	}
	fmt.Printf("checkoffs moved: %d\n", moved)

	tx, err := svc.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM habit_ranges WHERE habit_id = ?", from.ID); err != nil {
		return fmt.Errorf("delete duplicate ranges: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM habits WHERE id = ?", from.ID); err != nil {
		return fmt.Errorf("delete duplicate habit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	fmt.Printf("deleted duplicate habit %q (id %d)\n", from.Name, from.ID)
	return nil
}
