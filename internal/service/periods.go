package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ferriskleier/delta/internal/apperror"
)

// PeriodKind separates eras from dynasties. The two kinds share one shape and
// one table but never interact: names and ranges are only compared within a
// kind.
type PeriodKind string

const (
	PeriodEra     PeriodKind = "era"
	PeriodDynasty PeriodKind = "dynasty"
)

// Period is one era or dynasty: a named, coloured date range of the user's
// life. Both dates are inclusive; a nil EndDate means the period is ongoing.
type Period struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Color     string  `json:"color"`
	StartDate string  `json:"start_date"`
	EndDate   *string `json:"end_date"`
}

// PeriodInput is the complete field set required to create a period.
type PeriodInput struct {
	Name      string
	Color     string
	StartDate string
	EndDate   *string
}

// OptionalEndDate preserves whether a PATCH included end_date. A null value
// makes the period ongoing; an omitted field leaves the stored end alone.
type OptionalEndDate struct {
	Set   bool
	Value *string
}

// PeriodPatch uses pointers so PATCH can distinguish an omitted field from a
// deliberately supplied zero value.
type PeriodPatch struct {
	Name      *string
	Color     *string
	StartDate *string
	EndDate   OptionalEndDate
}

var periodColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (kind PeriodKind) valid() bool { return kind == PeriodEra || kind == PeriodDynasty }

func (kind PeriodKind) invalidCode() string {
	if kind == PeriodDynasty {
		return apperror.CodeInvalidDynasty
	}
	return apperror.CodeInvalidEra
}

func (kind PeriodKind) notFound() error {
	if kind == PeriodDynasty {
		return apperror.New(apperror.CodeDynastyNotFound, "dynasty not found")
	}
	return apperror.New(apperror.CodeEraNotFound, "era not found")
}

// withArticle is the kind as it reads mid-sentence: "an era", "a dynasty".
func (kind PeriodKind) withArticle() string {
	if kind == PeriodDynasty {
		return "a dynasty"
	}
	return "an era"
}

// ListPeriods returns every period of one kind, newest start first.
func (s *Service) ListPeriods(ctx context.Context, kind PeriodKind) ([]Period, error) {
	if !kind.valid() {
		return nil, fmt.Errorf("unknown period kind %q", kind)
	}
	return listPeriods(ctx, s.Store.DB, kind)
}

func (s *Service) CreatePeriod(ctx context.Context, kind PeriodKind, input PeriodInput) (Period, error) {
	if !kind.valid() {
		return Period{}, fmt.Errorf("unknown period kind %q", kind)
	}
	period, err := normalizePeriod(kind, Period{Name: input.Name, Color: input.Color, StartDate: input.StartDate, EndDate: input.EndDate})
	if err != nil {
		return Period{}, err
	}
	s.beforeWrite(ctx)
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Period{}, fmt.Errorf("begin %s creation: %w", kind, err)
	}
	defer tx.Rollback()
	if err := checkPeriodConflicts(ctx, tx, kind, period); err != nil {
		return Period{}, err
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO periods(kind, name, color, start_date, end_date) VALUES (?, ?, ?, ?, ?)",
		string(kind), period.Name, period.Color, period.StartDate, nullableDate(period.EndDate))
	if err != nil {
		return Period{}, fmt.Errorf("create %s: %w", kind, err)
	}
	if period.ID, err = result.LastInsertId(); err != nil {
		return Period{}, fmt.Errorf("read %s id: %w", kind, err)
	}
	if err := tx.Commit(); err != nil {
		return Period{}, fmt.Errorf("commit %s creation: %w", kind, err)
	}
	return period, nil
}

// PatchPeriod applies the supplied fields and validates the merged result, so
// a patch that moves only one date is still checked against the other.
func (s *Service) PatchPeriod(ctx context.Context, kind PeriodKind, id int64, patch PeriodPatch) (Period, error) {
	if !kind.valid() {
		return Period{}, fmt.Errorf("unknown period kind %q", kind)
	}
	if id <= 0 {
		return Period{}, kind.notFound()
	}
	s.beforeWrite(ctx)
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Period{}, fmt.Errorf("begin %s update: %w", kind, err)
	}
	defer tx.Rollback()
	period := Period{ID: id}
	var endDate sql.NullString
	// The kind is part of the lookup: an era ID never addresses a dynasty.
	err = tx.QueryRowContext(ctx, "SELECT name, color, start_date, end_date FROM periods WHERE id = ? AND kind = ?", id, string(kind)).
		Scan(&period.Name, &period.Color, &period.StartDate, &endDate)
	if errors.Is(err, sql.ErrNoRows) {
		return Period{}, kind.notFound()
	}
	if err != nil {
		return Period{}, fmt.Errorf("read %s: %w", kind, err)
	}
	if endDate.Valid {
		period.EndDate = &endDate.String
	}
	if patch.Name != nil {
		period.Name = *patch.Name
	}
	if patch.Color != nil {
		period.Color = *patch.Color
	}
	if patch.StartDate != nil {
		period.StartDate = *patch.StartDate
	}
	if patch.EndDate.Set {
		period.EndDate = patch.EndDate.Value
	}
	if period, err = normalizePeriod(kind, period); err != nil {
		return Period{}, err
	}
	if err := checkPeriodConflicts(ctx, tx, kind, period); err != nil {
		return Period{}, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE periods SET name = ?, color = ?, start_date = ?, end_date = ? WHERE id = ?",
		period.Name, period.Color, period.StartDate, nullableDate(period.EndDate), id); err != nil {
		return Period{}, fmt.Errorf("update %s: %w", kind, err)
	}
	if err := tx.Commit(); err != nil {
		return Period{}, fmt.Errorf("commit %s update: %w", kind, err)
	}
	return period, nil
}

