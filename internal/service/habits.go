package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ferriskleier/delta/internal/apperror"
)

// Habit is a named commitment whose active dates are derived from Ranges.
// Position is zero-based and contiguous in GET /api/habits responses.
type Habit struct {
	ID       int64        `json:"id"`
	Name     string       `json:"name"`
	Position int          `json:"position"`
	Ranges   []HabitRange `json:"ranges"`
}

type HabitRange struct {
	ActiveFrom string  `json:"active_from"`
	ActiveTo   *string `json:"active_to"`
}

// HabitPatch uses pointers so PATCH can distinguish an omitted field from a
// deliberately supplied zero value. Ranges replaces the complete range list.
type HabitPatch struct {
	Name     *string
	Position *int
	Ranges   *[]HabitRange
	Archived *bool
}

func (s *Service) CreateHabit(ctx context.Context, name string) (Habit, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Habit{}, apperror.New(apperror.CodeInvalidHabit, "habit name cannot be empty")
	}
	s.beforeWrite(ctx)
	today := serviceToday()
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Habit{}, fmt.Errorf("begin habit creation: %w", err)
	}
	defer tx.Rollback()
	var position int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(position), -1) + 1 FROM habits").Scan(&position); err != nil {
		return Habit{}, fmt.Errorf("find habit position: %w", err)
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO habits(name, position) VALUES (?, ?)", name, position)
	if err != nil {
		return Habit{}, fmt.Errorf("create habit: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Habit{}, fmt.Errorf("read habit id: %w", err)
	}
	if err := insertHabitRange(ctx, tx, id, HabitRange{ActiveFrom: today}); err != nil {
		return Habit{}, err
	}
	if err := tx.Commit(); err != nil {
		return Habit{}, fmt.Errorf("commit habit creation: %w", err)
	}
	return s.GetHabit(ctx, id)
}

func (s *Service) ListHabits(ctx context.Context) ([]Habit, error) {
	rows, err := s.Store.DB.QueryContext(ctx, "SELECT id, name, position FROM habits ORDER BY position, id")
	if err != nil {
		return nil, fmt.Errorf("list habits: %w", err)
	}
	var habits []Habit
	for rows.Next() {
		var habit Habit
		if err := rows.Scan(&habit.ID, &habit.Name, &habit.Position); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read habit: %w", err)
		}
		habits = append(habits, habit)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("list habits: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close habits: %w", err)
	}
	for index := range habits {
		ranges, err := s.habitRanges(ctx, habits[index].ID)
		if err != nil {
			return nil, err
		}
		habits[index].Ranges = ranges
	}
	return habits, nil
}

func (s *Service) GetHabit(ctx context.Context, id int64) (Habit, error) {
	if id <= 0 {
		return Habit{}, apperror.New(apperror.CodeHabitNotFound, "habit not found")
	}
	var habit Habit
	err := s.Store.DB.QueryRowContext(ctx, "SELECT id, name, position FROM habits WHERE id = ?", id).Scan(&habit.ID, &habit.Name, &habit.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return Habit{}, apperror.New(apperror.CodeHabitNotFound, "habit not found")
	}
	if err != nil {
		return Habit{}, fmt.Errorf("read habit: %w", err)
	}
	habit.Ranges, err = s.habitRanges(ctx, id)
	if err != nil {
		return Habit{}, err
	}
	return habit, nil
}

