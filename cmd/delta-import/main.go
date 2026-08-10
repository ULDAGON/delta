// Command delta-import is a personal one-off importer for the pre-DELTA
// journal: `values` loads the spreadsheet export (ratings, habit checkoffs,
// work hours, day pixels) and `journal` loads the plain-text journal file.
// Both are merge-safe: they never overwrite a non-empty field.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/service"
	"github.com/ferriskleier/delta/internal/storage"
)

// entryOrBlank reads an entry, treating a missing row as an all-empty entry
// so merge checks work uniformly for new and existing dates.
func entryOrBlank(ctx context.Context, svc *service.Service, date string) (service.Entry, error) {
	entry, err := svc.GetEntry(ctx, date)
	if apperror.Code(err) == apperror.CodeEntryNotFound {
		return service.Entry{Date: date, Goals: make([]service.Goal, 5), Gratitudes: make([]string, 3)}, nil
	}
	return entry, err
}

func main() {
	usage := func() {
		fmt.Fprintln(os.Stderr, "usage: delta-import values <values.json> | delta-import journal <journal.txt> | delta-import merge <duplicateHabitID> <keptHabitID> | delta-import cleanup-prehistory")
		os.Exit(2)
	}
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "values", "journal":
		if len(os.Args) != 3 {
			usage()
		}
	case "merge":
		if len(os.Args) != 4 {
			usage()
		}
	case "cleanup-prehistory":
		if len(os.Args) != 2 {
			usage()
		}
	default:
		usage()
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "delta-import:", err)
		os.Exit(1)
	}
}

func run(command string, args []string) error {
	ctx := context.Background()
	c, err := config.Load()
	if err != nil {
		return err
	}
	store, err := storage.Open(ctx, c.DatabasePath, c.Key)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := storage.MigrateStoreWithBackups(ctx, store, c.BackupsPath); err != nil {
		return err
	}
	svc := service.New(store, service.WithBackupsPath(c.BackupsPath))
	fmt.Println("database:", store.Path)

	backup, err := svc.CreateBackup(ctx)
	if err != nil {
		return fmt.Errorf("pre-import backup failed: %w", err)
	}
	fmt.Println("backup:  ", backup.Path)

	switch command {
	case "values":
		return runValues(ctx, svc, args[0])
	case "cleanup-prehistory":
		return runCleanupPrehistory(ctx, svc)
	case "merge":
		fromID, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("duplicate habit id %q: %w", args[0], err)
		}
		toID, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("kept habit id %q: %w", args[1], err)
		}
		return runMerge(ctx, svc, fromID, toID)
	default:
		return runJournal(ctx, svc, args[0])
	}
}

type valuesRow struct {
	Date      string   `json:"date"`
	D         *float64 `json:"d"`
	Body      *float64 `json:"body"`
	Mind      *float64 `json:"mind"`
	Spirit    *float64 `json:"spirit"`
	HabitCols []int    `json:"habit_cols"`
	WorkHours *float64 `json:"work_hours"`
	Pixel     int      `json:"pixel"`
}