func (s *Service) DeletePeriod(ctx context.Context, kind PeriodKind, id int64) error {
	if !kind.valid() {
		return fmt.Errorf("unknown period kind %q", kind)
	}
	if id <= 0 {
		return kind.notFound()
	}
	s.beforeWrite(ctx)
	result, err := s.Store.DB.ExecContext(ctx, "DELETE FROM periods WHERE id = ? AND kind = ?", id, string(kind))
	if err != nil {
		return fmt.Errorf("delete %s: %w", kind, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deleted %s: %w", kind, err)
	}
	if count == 0 {
		return kind.notFound()
	}
	return nil
}

type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listPeriods(ctx context.Context, db sqlQueryer, kind PeriodKind) ([]Period, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, name, color, start_date, end_date FROM periods WHERE kind = ? ORDER BY start_date DESC, id DESC", string(kind))
	if err != nil {
		return nil, fmt.Errorf("list %s periods: %w", kind, err)
	}
	defer rows.Close()
	periods := make([]Period, 0)
	for rows.Next() {
		var period Period
		var endDate sql.NullString
		if err := rows.Scan(&period.ID, &period.Name, &period.Color, &period.StartDate, &endDate); err != nil {
			return nil, fmt.Errorf("read %s: %w", kind, err)
		}
		if endDate.Valid {
			value := endDate.String
			period.EndDate = &value
		}
		periods = append(periods, period)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list %s periods: %w", kind, err)
	}
	return periods, nil
}

// normalizePeriod trims the name and validates every field that can be judged
// without looking at other periods.
func normalizePeriod(kind PeriodKind, period Period) (Period, error) {
	code := kind.invalidCode()
	period.Name = strings.TrimSpace(period.Name)
	if period.Name == "" {
		return Period{}, apperror.New(code, fmt.Sprintf("%s name cannot be empty", kind))
	}
	if !periodColorPattern.MatchString(period.Color) {
		return Period{}, apperror.New(code, "color must be a hex color in #rrggbb format")
	}
	if err := ValidateDate(period.StartDate); err != nil {
		return Period{}, apperror.New(code, "start_date must be a real calendar date in YYYY-MM-DD format")
	}
	if period.EndDate != nil {
		if err := ValidateDate(*period.EndDate); err != nil {
			return Period{}, apperror.New(code, "end_date must be a real calendar date in YYYY-MM-DD format, or null while ongoing")
		}
		if *period.EndDate < period.StartDate {
			return Period{}, apperror.New(code, "end_date cannot be before start_date")
		}
	}
	return period, nil
}

// checkPeriodConflicts rejects a duplicate name or an overlapping range among
// the other periods of the same kind. candidate.ID is zero for a new period
// and otherwise excludes the period being edited from the comparison.
func checkPeriodConflicts(ctx context.Context, tx *sql.Tx, kind PeriodKind, candidate Period) error {
	others, err := listPeriods(ctx, tx, kind)
	if err != nil {
		return err
	}
	for _, other := range others {
		if other.ID == candidate.ID {
			continue
		}
		if strings.EqualFold(other.Name, candidate.Name) {
			return apperror.New(kind.invalidCode(), fmt.Sprintf("%s named %q already exists", kind.withArticle(), other.Name))
		}
	}
	for _, other := range others {
		if other.ID == candidate.ID {
			continue
		}
		// Inclusive ranges overlap when each starts no later than the other
		// ends; an ongoing period has no end to start after.
		if candidate.StartDate <= periodEnd(other) && other.StartDate <= periodEnd(candidate) {
			return apperror.New(kind.invalidCode(), fmt.Sprintf("%s to %s overlaps %s %q (%s to %s)",
				candidate.StartDate, periodEndLabel(candidate), kind, other.Name, other.StartDate, periodEndLabel(other)))
		}
	}
	return nil
}

func periodEnd(period Period) string {
	if period.EndDate == nil {
		return "9999-12-31"
	}
	return *period.EndDate
}

func periodEndLabel(period Period) string {
	if period.EndDate == nil {
		return "ongoing"
	}
	return *period.EndDate
}

func nullableDate(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// periodIDAt returns the period covering date, or nil when none does. Periods
// of one kind never overlap, so at most one can match.
func periodIDAt(periods []Period, date string) *int64 {
	for _, period := range periods {
		if period.StartDate <= date && date <= periodEnd(period) {
			id := period.ID
			return &id
		}
	}
	return nil
}
