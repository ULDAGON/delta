package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ferriskleier/delta/internal/apperror"
)

var datePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

type Goal struct {
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
}

type Ws struct {
	WentWell            string `json:"went_well"`
	CouldHaveGoneBetter string `json:"could_have_gone_better"`
	GoalForTomorrow     string `json:"goal_for_tomorrow"`
}

type Ratings struct {
	Total  *int `json:"total,omitempty"`
	Body   *int `json:"body,omitempty"`
	Mind   *int `json:"mind,omitempty"`
	Spirit *int `json:"spirit,omitempty"`
}

type Entry struct {
	Date       string   `json:"date"`
	Text       string   `json:"text"`
	Goals      []Goal   `json:"goals"`
	Gratitudes []string `json:"gratitudes"`
	Ws         Ws       `json:"ws"`
	Ratings    Ratings  `json:"ratings"`
	// Checkoffs contains stable, stringified habit IDs; reads only filter them
	// by the habit's range for this date and never rewrite them as names.
	Checkoffs []string `json:"checkoffs"`
	// Pixel is the per-entry marker: 0 grey, 1 green, 2 orange.
	Pixel int `json:"pixel"`
	// WorkHours is the optional hours-worked value for the day. An absent
	// value is omitted from JSON and is never the same as a recorded 0.
	WorkHours *float64 `json:"work_hours,omitempty"`
}

// WorkHoursMax is the inclusive upper bound for one day's recorded work hours.
const WorkHoursMax = 24

// ValidateWorkHours accepts any decimal from 0 to 24 inclusive. The REST
// decoder shares it so both seams reject exactly the same values.
func ValidateWorkHours(value float64) error {
	if math.IsNaN(value) || value < 0 || value > WorkHoursMax {
		return apperror.New(apperror.CodeInvalidEntry, "work hours must be between 0 and 24")
	}
	return nil
}

// OptionalString and OptionalRating preserve whether a partial PUT included a
// field. A null rating clears that one rating without touching its siblings.
type OptionalString struct {
	Set   bool
	Value string
}

type OptionalRating struct {
	Set   bool
	Value *int
}

// OptionalPixel preserves whether a partial PUT included the pixel marker, so
// a request that sets it to 0 is not confused with an omitted field.
type OptionalPixel struct {
	Set   bool
	Value int
}

// OptionalWorkHours preserves whether a partial PUT included work hours. A
// null value clears them; an omitted field leaves the stored value alone.
type OptionalWorkHours struct {
	Set   bool
	Value *float64
}

type WsPatch struct {
	WentWell            OptionalString
	CouldHaveGoneBetter OptionalString
	GoalForTomorrow     OptionalString
}

type RatingsPatch struct {
	Total  OptionalRating
	Body   OptionalRating
	Mind   OptionalRating
	Spirit OptionalRating
}

type EntryPatch struct {
	Text          OptionalString
	GoalsSet      bool
	Goals         []Goal
	GratitudesSet bool
	Gratitudes    []string
	Ws            WsPatch
	Ratings       RatingsPatch
	Pixel         OptionalPixel
	WorkHours     OptionalWorkHours
}

