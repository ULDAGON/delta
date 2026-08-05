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
	total := "—"
	if stats.Averages.Total != nil {
		total = fmt.Sprintf("%.1f", *stats.Averages.Total)
	}
	habit := "—"
	if stats.Averages.HabitScore != nil {
		habit = fmt.Sprintf("%.0f%%", *stats.Averages.HabitScore)
	}
	work := "—"
	if stats.Averages.WorkHours != nil {
		work = fmt.Sprintf("%.1fh", *stats.Averages.WorkHours)
	}
	_, err = fmt.Fprintf(stdout, "%s → %s · %d chars · avg total %s · habit %s · work %s\n", stats.From, stats.To, stats.Characters, total, habit, work)
	return err
}
