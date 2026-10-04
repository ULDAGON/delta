package service

import (
	"context"
	"fmt"
	"time"

	"github.com/ferriskleier/delta/internal/apperror"
)

// StatsPoint is one monthly aggregate. Value is nil when no eligible samples
// exist in that month; Samples makes the denominator visible to clients.
type StatsPoint struct {
	Month   string   `json:"month"`
	Value   *float64 `json:"value"`
	Samples int      `json:"samples"`
}

type StatsAverages struct {
	Total      *float64 `json:"total"`
	HabitScore *float64 `json:"habit_score"`
	WorkHours  *float64 `json:"work_hours"`
}

// StatsChanges is the relative change in percent against the comparison
// window in the previous year. A value is nil when either side is missing or
// the previous value is zero.
type StatsChanges struct {
	Characters *float64 `json:"characters"`
	Total      *float64 `json:"total"`
	HabitScore *float64 `json:"habit_score"`
	WorkHours  *float64 `json:"work_hours"`
}

// Characters record states.
const (
	CharactersRecordBehind = "behind"
	CharactersRecordAhead  = "ahead"
	CharactersRecordPast   = "past"
	CharactersRecordRecord = "record"
)

// CharactersRecord compares a calendar year's character total with the record
// year. Behind and ahead apply to the current year; past and record apply to
// finished years. Numbers a state does not use are omitted.
type CharactersRecord struct {
	State            string `json:"state"`
	RecordYear       *int   `json:"record_year,omitempty"`
	RecordCharacters *int   `json:"record_characters,omitempty"`
	PerDay           *int   `json:"per_day,omitempty"`
	Difference       *int   `json:"difference,omitempty"`
	PreviousYear     *int   `json:"previous_year,omitempty"`
}

type HabitStreak struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Current int    `json:"current"`
	Best    int    `json:"best"`
}

type HabitCompletion struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Checked    int     `json:"checked"`
	ActiveDays int     `json:"active_days"`
	Percent    float64 `json:"percent"`
}

// StatsResponse is the read-only, monthly stats view consumed by the web UI
// and the CLI. Rating is the average Total rating over rated entries; the
// habit series averages one derived daily score per active-habit day.
type StatsResponse struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Aggregation string `json:"aggregation"`
	// Year reports the year containing From, including for cross-year ranges.
	Year       int          `json:"year"`
	Rating     []StatsPoint `json:"rating"`
	HabitScore []StatsPoint `json:"habit_score"`
	// WorkHours averages recorded work hours per month. A month with no
	// recorded hours has a nil value rather than a zero average.
	WorkHours    []StatsPoint      `json:"work_hours"`
	Averages     StatsAverages     `json:"averages"`
	Entries      int               `json:"entries"`
	Characters   int               `json:"characters"`
	Streaks      []HabitStreak     `json:"streaks"`
	Completion   []HabitCompletion `json:"completion"`
	EarliestYear *int              `json:"earliest_year"`
	CurrentYear  int               `json:"current_year"`
	Years        []int             `json:"years"`
	// Changes and CharactersRecord are only set when the range is exactly one
	// calendar year; CharactersRecord is also nil when no comparison exists.
	Changes          *StatsChanges     `json:"changes"`
	CharactersRecord *CharactersRecord `json:"characters_record"`
}

type statsMonth struct {
	month     string
	ratingSum float64
	ratingN   int
	habitSum  float64
	habitN    int
	workSum   float64
	workN     int
}