func runValues(ctx context.Context, svc *service.Service, path string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var rows []valuesRow
	if err := json.Unmarshal(contents, &rows); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Date < rows[j].Date })

	habitIDs, err := ensureHabits(ctx, svc)
	if err != nil {
		return err
	}

	var entriesWritten, checkoffsWritten, skippedEmpty, prehistoryDropped int
	var checkoffsSkipped, ratingConflicts []string
	for _, row := range rows {
		if err := service.ValidateDate(row.Date); err != nil {
			return fmt.Errorf("row %q: %w", row.Date, err)
		}
		if !rowHasData(row) {
			skippedEmpty++
			continue
		}
		entry, err := entryOrBlank(ctx, svc, row.Date)
		if err != nil {
			return fmt.Errorf("%s: %w", row.Date, err)
		}

		patch := service.EntryPatch{}
		changed := false
		importRating := func(source *float64, current *int, set func(service.OptionalRating)) {
			value, ok := ratingFrom(source)
			if !ok {
				return
			}
			if current != nil {
				if *current != value {
					ratingConflicts = append(ratingConflicts, row.Date)
				}
				return
			}
			set(service.OptionalRating{Set: true, Value: &value})
			changed = true
		}
		importRating(row.D, entry.Ratings.Total, func(r service.OptionalRating) { patch.Ratings.Total = r })
		importRating(row.Body, entry.Ratings.Body, func(r service.OptionalRating) { patch.Ratings.Body = r })
		importRating(row.Mind, entry.Ratings.Mind, func(r service.OptionalRating) { patch.Ratings.Mind = r })
		importRating(row.Spirit, entry.Ratings.Spirit, func(r service.OptionalRating) { patch.Ratings.Spirit = r })
		if row.Pixel > 0 && entry.Pixel == 0 {
			patch.Pixel = service.OptionalPixel{Set: true, Value: row.Pixel}
			changed = true
		}
		if row.WorkHours != nil && entry.WorkHours == nil {
			value := *row.WorkHours
			patch.WorkHours = service.OptionalWorkHours{Set: true, Value: &value}
			changed = true
		}
		if changed {
			if _, err := svc.UpsertEntry(ctx, row.Date, patch); err != nil {
				return fmt.Errorf("%s: %w", row.Date, err)
			}
			entriesWritten++
		}

		if len(row.HabitCols) == 0 {
			continue
		}
		if len(entry.Checkoffs) > 0 {
			checkoffsSkipped = append(checkoffsSkipped, row.Date)
			continue
		}
		if row.Date < restructureDate {
			prehistoryDropped += len(row.HabitCols)
			continue
		}
		columns := columnHabits(row.Date)
		for _, column := range row.HabitCols {
			name, ok := columns[column]
			if !ok {
				return fmt.Errorf("%s: checkbox column %d maps to no habit", row.Date, column)
			}
			if _, err := svc.SetCheckoff(ctx, row.Date, habitIDs[name], true); err != nil {
				return fmt.Errorf("%s: check off %q: %w", row.Date, name, err)
			}
			checkoffsWritten++
		}
	}

	fmt.Printf("entries updated:    %d\n", entriesWritten)
	fmt.Printf("checkoffs written:  %d\n", checkoffsWritten)
	fmt.Printf("empty rows skipped: %d\n", skippedEmpty)
	if prehistoryDropped > 0 {
		fmt.Printf("pre-restructure checkmarks dropped: %d\n", prehistoryDropped)
	}
	if len(checkoffsSkipped) > 0 {
		fmt.Printf("dates keeping their existing checkoffs (%d): %v\n", len(checkoffsSkipped), checkoffsSkipped)
	}
	if len(ratingConflicts) > 0 {
		fmt.Printf("dates keeping their existing differing ratings (%d): %v\n", len(ratingConflicts), ratingConflicts)
	}
	return nil
}

// ratingFrom converts a spreadsheet rating to DELTA's 1-5 scale. Zero meant
// "not rated" in the spreadsheet and is treated as absent.
func ratingFrom(source *float64) (int, bool) {
	if source == nil {
		return 0, false
	}
	value := int(*source + 0.5)
	if value < 1 || value > 5 {
		return 0, false
	}
	return value, true
}

func rowHasData(row valuesRow) bool {
	for _, rating := range []*float64{row.D, row.Body, row.Mind, row.Spirit} {
		if _, ok := ratingFrom(rating); ok {
			return true
		}
	}
	return len(row.HabitCols) > 0 || row.WorkHours != nil || row.Pixel > 0
}