func ValidateDate(value string) error {
	if !datePattern.MatchString(value) {
		return apperror.New(apperror.CodeInvalidDate, "date must be a real calendar date in YYYY-MM-DD format")
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value || parsed.Year() < 1 {
		return apperror.New(apperror.CodeInvalidDate, "date must be a real calendar date in YYYY-MM-DD format")
	}
	return nil
}

func (s *Service) GetEntry(ctx context.Context, date string) (Entry, error) {
	if err := ValidateDate(date); err != nil {
		return Entry{}, err
	}
	entry, err := scanEntry(ctx, s.Store.DB, date)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, apperror.New(apperror.CodeEntryNotFound, "entry not found")
	}
	if err != nil {
		return Entry{}, fmt.Errorf("read entry: %w", err)
	}
	if err := s.filterCheckoffs(ctx, &entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (s *Service) ListEntries(ctx context.Context, from, to string) ([]Entry, error) {
	entries, err := s.listEntries(ctx, from, to)
	if err != nil {
		return nil, err
	}
	schedules, err := s.habitSchedules(ctx)
	if err != nil {
		return nil, err
	}
	for index := range entries {
		active := activeHabitIDsAt(schedules, entries[index].Date)
		entries[index].Checkoffs = CalculateDailyHabitScore(entries[index].Checkoffs, active).VisibleCheckoffs
	}
	return entries, nil
}

// EntryDate is the date-only projection of one entry, for clients that need
// the calendar of existing entries rather than their contents.
type EntryDate struct {
	Date string `json:"date"`
}

// ListEntryDates lists the dates of existing entries with the same validation,
// range filtering, and ordering as ListEntries, reading no other column.
func (s *Service) ListEntryDates(ctx context.Context, from, to string) ([]EntryDate, error) {
	query, args, err := entryRangeQuery("date", from, to)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list entry dates: %w", err)
	}
	defer rows.Close()
	dates := make([]EntryDate, 0)
	for rows.Next() {
		var date EntryDate
		if err := rows.Scan(&date.Date); err != nil {
			return nil, fmt.Errorf("read entry date row: %w", err)
		}
		dates = append(dates, date)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list entry dates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close entry dates: %w", err)
	}
	return dates, nil
}

// entryRangeQuery builds the range-filtered entries query shared by the full
// and date-only reads, so both accept and order exactly the same requests.
func entryRangeQuery(columns, from, to string) (string, []any, error) {
	if from != "" {
		if err := ValidateDate(from); err != nil {
			return "", nil, err
		}
	}
	if to != "" {
		if err := ValidateDate(to); err != nil {
			return "", nil, err
		}
	}
	if from != "" && to != "" && from > to {
		return "", nil, apperror.New(apperror.CodeInvalidDate, "from date must not be after to date")
	}

	query := `SELECT ` + columns + ` FROM entries`
	args := make([]any, 0, 2)
	filters := make([]string, 0, 2)
	if from != "" {
		filters = append(filters, "date >= ?")
		args = append(args, from)
	}
	if to != "" {
		filters = append(filters, "date <= ?")
		args = append(args, to)
	}
	if len(filters) > 0 {
		query += " WHERE " + strings.Join(filters, " AND ")
	}
	return query + " ORDER BY date", args, nil
}

func (s *Service) listEntries(ctx context.Context, from, to string) ([]Entry, error) {
	query, args, err := entryRangeQuery(entryColumns, from, to)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()
	entries := make([]Entry, 0)
	for rows.Next() {
		entry, err := scanEntryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("read entry row: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close entries: %w", err)
	}
	return entries, nil
}

func (s *Service) UpsertEntry(ctx context.Context, date string, patch EntryPatch) (Entry, error) {
	if err := ValidateDate(date); err != nil {
		return Entry{}, err
	}
	s.beforeWrite(ctx)
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, fmt.Errorf("begin entry update: %w", err)
	}
	defer tx.Rollback()

	entry, err := scanEntry(ctx, tx, date)
	if errors.Is(err, sql.ErrNoRows) {
		entry = blankEntry(date)
	} else if err != nil {
		return Entry{}, fmt.Errorf("read entry before update: %w", err)
	}
	if err := applyPatch(&entry, patch); err != nil {
		return Entry{}, err
	}
	if err := persistEntry(ctx, tx, entry, entryUpdateColumns(patch)...); err != nil {
		return Entry{}, err
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, fmt.Errorf("commit entry: %w", err)
	}
	if err := s.filterCheckoffs(ctx, &entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func persistEntry(ctx context.Context, writer sqlExecer, entry Entry, updateColumns ...string) error {
	query := `
		INSERT INTO entries (
			date, text,
			goal1_text, goal1_checked, goal2_text, goal2_checked,
			goal3_text, goal3_checked, goal4_text, goal4_checked,
			goal5_text, goal5_checked,
			gratitude1, gratitude2, gratitude3,
			went_well, could_have_gone_better, goal_for_tomorrow,
			rating_total, rating_body, rating_mind, rating_spirit, checkoffs,
			pixel, work_hours
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if len(updateColumns) == 0 {
		query += ` ON CONFLICT(date) DO NOTHING`
	} else {
		assignments := make([]string, 0, len(updateColumns))
		for _, column := range updateColumns {
			assignments = append(assignments, column+"=excluded."+column)
		}
		query += " ON CONFLICT(date) DO UPDATE SET " + strings.Join(assignments, ", ")
	}
	if _, err := writer.ExecContext(ctx, query, entryValues(entry)...); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}
	return nil
}

func entryUpdateColumns(patch EntryPatch) []string {
	columns := make([]string, 0, 21)
	if patch.Text.Set {
		columns = append(columns, "text")
	}
	if patch.GoalsSet {
		for index := 1; index <= 5; index++ {
			columns = append(columns, fmt.Sprintf("goal%d_text", index), fmt.Sprintf("goal%d_checked", index))
		}
	}
	if patch.GratitudesSet {
		columns = append(columns, "gratitude1", "gratitude2", "gratitude3")
	}
	if patch.Ws.WentWell.Set {
		columns = append(columns, "went_well")
	}
	if patch.Ws.CouldHaveGoneBetter.Set {
		columns = append(columns, "could_have_gone_better")
	}
	if patch.Ws.GoalForTomorrow.Set {
		columns = append(columns, "goal_for_tomorrow")
	}
	if patch.Ratings.Total.Set {
		columns = append(columns, "rating_total")
	}
	if patch.Ratings.Body.Set {
		columns = append(columns, "rating_body")
	}
	if patch.Ratings.Mind.Set {
		columns = append(columns, "rating_mind")
	}
	if patch.Ratings.Spirit.Set {
		columns = append(columns, "rating_spirit")
	}
	if patch.Pixel.Set {
		columns = append(columns, "pixel")
	}
	if patch.WorkHours.Set {
		columns = append(columns, "work_hours")
	}
	return columns
}

func (s *Service) DeleteEntry(ctx context.Context, date string) error {
	if err := ValidateDate(date); err != nil {
		return err
	}
	s.beforeWrite(ctx)
	result, err := s.Store.DB.ExecContext(ctx, "DELETE FROM entries WHERE date = ?", date)
	if err != nil {
		return fmt.Errorf("delete entry: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deleted entry: %w", err)
	}
	if count == 0 {
		return apperror.New(apperror.CodeEntryNotFound, "entry not found")
	}
	return nil
}

const entryColumns = `date, text,
 goal1_text, goal1_checked, goal2_text, goal2_checked,
 goal3_text, goal3_checked, goal4_text, goal4_checked,
 goal5_text, goal5_checked,
 gratitude1, gratitude2, gratitude3,
 went_well, could_have_gone_better, goal_for_tomorrow,
 rating_total, rating_body, rating_mind, rating_spirit, checkoffs, pixel, work_hours`

type rowScanner interface {
	Scan(dest ...any) error
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scanEntry(ctx context.Context, query queryRower, date string) (Entry, error) {
	return scanEntryRow(query.QueryRowContext(ctx, "SELECT "+entryColumns+" FROM entries WHERE date = ?", date))
}

func scanEntryRow(row rowScanner) (Entry, error) {
	var entry Entry
	entry.Goals = make([]Goal, 5)
	entry.Gratitudes = make([]string, 3)
	entry.Checkoffs = make([]string, 0)
	var checked [5]int
	var total, body, mind, spirit sql.NullInt64
	var checkoffs string
	var pixel int
	var workHours sql.NullFloat64
	err := row.Scan(
		&entry.Date, &entry.Text,
		&entry.Goals[0].Text, &checked[0], &entry.Goals[1].Text, &checked[1],
		&entry.Goals[2].Text, &checked[2], &entry.Goals[3].Text, &checked[3],
		&entry.Goals[4].Text, &checked[4],
		&entry.Gratitudes[0], &entry.Gratitudes[1], &entry.Gratitudes[2],
		&entry.Ws.WentWell, &entry.Ws.CouldHaveGoneBetter, &entry.Ws.GoalForTomorrow,
		&total, &body, &mind, &spirit, &checkoffs, &pixel, &workHours)
	if err != nil {
		return Entry{}, err
	}
	for i := range entry.Goals {
		entry.Goals[i].Checked = checked[i] != 0
	}
	entry.Ratings = Ratings{
		Total: nullRating(total), Body: nullRating(body), Mind: nullRating(mind), Spirit: nullRating(spirit),
	}
	if checkoffs != "" {
		if err := json.Unmarshal([]byte(checkoffs), &entry.Checkoffs); err != nil {
			return Entry{}, fmt.Errorf("decode entry checkoffs: %w", err)
		}
	}
	if entry.Checkoffs == nil {
		entry.Checkoffs = make([]string, 0)
	}
	entry.Pixel = pixel
	entry.WorkHours = nullWorkHours(workHours)
	return entry, nil
}

func nullRating(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}

func nullWorkHours(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}

func blankEntry(date string) Entry {
	return Entry{Date: date, Goals: make([]Goal, 5), Gratitudes: make([]string, 3), Checkoffs: make([]string, 0)}
}

func entryValues(entry Entry) []any {
	goals := normalizedGoals(entry.Goals)
	gratitudes := normalizedGratitudes(entry.Gratitudes)
	checkoffs, _ := json.Marshal(normalizedCheckoffs(entry.Checkoffs))
	return []any{
		entry.Date, entry.Text,
		goals[0].Text, boolInt(goals[0].Checked), goals[1].Text, boolInt(goals[1].Checked),
		goals[2].Text, boolInt(goals[2].Checked), goals[3].Text, boolInt(goals[3].Checked),
		goals[4].Text, boolInt(goals[4].Checked),
		gratitudes[0], gratitudes[1], gratitudes[2],
		entry.Ws.WentWell, entry.Ws.CouldHaveGoneBetter, entry.Ws.GoalForTomorrow,
		ratingValue(entry.Ratings.Total), ratingValue(entry.Ratings.Body), ratingValue(entry.Ratings.Mind), ratingValue(entry.Ratings.Spirit), string(checkoffs),
		entry.Pixel, workHoursValue(entry.WorkHours),
	}
}

func applyPatch(entry *Entry, patch EntryPatch) error {
	if patch.Text.Set {
		entry.Text = patch.Text.Value
	}
	if patch.GoalsSet {
		if len(patch.Goals) != 5 {
			return apperror.New(apperror.CodeInvalidEntry, "goals must contain exactly five lines")
		}
		entry.Goals = append([]Goal(nil), patch.Goals...)
	}
	if patch.GratitudesSet {
		if len(patch.Gratitudes) != 3 {
			return apperror.New(apperror.CodeInvalidEntry, "gratitudes must contain exactly three lines")
		}
		entry.Gratitudes = append([]string(nil), patch.Gratitudes...)
	}
	if patch.Ws.WentWell.Set {
		entry.Ws.WentWell = patch.Ws.WentWell.Value
	}
	if patch.Ws.CouldHaveGoneBetter.Set {
		entry.Ws.CouldHaveGoneBetter = patch.Ws.CouldHaveGoneBetter.Value
	}
	if patch.Ws.GoalForTomorrow.Set {
		entry.Ws.GoalForTomorrow = patch.Ws.GoalForTomorrow.Value
	}
	if err := applyRating(&entry.Ratings.Total, patch.Ratings.Total); err != nil {
		return err
	}
	if err := applyRating(&entry.Ratings.Body, patch.Ratings.Body); err != nil {
		return err
	}
	if err := applyRating(&entry.Ratings.Mind, patch.Ratings.Mind); err != nil {
		return err
	}
	if err := applyRating(&entry.Ratings.Spirit, patch.Ratings.Spirit); err != nil {
		return err
	}
	if patch.Pixel.Set {
		if patch.Pixel.Value < 0 || patch.Pixel.Value > 2 {
			return apperror.New(apperror.CodeInvalidEntry, "pixel must be 0 (grey), 1 (green), or 2 (orange)")
		}
		entry.Pixel = patch.Pixel.Value
	}
	return applyWorkHours(&entry.WorkHours, patch.WorkHours)
}

func applyWorkHours(target **float64, patch OptionalWorkHours) error {
	if !patch.Set {
		return nil
	}
	if patch.Value == nil {
		*target = nil
		return nil
	}
	if err := ValidateWorkHours(*patch.Value); err != nil {
		return err
	}
	value := *patch.Value
	*target = &value
	return nil
}

func applyRating(target **int, patch OptionalRating) error {
	if !patch.Set {
		return nil
	}
	if patch.Value != nil && (*patch.Value < 1 || *patch.Value > 5) {
		return apperror.New(apperror.CodeInvalidEntry, "ratings must be between 1 and 5")
	}
	if patch.Value == nil {
		*target = nil
	} else {
		value := *patch.Value
		*target = &value
	}
	return nil
}

func normalizedGoals(goals []Goal) []Goal {
	result := make([]Goal, 5)
	copy(result, goals)
	return result
}

func normalizedGratitudes(gratitudes []string) []string {
	result := make([]string, 3)
	copy(result, gratitudes)
	return result
}

func normalizedCheckoffs(checkoffs []string) []string {
	seen := make(map[string]struct{}, len(checkoffs))
	result := make([]string, 0, len(checkoffs))
	for _, checkoff := range checkoffs {
		if checkoff == "" {
			continue
		}
		if _, ok := seen[checkoff]; ok {
			continue
		}
		seen[checkoff] = struct{}{}
		result = append(result, checkoff)
	}
	sort.Strings(result)
	return result
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func ratingValue(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func workHoursValue(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}