// Stats derives monthly rating and habit series from the same entry and
// validity-range data as Grid. Future dates are excluded from every numeric
// aggregate. An empty day with active habits contributes a zero habit score;
// a day with no active habits contributes no habit sample.
func (s *Service) Stats(ctx context.Context, from, to, aggregation string) (StatsResponse, error) {
	if aggregation == "" {
		aggregation = "month"
	}
	if aggregation != "month" {
		return StatsResponse{}, apperror.New(apperror.CodeInvalidStats, "aggregation must be month")
	}
	from, to, err := normalizeStatsRange(from, to)
	if err != nil {
		return StatsResponse{}, err
	}

	entries, err := s.listEntries(ctx, "", "")
	if err != nil {
		return StatsResponse{}, err
	}
	schedules, err := s.habitSchedules(ctx)
	if err != nil {
		return StatsResponse{}, err
	}
	habits, err := s.ListHabits(ctx)
	if err != nil {
		return StatsResponse{}, err
	}

	entryByDate := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		entryByDate[entry.Date] = entry
	}
	today := LocalToday()
	window := aggregateStats(from, to, today, entries, entryByDate, schedules)
	monthValues := window.months
	characters := window.characters
	completionChecked := window.completionChecked
	completionActive := window.completionActive

	// Habits with no active day inside the requested range stay out of the
	// per-range lists entirely; an ended habit belongs to its own years only.
	rangeHabits := make([]Habit, 0, len(habits))
	for _, habit := range habits {
		if completionActive[fmt.Sprintf("%d", habit.ID)] > 0 {
			rangeHabits = append(rangeHabits, habit)
		}
	}

	response := StatsResponse{
		From:        from,
		To:          to,
		Aggregation: aggregation,
		Year:        yearFromDate(from),
		Rating:      make([]StatsPoint, len(monthValues)),
		HabitScore:  make([]StatsPoint, len(monthValues)),
		WorkHours:   make([]StatsPoint, len(monthValues)),
		Averages:    StatsAverages{},
		Entries:     window.entries,
		Characters:  characters,
		Streaks:     calculateHabitStreaks(rangeHabits, schedules, entryByDate, today),
		Completion:  make([]HabitCompletion, 0, len(rangeHabits)),
	}
	for index, value := range monthValues {
		rating := StatsPoint{Month: value.month, Samples: value.ratingN}
		if value.ratingN > 0 {
			average := value.ratingSum / float64(value.ratingN)
			rating.Value = &average
		}
		habit := StatsPoint{Month: value.month, Samples: value.habitN}
		if value.habitN > 0 {
			average := value.habitSum / float64(value.habitN)
			habit.Value = &average
		}
		work := StatsPoint{Month: value.month, Samples: value.workN}
		if value.workN > 0 {
			average := value.workSum / float64(value.workN)
			work.Value = &average
		}
		response.Rating[index] = rating
		response.HabitScore[index] = habit
		response.WorkHours[index] = work
	}
	response.Averages = window.averages()
	for _, habit := range rangeHabits {
		id := fmt.Sprintf("%d", habit.ID)
		activeDays := completionActive[id]
		percent := float64(completionChecked[id]) / float64(activeDays) * 100
		response.Completion = append(response.Completion, HabitCompletion{
			ID: habit.ID, Name: habit.Name, Checked: completionChecked[id], ActiveDays: activeDays, Percent: percent,
		})
	}
	response.EarliestYear, response.CurrentYear, response.Years = yearRailMetadata(entries)
	if year, ok := wholeCalendarYear(from, to); ok {
		response.Changes = statsChanges(year, today, response, entries, entryByDate, schedules)
		response.CharactersRecord = statsCharactersRecord(year, today, characters, entries)
	}
	return response, nil
}

// statsWindow holds the raw aggregates for one date range. Stats reads the
// monthly series and completion counts from it; the previous-year comparison
// reuses it for the averages only.
type statsWindow struct {
	months            []statsMonth
	entries           int
	characters        int
	ratingTotal       float64
	ratingCount       int
	completionChecked map[string]int
	completionActive  map[string]int
}

