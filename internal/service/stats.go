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
	months := statsMonths(from, to)
	monthIndex := make(map[string]int, len(months))
	monthValues := make([]statsMonth, len(months))
	for index, month := range months {
		monthValues[index].month = month
		monthIndex[month] = index
	}
	today := LocalToday()
	effectiveTo := to
	if effectiveTo > today {
		effectiveTo = today
	}

	var ratingTotal float64
	ratingCount := 0
	entriesInRange := 0
	characters := 0
	completionChecked := make(map[string]int, len(habits))
	completionActive := make(map[string]int, len(habits))

	if from <= effectiveTo {
		for _, entry := range entries {
			if entry.Date < from || entry.Date > effectiveTo {
				continue
			}
			entriesInRange++
			characters += len([]rune(entry.Text))
			if entry.Ratings.Total != nil {
				ratingTotal += float64(*entry.Ratings.Total)
				ratingCount++
				monthValues[monthIndex[entry.Date[:7]]].ratingSum += float64(*entry.Ratings.Total)
				monthValues[monthIndex[entry.Date[:7]]].ratingN++
			}
			if entry.WorkHours != nil {
				monthValues[monthIndex[entry.Date[:7]]].workSum += *entry.WorkHours
				monthValues[monthIndex[entry.Date[:7]]].workN++
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
			point := &monthValues[monthIndex[date[:7]]]
			point.habitSum += score
			point.habitN++
			for habitID := range active {
				completionActive[habitID]++
			}
			for _, habitID := range daily.VisibleCheckoffs {
				completionChecked[habitID]++
			}
		}
	}

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
		Entries:     entriesInRange,
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
	if ratingCount > 0 {
		average := ratingTotal / float64(ratingCount)
		response.Averages.Total = &average
	}
	var habitSum, workSum float64
	var habitCount, workCount int
	for _, point := range monthValues {
		habitSum += point.habitSum
		habitCount += point.habitN
		workSum += point.workSum
		workCount += point.workN
	}
	if habitCount > 0 {
		average := habitSum / float64(habitCount)
		response.Averages.HabitScore = &average
	}
	if workCount > 0 {
		average := workSum / float64(workCount)
		response.Averages.WorkHours = &average
	}
	for _, habit := range rangeHabits {
		id := fmt.Sprintf("%d", habit.ID)
		activeDays := completionActive[id]
		percent := float64(completionChecked[id]) / float64(activeDays) * 100
		response.Completion = append(response.Completion, HabitCompletion{
			ID: habit.ID, Name: habit.Name, Checked: completionChecked[id], ActiveDays: activeDays, Percent: percent,
		})
	}
	response.EarliestYear, response.CurrentYear, response.Years = yearRailMetadata(entries)
	return response, nil
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
