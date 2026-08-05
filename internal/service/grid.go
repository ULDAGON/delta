package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ferriskleier/delta/internal/apperror"
)

const (
	GridViewRating = "rating"
	GridViewHabit  = "habit"
)

// GridDay is the derived, read-only representation of one calendar date.
// Both view values and entry details are included so clients can switch
// presentation without another request.
type GridDay struct {
	Date          string   `json:"date"`
	Rating        *int     `json:"rating"`
	HabitScore    *float64 `json:"habit_score"`
	Body          *int     `json:"body"`
	Mind          *int     `json:"mind"`
	Spirit        *int     `json:"spirit"`
	HasEntry      bool     `json:"has_entry"`
	Journal       bool     `json:"journal"`
	ActiveHabits  int      `json:"active_habits"`
	CheckedHabits int      `json:"checked_habits"`
	Pixel         int      `json:"pixel"`
}

// GridSummary carries two horizons at once: the counts and averages of the
// requested year, plus the quick view of the current calendar month, which is
// the same month no matter which year is being looked at.
type GridSummary struct {
	Entries            int      `json:"entries"`
	Characters         int      `json:"characters"`
	AverageRating      *float64 `json:"average_rating"`
	HabitPercent       *float64 `json:"habit_percent"`
	MonthAverageRating *float64 `json:"month_average_rating"`
	MonthHabitPercent  *float64 `json:"month_habit_percent"`
}

type GridResponse struct {
	Year         int         `json:"year"`
	View         string      `json:"view"`
	Days         []GridDay   `json:"days"`
	Summary      GridSummary `json:"summary"`
	EarliestYear *int        `json:"earliest_year"`
	CurrentYear  int         `json:"current_year"`
	Years        []int       `json:"years"`
}

// Grid derives all displayed values from entries and habit validity ranges at
// read time. In particular, changing a habit range immediately recomputes
// historical scores and out-of-range check-offs remain uncounted.
func (s *Service) Grid(ctx context.Context, year int, view string) (GridResponse, error) {
	if year < 1 || year > 9999 {
		return GridResponse{}, apperror.New(apperror.CodeInvalidGrid, "year must be between 0001 and 9999")
	}
	if view != GridViewRating && view != GridViewHabit {
		return GridResponse{}, apperror.New(apperror.CodeInvalidGrid, "view must be rating or habit")
	}

	start := fmt.Sprintf("%04d-01-01", year)
	end := fmt.Sprintf("%04d-12-31", year)
	entries, err := s.listEntries(ctx, start, end)
	if err != nil {
		return GridResponse{}, err
	}
	allEntries, err := s.listEntries(ctx, "", "")
	if err != nil {
		return GridResponse{}, err
	}
	// Load all ranges once. Per-day activity is derived from this in-memory
	// schedule instead of issuing one query for every calendar cell.
	schedules, err := s.habitSchedules(ctx)
	if err != nil {
		return GridResponse{}, err
	}

	entryByDate := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		entryByDate[entry.Date] = entry
	}
	today := LocalToday()
	earliestYear, currentYear, years := yearRailMetadata(allEntries)

	result := GridResponse{
		Year:         year,
		View:         view,
		Days:         make([]GridDay, 0, daysInYear(year)),
		EarliestYear: earliestYear,
		CurrentYear:  currentYear,
		Years:        years,
	}
	var ratingTotal, ratingCount float64
	var habitTotal float64
	var habitDays int
	// Day iteration anchors at noon, never midnight: where a DST transition
	// falls at midnight that wall clock is skipped or repeated, so AddDate
	// normalizes across the date boundary and the same date is visited twice.
	for day := time.Date(year, time.January, 1, 12, 0, 0, 0, time.Local); day.Year() == year; day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		entry, hasStoredEntry := entryByDate[date]
		future := date > today
		if future {
			hasStoredEntry = false
		}

		active := activeHabitIDsAt(schedules, date)
		checkoffs := entry.Checkoffs
		if !hasStoredEntry {
			checkoffs = nil
		}
		habitDay := CalculateDailyHabitScore(checkoffs, active)
		var habitScore *float64
		if !future {
			habitScore = habitDay.Percent
			if habitScore != nil {
				habitTotal += *habitScore
				habitDays++
			}
		}

		var rating, body, mind, spirit *int
		var pixel int
		if hasStoredEntry {
			rating, body, mind, spirit = entry.Ratings.Total, entry.Ratings.Body, entry.Ratings.Mind, entry.Ratings.Spirit
			if rating != nil {
				ratingTotal += float64(*rating)
				ratingCount++
			}
			result.Summary.Entries++
			result.Summary.Characters += len([]rune(entry.Text))
			pixel = entry.Pixel
		}
		result.Days = append(result.Days, GridDay{
			Date:          date,
			Rating:        rating,
			HabitScore:    habitScore,
			Body:          body,
			Mind:          mind,
			Spirit:        spirit,
			HasEntry:      hasStoredEntry,
			Journal:       hasStoredEntry && entry.Text != "",
			ActiveHabits:  habitDay.Active,
			CheckedHabits: habitDay.Checked,
			Pixel:         pixel,
		})
	}
	if ratingCount > 0 {
		average := ratingTotal / ratingCount
		result.Summary.AverageRating = &average
	}
	if habitDays > 0 {
		average := habitTotal / float64(habitDays)
		result.Summary.HabitPercent = &average
	}
	result.Summary.MonthAverageRating, result.Summary.MonthHabitPercent = monthQuickView(allEntries, schedules, today)
	return result, nil
}