// aggregateStats walks from through min(to, today). The month list still spans
// the whole range so future months appear as empty points.
func aggregateStats(from, to, today string, entries []Entry, entryByDate map[string]Entry, schedules []habitSchedule) statsWindow {
	months := statsMonths(from, to)
	monthIndex := make(map[string]int, len(months))
	window := statsWindow{
		months:            make([]statsMonth, len(months)),
		completionChecked: map[string]int{},
		completionActive:  map[string]int{},
	}
	for index, month := range months {
		window.months[index].month = month
		monthIndex[month] = index
	}
	effectiveTo := to
	if effectiveTo > today {
		effectiveTo = today
	}
	if from > effectiveTo {
		return window
	}

	for _, entry := range entries {
		if entry.Date < from || entry.Date > effectiveTo {
			continue
		}
		window.entries++
		window.characters += len([]rune(entry.Text))
		if entry.Ratings.Total != nil {
			window.ratingTotal += float64(*entry.Ratings.Total)
			window.ratingCount++
			window.months[monthIndex[entry.Date[:7]]].ratingSum += float64(*entry.Ratings.Total)
			window.months[monthIndex[entry.Date[:7]]].ratingN++
		}
		if entry.WorkHours != nil {
			window.months[monthIndex[entry.Date[:7]]].workSum += *entry.WorkHours
			window.months[monthIndex[entry.Date[:7]]].workN++
		}
	}

	for date := from; date <= effectiveTo; date = nextDate(date) {
		active := activeHabitIDsAt(schedules, date)
		if len(active) == 0 {
			continue
		}
		entry := entryByDate[date]
		daily := CalculateDailyHabitScore(entry.Checkoffs, active)
		score := float64(0)
		if daily.Percent != nil {
			score = *daily.Percent
		}
		point := &window.months[monthIndex[date[:7]]]
		point.habitSum += score
		point.habitN++
		for habitID := range active {
			window.completionActive[habitID]++
		}
		for _, habitID := range daily.VisibleCheckoffs {
			window.completionChecked[habitID]++
		}
	}
	return window
}

func (w statsWindow) averages() StatsAverages {
	var averages StatsAverages
	if w.ratingCount > 0 {
		average := w.ratingTotal / float64(w.ratingCount)
		averages.Total = &average
	}
	var habitSum, workSum float64
	var habitCount, workCount int
	for _, point := range w.months {
		habitSum += point.habitSum
		habitCount += point.habitN
		workSum += point.workSum
		workCount += point.workN
	}
	if habitCount > 0 {
		average := habitSum / float64(habitCount)
		averages.HabitScore = &average
	}
	if workCount > 0 {
		average := workSum / float64(workCount)
		averages.WorkHours = &average
	}
	return averages
}

// wholeCalendarYear reports the year when the range is exactly Jan 1 through
// Dec 31 of one year.
func wholeCalendarYear(from, to string) (int, bool) {
	year := yearFromDate(from)
	if year < 2 {
		return 0, false
	}
	return year, from == fmt.Sprintf("%04d-01-01", year) && to == fmt.Sprintf("%04d-12-31", year)
}

// statsChanges compares the response against the previous year. The current
// year is compared year-to-date against the same period last year; a past year
// against the whole previous year. A future year has nothing to compare, so
// every value is nil.
func statsChanges(year int, today string, current StatsResponse, entries []Entry, entryByDate map[string]Entry, schedules []habitSchedule) *StatsChanges {
	currentYear := yearFromDate(today)
	if year > currentYear {
		return &StatsChanges{}
	}
	previousFrom := fmt.Sprintf("%04d-01-01", year-1)
	previousTo := fmt.Sprintf("%04d-12-31", year-1)
	if year == currentYear {
		monthDay := today[5:]
		if monthDay == "02-29" {
			monthDay = "02-28"
		}
		previousTo = fmt.Sprintf("%04d-%s", year-1, monthDay)
	}
	previous := aggregateStats(previousFrom, previousTo, today, entries, entryByDate, schedules)
	averages := previous.averages()
	characters := float64(current.Characters)
	previousCharacters := float64(previous.characters)
	return &StatsChanges{
		Characters: relativeChange(&characters, &previousCharacters),
		Total:      relativeChange(current.Averages.Total, averages.Total),
		HabitScore: relativeChange(current.Averages.HabitScore, averages.HabitScore),
		WorkHours:  relativeChange(current.Averages.WorkHours, averages.WorkHours),
	}
}