func (s *Service) PatchHabit(ctx context.Context, id int64, patch HabitPatch) (Habit, error) {
	if id <= 0 {
		return Habit{}, apperror.New(apperror.CodeHabitNotFound, "habit not found")
	}
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return Habit{}, apperror.New(apperror.CodeInvalidHabit, "habit name cannot be empty")
		}
		patch.Name = &name
	}
	if patch.Ranges != nil {
		ranges, err := normalizeHabitRanges(*patch.Ranges)
		if err != nil {
			return Habit{}, err
		}
		patch.Ranges = &ranges
	}

	s.beforeWrite(ctx)
	today := serviceToday()
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Habit{}, fmt.Errorf("begin habit update: %w", err)
	}
	defer tx.Rollback()
	var oldPosition int
	if err := tx.QueryRowContext(ctx, "SELECT position FROM habits WHERE id = ?", id).Scan(&oldPosition); errors.Is(err, sql.ErrNoRows) {
		return Habit{}, apperror.New(apperror.CodeHabitNotFound, "habit not found")
	} else if err != nil {
		return Habit{}, fmt.Errorf("read habit position: %w", err)
	}
	if patch.Name != nil {
		if _, err := tx.ExecContext(ctx, "UPDATE habits SET name = ? WHERE id = ?", *patch.Name, id); err != nil {
			return Habit{}, fmt.Errorf("rename habit: %w", err)
		}
	}
	if patch.Ranges != nil {
		// PATCH replaces ranges before applying archive state, so a combined
		// request archives against the new range set.
		if _, err := tx.ExecContext(ctx, "DELETE FROM habit_ranges WHERE habit_id = ?", id); err != nil {
			return Habit{}, fmt.Errorf("replace habit ranges: %w", err)
		}
		for _, habitRange := range *patch.Ranges {
			if err := insertHabitRange(ctx, tx, id, habitRange); err != nil {
				return Habit{}, err
			}
		}
	}
	if patch.Archived != nil {
		if err := applyArchiveState(ctx, tx, id, today, *patch.Archived); err != nil {
			return Habit{}, err
		}
	}
	if patch.Position != nil {
		if err := moveHabit(ctx, tx, id, oldPosition, *patch.Position); err != nil {
			return Habit{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Habit{}, fmt.Errorf("commit habit update: %w", err)
	}
	return s.GetHabit(ctx, id)
}

func (s *Service) SetCheckoff(ctx context.Context, date string, habitID int64, checked bool) (Entry, error) {
	if err := ValidateDate(date); err != nil {
		return Entry{}, err
	}
	if habitID <= 0 {
		return Entry{}, apperror.New(apperror.CodeHabitNotFound, "habit not found")
	}
	s.beforeWrite(ctx)
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, fmt.Errorf("begin check-off update: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM habits WHERE id = ?", habitID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return Entry{}, apperror.New(apperror.CodeHabitNotFound, "habit not found")
	} else if err != nil {
		return Entry{}, fmt.Errorf("read check-off habit: %w", err)
	}
	entry, err := scanEntry(ctx, tx, date)
	if errors.Is(err, sql.ErrNoRows) {
		entry = blankEntry(date)
		if !checked {
			if err := tx.Commit(); err != nil {
				return Entry{}, fmt.Errorf("commit idempotent check-off removal: %w", err)
			}
			return entry, nil
		}
	} else if err != nil {
		return Entry{}, fmt.Errorf("read entry before check-off: %w", err)
	}
	key := strconv.FormatInt(habitID, 10)
	entry.Checkoffs = updateCheckoffs(entry.Checkoffs, key, checked)
	if err := persistEntry(ctx, tx, entry, "checkoffs"); err != nil {
		return Entry{}, err
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, fmt.Errorf("commit check-off update: %w", err)
	}
	if err := s.filterCheckoffs(ctx, &entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func updateCheckoffs(checkoffs []string, key string, checked bool) []string {
	result := make([]string, 0, len(checkoffs)+1)
	found := false
	for _, checkoff := range checkoffs {
		if checkoff == key {
			found = true
			if checked {
				result = append(result, checkoff)
			}
			continue
		}
		result = append(result, checkoff)
	}
	if checked && !found {
		result = append(result, key)
	}
	return normalizedCheckoffs(result)
}

func (s *Service) habitRanges(ctx context.Context, id int64) ([]HabitRange, error) {
	rows, err := s.Store.DB.QueryContext(ctx, "SELECT active_from, active_to FROM habit_ranges WHERE habit_id = ? ORDER BY active_from, id", id)
	if err != nil {
		return nil, fmt.Errorf("list habit ranges: %w", err)
	}
	defer rows.Close()
	ranges := make([]HabitRange, 0)
	for rows.Next() {
		var habitRange HabitRange
		var activeTo sql.NullString
		if err := rows.Scan(&habitRange.ActiveFrom, &activeTo); err != nil {
			return nil, fmt.Errorf("read habit range: %w", err)
		}
		if activeTo.Valid {
			value := activeTo.String
			habitRange.ActiveTo = &value
		}
		ranges = append(ranges, habitRange)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list habit ranges: %w", err)
	}
	return ranges, nil
}

func insertHabitRange(ctx context.Context, tx sqlExecer, habitID int64, habitRange HabitRange) error {
	if err := validateHabitRange(habitRange); err != nil {
		return err
	}
	var activeTo any
	if habitRange.ActiveTo != nil {
		activeTo = *habitRange.ActiveTo
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO habit_ranges(habit_id, active_from, active_to) VALUES (?, ?, ?)", habitID, habitRange.ActiveFrom, activeTo); err != nil {
		return fmt.Errorf("write habit range: %w", err)
	}
	return nil
}

func validateHabitRange(habitRange HabitRange) error {
	if err := ValidateDate(habitRange.ActiveFrom); err != nil {
		return apperror.New(apperror.CodeInvalidHabit, "active_from must be a real calendar date in YYYY-MM-DD format")
	}
	if habitRange.ActiveTo != nil {
		if err := ValidateDate(*habitRange.ActiveTo); err != nil {
			return apperror.New(apperror.CodeInvalidHabit, "active_to must be a real calendar date in YYYY-MM-DD format")
		}
		if habitRange.ActiveFrom > *habitRange.ActiveTo {
			return apperror.New(apperror.CodeInvalidHabit, "active_to cannot be before active_from")
		}
	}
	return nil
}

func normalizeHabitRanges(ranges []HabitRange) ([]HabitRange, error) {
	if len(ranges) == 0 {
		return nil, apperror.New(apperror.CodeInvalidHabit, "habit must have at least one validity range")
	}
	result := append([]HabitRange(nil), ranges...)
	for _, habitRange := range result {
		if err := validateHabitRange(habitRange); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].ActiveFrom != result[j].ActiveFrom {
			return result[i].ActiveFrom < result[j].ActiveFrom
		}
		return rangeEnd(result[i]) < rangeEnd(result[j])
	})
	for index := 1; index < len(result); index++ {
		previous, current := result[index-1], result[index]
		if previous.ActiveTo == nil || current.ActiveFrom <= *previous.ActiveTo {
			return nil, apperror.New(apperror.CodeInvalidHabit, "habit validity ranges cannot overlap")
		}
	}
	return result, nil
}

func rangeEnd(habitRange HabitRange) string {
	if habitRange.ActiveTo == nil {
		return "9999-12-31"
	}
	return *habitRange.ActiveTo
}

func applyArchiveState(ctx context.Context, tx *sql.Tx, id int64, today string, archived bool) error {
	if archived {
		var rangeID int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM habit_ranges WHERE habit_id = ? AND active_from <= ? AND (active_to IS NULL OR active_to >= ?) ORDER BY active_from DESC, id DESC LIMIT 1", id, today, today).Scan(&rangeID)
		if errors.Is(err, sql.ErrNoRows) {
			return apperror.New(apperror.CodeHabitNotActive, "habit is not active today")
		}
		if err != nil {
			return fmt.Errorf("find active habit range: %w", err)
		}
		// Intentional reducer deviation: archiving also closes a finite range
		// that is currently active, even when its requested end is later.
		if _, err := tx.ExecContext(ctx, "UPDATE habit_ranges SET active_to = ? WHERE id = ?", today, rangeID); err != nil {
			return fmt.Errorf("archive habit: %w", err)
		}
		return nil
	}

	var endedToday int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM habit_ranges WHERE habit_id = ? AND active_to = ? ORDER BY id DESC LIMIT 1", id, today).Scan(&endedToday)
	if err == nil {
		if _, err := tx.ExecContext(ctx, "UPDATE habit_ranges SET active_to = NULL WHERE id = ?", endedToday); err != nil {
			return fmt.Errorf("reopen habit range: %w", err)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("find today's ended habit range: %w", err)
	}
	var openRange int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM habit_ranges WHERE habit_id = ? AND active_to IS NULL LIMIT 1", id).Scan(&openRange)
	if err == nil {
		// Intentional reducer deviation: resume is a no-op when an open range
		// already exists, preserving the existing range identity and dates.
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("find open habit range: %w", err)
	}
	return insertHabitRange(ctx, tx, id, HabitRange{ActiveFrom: today})
}

func moveHabit(ctx context.Context, tx *sql.Tx, id int64, oldPosition, requested int) error {
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM habits").Scan(&count); err != nil {
		return fmt.Errorf("count habits: %w", err)
	}
	if count == 0 {
		return nil
	}
	position := requested
	if position < 0 {
		position = 0
	}
	if position >= count {
		position = count - 1
	}
	if position == oldPosition {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE habits SET position = -1 WHERE id = ?", id); err != nil {
		return fmt.Errorf("hold habit position: %w", err)
	}
	if position < oldPosition {
		if _, err := tx.ExecContext(ctx, "UPDATE habits SET position = position + 1 WHERE id <> ? AND position >= ? AND position < ?", id, position, oldPosition); err != nil {
			return fmt.Errorf("shift habits down: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, "UPDATE habits SET position = position - 1 WHERE id <> ? AND position > ? AND position <= ?", id, oldPosition, position); err != nil {
			return fmt.Errorf("shift habits up: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE habits SET position = ? WHERE id = ?", position, id); err != nil {
		return fmt.Errorf("set habit position: %w", err)
	}
	return nil
}

type habitSchedule struct {
	ID     string
	Ranges []HabitRange
}

func (s *Service) habitSchedules(ctx context.Context) ([]habitSchedule, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `
		SELECT h.id, r.active_from, r.active_to
		FROM habits h
		JOIN habit_ranges r ON r.habit_id = h.id
		ORDER BY h.position, h.id, r.active_from, r.id`)
	if err != nil {
		return nil, fmt.Errorf("list habit schedules: %w", err)
	}
	defer rows.Close()
	schedules := make([]habitSchedule, 0)
	byID := make(map[string]int)
	for rows.Next() {
		var id int64
		var habitRange HabitRange
		var activeTo sql.NullString
		if err := rows.Scan(&id, &habitRange.ActiveFrom, &activeTo); err != nil {
			return nil, fmt.Errorf("read habit schedule: %w", err)
		}
		if activeTo.Valid {
			value := activeTo.String
			habitRange.ActiveTo = &value
		}
		key := strconv.FormatInt(id, 10)
		index, ok := byID[key]
		if !ok {
			index = len(schedules)
			byID[key] = index
			schedules = append(schedules, habitSchedule{ID: key})
		}
		schedules[index].Ranges = append(schedules[index].Ranges, habitRange)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list habit schedules: %w", err)
	}
	return schedules, nil
}

func activeHabitIDsAt(schedules []habitSchedule, date string) map[string]struct{} {
	active := make(map[string]struct{}, len(schedules))
	for _, schedule := range schedules {
		for _, habitRange := range schedule.Ranges {
			if habitRangeActive(habitRange, date) {
				active[schedule.ID] = struct{}{}
				break
			}
		}
	}
	return active
}

func habitRangeActive(habitRange HabitRange, date string) bool {
	return habitRange.ActiveFrom <= date && (habitRange.ActiveTo == nil || *habitRange.ActiveTo >= date)
}

func (s *Service) activeHabitIDs(ctx context.Context, date string) (map[string]struct{}, error) {
	schedules, err := s.habitSchedules(ctx)
	if err != nil {
		return nil, err
	}
	return activeHabitIDsAt(schedules, date), nil
}

func (s *Service) filterCheckoffs(ctx context.Context, entry *Entry) error {
	active, err := s.activeHabitIDs(ctx, entry.Date)
	if err != nil {
		return err
	}
	entry.Checkoffs = CalculateDailyHabitScore(entry.Checkoffs, active).VisibleCheckoffs
	return nil
}
