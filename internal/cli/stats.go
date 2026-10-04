package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	apiclient "github.com/ferriskleier/delta/internal/client"
	"github.com/ferriskleier/delta/internal/service"
)

func runStats(ctx context.Context, args []string, stdout io.Writer) error {
	if containsHelpArg(args) {
		_, err := io.WriteString(stdout, "usage: delta stats [--year YYYY] [--from YYYY-MM-DD --to YYYY-MM-DD] [--agg month] [--json]\n")
		return err
	}
	flags := flag.NewFlagSet("delta stats", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	yearFlag := flags.Int("year", 0, "calendar year (default current year)")
	fromFlag := flags.String("from", "", "first date, inclusive")
	toFlag := flags.String("to", "", "last date, inclusive")
	aggFlag := flags.String("agg", "month", "aggregation (month only)")
	jsonFlag := flags.Bool("json", false, "write JSON")
	parsed, err := normalizeArgs(args, flags, nil)
	if err != nil {
		return err
	}
	if err := flags.Parse(parsed); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("stats accepts no positional arguments")
	}
	if *yearFlag != 0 && (*fromFlag != "" || *toFlag != "") {
		return errors.New("stats accepts --year or --from/--to, not both")
	}
	from, to := *fromFlag, *toFlag
	if *yearFlag != 0 {
		from = fmt.Sprintf("%04d-01-01", *yearFlag)
		to = fmt.Sprintf("%04d-12-31", *yearFlag)
	}
	query := url.Values{}
	if from != "" {
		query.Set("from", from)
	}
	if to != "" {
		query.Set("to", to)
	}
	if *aggFlag != "" {
		query.Set("agg", *aggFlag)
	}
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	status, body, err := client.Do(ctx, http.MethodGet, "/api/stats?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, body)
	}
	if *jsonFlag {
		return writeBody(stdout, body)
	}
	var stats service.StatsResponse
	if err := json.Unmarshal(body, &stats); err != nil {
		return fmt.Errorf("decode stats response: %w", err)
	}
	var changes service.StatsChanges
	if stats.Changes != nil {
		changes = *stats.Changes
	}
	total := "—"
	if stats.Averages.Total != nil {
		total = fmt.Sprintf("%.1f", *stats.Averages.Total) + statsChangeSuffix(changes.Total)
	}
	habit := "—"
	if stats.Averages.HabitScore != nil {
		habit = fmt.Sprintf("%.0f%%", *stats.Averages.HabitScore) + statsChangeSuffix(changes.HabitScore)
	}
	work := "—"
	if stats.Averages.WorkHours != nil {
		work = fmt.Sprintf("%.1fh", *stats.Averages.WorkHours) + statsChangeSuffix(changes.WorkHours)
	}
	if _, err := fmt.Fprintf(stdout, "%s → %s · %d chars%s · avg total %s · habit %s · work %s\n", stats.From, stats.To, stats.Characters, statsChangeSuffix(changes.Characters), total, habit, work); err != nil {
		return err
	}
	if caption := charactersRecordCaption(stats.CharactersRecord); caption != "" {
		_, err = fmt.Fprintln(stdout, caption)
	}
	return err
}

// statsChangeSuffix renders a relative change as " (+4.1%)"; absent changes
// render nothing.
func statsChangeSuffix(change *float64) string {
	if change == nil {
		return ""
	}
	return fmt.Sprintf(" (%+.1f%%)", *change)
}

func charactersRecordCaption(record *service.CharactersRecord) string {
	if record == nil {
		return ""
	}
	switch record.State {
	case service.CharactersRecordBehind:
		if record.PerDay == nil || record.RecordYear == nil {
			return ""
		}
		return fmt.Sprintf("%s/day to break record (%d)", groupThousands(*record.PerDay), *record.RecordYear)
	case service.CharactersRecordAhead:
		if record.Difference == nil || record.RecordYear == nil {
			return ""
		}
		return fmt.Sprintf("+%s over record (%d)", groupThousands(*record.Difference), *record.RecordYear)
	case service.CharactersRecordPast:
		if record.Difference == nil || record.PreviousYear == nil {
			return ""
		}
		sign := "+"
		if *record.Difference < 0 {
			sign = "-"
		}
		return fmt.Sprintf("%s%s vs %d", sign, groupThousands(absInt(*record.Difference)), *record.PreviousYear)
	case service.CharactersRecordRecord:
		return "record year"
	}
	return ""
}

func groupThousands(value int) string {
	digits := strconv.Itoa(absInt(value))
	var grouped strings.Builder
	for index, digit := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(digit)
	}
	if value < 0 {
		return "-" + grouped.String()
	}
	return grouped.String()
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