// relativeChange is (current - previous) / previous in percent, nil when
// either value is missing or the previous value is zero.
func relativeChange(current, previous *float64) *float64 {
	if current == nil || previous == nil || *previous == 0 {
		return nil
	}
	change := (*current - *previous) / *previous * 100
	return &change
}

// statsCharactersRecord paces a calendar year's character total against the
// highest total of any other year. Future-dated entries do not count.
func statsCharactersRecord(year int, today string, characters int, entries []Entry) *CharactersRecord {
	currentYear := yearFromDate(today)
	if year > currentYear {
		return nil
	}
	yearTotals := map[int]int{}
	for _, entry := range entries {
		if entry.Date <= today {
			yearTotals[yearFromDate(entry.Date)] += len([]rune(entry.Text))
		}
	}
	recordYear, recordCharacters := 0, 0
	for candidate, total := range yearTotals {
		if candidate == year || total <= 0 {
			continue
		}
		if total > recordCharacters || (total == recordCharacters && candidate < recordYear) {
			recordYear, recordCharacters = candidate, total
		}
	}

	if year == currentYear {
		if recordYear == 0 {
			return nil
		}
		if characters > recordCharacters {
			difference := characters - recordCharacters
			return &CharactersRecord{State: CharactersRecordAhead, RecordYear: &recordYear, RecordCharacters: &recordCharacters, Difference: &difference}
		}
		// Today counts as a remaining day. At least one character is needed even
		// when the totals are level, so the pace never reads as zero.
		needed := recordCharacters - characters
		if needed < 1 {
			needed = 1
		}
		daysLeft := time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC).YearDay() - mustParseDate(today).YearDay() + 1
		perDay := (needed + daysLeft - 1) / daysLeft
		return &CharactersRecord{State: CharactersRecordBehind, RecordYear: &recordYear, RecordCharacters: &recordCharacters, PerDay: &perDay}
	}

	if characters > 0 && characters > recordCharacters {
		return &CharactersRecord{State: CharactersRecordRecord}
	}
	previousYear := year - 1
	if len(entries) == 0 || previousYear < yearFromDate(entries[0].Date) {
		return nil
	}
	difference := characters - yearTotals[previousYear]
	return &CharactersRecord{State: CharactersRecordPast, Difference: &difference, PreviousYear: &previousYear}
}

func mustParseDate(date string) time.Time {
	value, _ := time.Parse(serviceDateFormat, date)
	return value
}

func normalizeStatsRange(from, to string) (string, string, error) {
	if from == "" && to == "" {
		year := LocalCurrentYear()
		return fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year), nil
	}
	if from == "" {
		if err := ValidateDate(to); err != nil {
			return "", "", err
		}
		from = fmt.Sprintf("%04d-01-01", yearFromDate(to))
	}
	if to == "" {
		if err := ValidateDate(from); err != nil {
			return "", "", err
		}
		to = fmt.Sprintf("%04d-12-31", yearFromDate(from))
	}
	if err := ValidateDate(from); err != nil {
		return "", "", err
	}
	if err := ValidateDate(to); err != nil {
		return "", "", err
	}
	if from > to {
		return "", "", apperror.New(apperror.CodeInvalidDate, "from date must not be after to date")
	}
	return from, to, nil
}

func statsMonths(from, to string) []string {
	start := from[:7] + "-01"
	end := to[:7] + "-01"
	months := make([]string, 0, 12)
	for date := start; date <= end; date = nextMonth(date) {
		months = append(months, date[:7])
	}
	return months
}

// Both helpers parse in UTC and re-anchor at local noon for the same DST
// reason as the day loops in grid.go: around a spring-forward at local
// midnight, both the parse and the AddDate normalize onto the previous day,
// which made nextDate return its input and stall every date-walking loop.
func nextDate(date string) string {
	value, _ := time.Parse(serviceDateFormat, date)
	return time.Date(value.Year(), value.Month(), value.Day(), 12, 0, 0, 0, time.Local).AddDate(0, 0, 1).Format(serviceDateFormat)
}