// ensureHabits matches timeline habits by exact name, creates missing ones in
// position order, and replaces each one's ranges with the reconstructed
// historical range.
func ensureHabits(ctx context.Context, svc *service.Service) (map[string]int64, error) {
	existing, err := svc.ListHabits(ctx)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]service.Habit, len(existing))
	for _, habit := range existing {
		byName[habit.Name] = habit
	}
	ids := make(map[string]int64, len(habitTimeline))
	for _, def := range habitTimeline {
		habit, found := byName[def.Name]
		if !found {
			habit, err = svc.CreateHabit(ctx, def.Name)
			if err != nil {
				return nil, fmt.Errorf("create habit %q: %w", def.Name, err)
			}
			fmt.Printf("habit created: %s\n", def.Name)
		} else {
			fmt.Printf("habit matched: %s\n", def.Name)
		}
		habitRange := service.HabitRange{ActiveFrom: def.ActiveFrom}
		if def.ActiveTo != "" {
			activeTo := def.ActiveTo
			habitRange.ActiveTo = &activeTo
		}
		ranges := []service.HabitRange{habitRange}
		if _, err := svc.PatchHabit(ctx, habit.ID, service.HabitPatch{Ranges: &ranges}); err != nil {
			return nil, fmt.Errorf("set ranges for %q: %w", def.Name, err)
		}
		ids[def.Name] = habit.ID
	}
	if len(existing) > len(habitTimeline) {
		fmt.Println("note: the database has habits outside the import timeline; they were left untouched")
	}
	return ids, nil
}

func runJournal(ctx context.Context, svc *service.Service, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	parsed, parseErrors := parseJournal(file)

	var updated int
	var skippedFields []string
	for _, entry := range parsed {
		current, err := entryOrBlank(ctx, svc, entry.Date)
		if err != nil {
			return fmt.Errorf("%s: %w", entry.Date, err)
		}
		patch := service.EntryPatch{}
		changed := false

		setText := func(field string, source string, existing string, set func(service.OptionalString)) {
			if source == "" {
				return
			}
			if existing != "" {
				skippedFields = append(skippedFields, entry.Date+" "+field)
				return
			}
			set(service.OptionalString{Set: true, Value: source})
			changed = true
		}
		setText("text", entry.Text, current.Text, func(v service.OptionalString) { patch.Text = v })
		setText("went-well", entry.WentWell, current.Ws.WentWell, func(v service.OptionalString) { patch.Ws.WentWell = v })
		setText("could-have-gone-better", entry.CouldHaveGoneBetter, current.Ws.CouldHaveGoneBetter, func(v service.OptionalString) { patch.Ws.CouldHaveGoneBetter = v })
		setText("goal-for-tomorrow", entry.GoalForTomorrow, current.Ws.GoalForTomorrow, func(v service.OptionalString) { patch.Ws.GoalForTomorrow = v })

		if len(entry.Goals) > 0 {
			if goalsEmpty(current.Goals) {
				goals := make([]service.Goal, 5)
				copy(goals, entry.Goals)
				patch.GoalsSet = true
				patch.Goals = goals
				changed = true
			} else {
				skippedFields = append(skippedFields, entry.Date+" goals")
			}
		}
		if len(entry.Gratitudes) > 0 {
			if stringsEmpty(current.Gratitudes) {
				gratitudes := make([]string, 3)
				copy(gratitudes, entry.Gratitudes)
				patch.GratitudesSet = true
				patch.Gratitudes = gratitudes
				changed = true
			} else {
				skippedFields = append(skippedFields, entry.Date+" gratitudes")
			}
		}

		if changed {
			if _, err := svc.UpsertEntry(ctx, entry.Date, patch); err != nil {
				return fmt.Errorf("%s: %w", entry.Date, err)
			}
			updated++
		}
	}

	fmt.Printf("journal entries parsed: %d\n", len(parsed))
	fmt.Printf("entries updated:        %d\n", updated)
	if len(skippedFields) > 0 {
		fmt.Printf("fields left untouched because DELTA already has content (%d):\n", len(skippedFields))
		for _, field := range skippedFields {
			fmt.Println("  ", field)
		}
	}
	if len(parseErrors) > 0 {
		fmt.Printf("blocks skipped due to parse errors (%d):\n", len(parseErrors))
		for _, parseError := range parseErrors {
			fmt.Println("  ", parseError)
		}
		return fmt.Errorf("%d journal blocks failed to parse; fix them and re-run (already-imported entries are skipped automatically)", len(parseErrors))
	}
	return nil
}

func goalsEmpty(goals []service.Goal) bool {
	for _, goal := range goals {
		if goal.Text != "" || goal.Checked {
			return false
		}
	}
	return true
}

func stringsEmpty(values []string) bool {
	for _, value := range values {
		if value != "" {
			return false
		}
	}
	return true
}