// monthQuickView averages the current calendar month from its 1st through
// today. It is handed every year's entries because the requested grid year
// need not be the year the current month lives in.
func monthQuickView(entries []Entry, schedules []habitSchedule, today string) (*float64, *float64) {
	// Parsed in UTC: a local parse of a DST-transition date normalizes onto
	// the previous day, which would shift the whole window one month back
	// when today is the 1st.
	day, err := time.Parse("2006-01-02", today)
	if err != nil {
		return nil, nil
	}
	monthPrefix := today[:8]
	entryByDate := make(map[string]Entry, 31)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Date, monthPrefix) {
			entryByDate[entry.Date] = entry
		}
	}
	var ratingTotal, ratingCount float64
	var habitTotal float64
	var habitDays int
	// Noon anchor for the same DST reason as the year loop in Grid.
	month := day.Month()
	for day = time.Date(day.Year(), month, 1, 12, 0, 0, 0, time.Local); day.Month() == month; day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		if date > today {
			break
		}
		entry, hasStoredEntry := entryByDate[date]
		var checkoffs []string
		if hasStoredEntry {
			checkoffs = entry.Checkoffs
			if entry.Ratings.Total != nil {
				ratingTotal += float64(*entry.Ratings.Total)
				ratingCount++
			}
		}
		if percent := CalculateDailyHabitScore(checkoffs, activeHabitIDsAt(schedules, date)).Percent; percent != nil {
			habitTotal += *percent
			habitDays++
		}
	}
	var averageRating, habitPercent *float64
	if ratingCount > 0 {
		average := ratingTotal / ratingCount
		averageRating = &average
	}
	if habitDays > 0 {
		average := habitTotal / float64(habitDays)
		habitPercent = &average
	}
	return averageRating, habitPercent
}

func daysInYear(year int) int {
	return int(time.Date(year+1, time.January, 1, 0, 0, 0, 0, time.Local).Sub(time.Date(year, time.January, 1, 0, 0, 0, 0, time.Local)) / (24 * time.Hour))
}

func yearFromDate(date string) int {
	if len(date) < 4 {
		return 0
	}
	var year int
	if _, err := fmt.Sscanf(date[:4], "%04d", &year); err != nil {
		return 0
	}
	return year
}