func nextMonth(date string) string {
	value, _ := time.Parse(serviceDateFormat, date)
	return time.Date(value.Year(), value.Month(), value.Day(), 12, 0, 0, 0, time.Local).AddDate(0, 1, 0).Format(serviceDateFormat)
}

func yearRailMetadata(entries []Entry) (*int, int, []int) {
	currentYear := LocalCurrentYear()
	firstYear := currentYear
	var earliestYear *int
	if len(entries) > 0 {
		value := yearFromDate(entries[0].Date)
		if value > 0 {
			earliestYear = &value
			if value < firstYear {
				firstYear = value
			}
		}
	}
	if firstYear > currentYear {
		firstYear = currentYear
	}
	years := make([]int, 0, currentYear-firstYear+1)
	for value := firstYear; value <= currentYear; value++ {
		years = append(years, value)
	}
	if len(years) == 0 {
		years = []int{currentYear}
	}
	return earliestYear, currentYear, years
}

func calculateHabitStreaks(habits []Habit, schedules []habitSchedule, entryByDate map[string]Entry, today string) []HabitStreak {
	result := make([]HabitStreak, 0, len(habits))
	for _, habit := range habits {
		schedule, ok := scheduleForID(schedules, fmt.Sprintf("%d", habit.ID))
		if !ok {
			result = append(result, HabitStreak{ID: habit.ID, Name: habit.Name})
			continue
		}
		start := scheduleStart(schedule)
		if start == "" || start > today {
			result = append(result, HabitStreak{ID: habit.ID, Name: habit.Name})
			continue
		}
		run, lastActiveRun, best := 0, 0, 0
		for date := start; date <= today; date = nextDate(date) {
			active := habitScheduleActive(schedule, date)
			checked := active && containsCheckoff(entryByDate[date].Checkoffs, schedule.ID)
			// Inactive dates are outside the habit's validity ranges. They neither
			// extend nor break a streak, so a pause gap is skipped entirely.
			if !active {
				continue
			}
			// The current day is a grace period. An unchecked active habit does
			// not break yesterday's current streak until the local date rolls.
			if date == today && !checked {
				continue
			}
			if checked {
				run++
				lastActiveRun = run
				if run > best {
					best = run
				}
			} else {
				run = 0
				lastActiveRun = 0
			}
		}
		activeToday := habitScheduleActive(schedule, today)
		futureRange := scheduleHasFutureRange(schedule, today)
		current := run
		if !activeToday && !futureRange {
			// Once an archived habit has no future validity range, preserve the
			// streak as of its final valid day. An unchecked final valid day is
			// therefore correctly zero rather than inheriting an older run.
			current = lastActiveRun
		}
		result = append(result, HabitStreak{ID: habit.ID, Name: habit.Name, Current: current, Best: best})
	}
	return result
}

func scheduleForID(schedules []habitSchedule, id string) (habitSchedule, bool) {
	for _, schedule := range schedules {
		if schedule.ID == id {
			return schedule, true
		}
	}
	return habitSchedule{}, false
}

func scheduleStart(schedule habitSchedule) string {
	start := ""
	for _, habitRange := range schedule.Ranges {
		if start == "" || habitRange.ActiveFrom < start {
			start = habitRange.ActiveFrom
		}
	}
	return start
}

func habitScheduleActive(schedule habitSchedule, date string) bool {
	for _, habitRange := range schedule.Ranges {
		if habitRangeActive(habitRange, date) {
			return true
		}
	}
	return false
}

func scheduleHasFutureRange(schedule habitSchedule, today string) bool {
	for _, habitRange := range schedule.Ranges {
		if habitRange.ActiveFrom > today {
			return true
		}
	}
	return false
}

func containsCheckoff(checkoffs []string, habitID string) bool {
	for _, checkoff := range checkoffs {
		if checkoff == habitID {
			return true
		}
	}
	return false
}
