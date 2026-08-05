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

	apiclient "github.com/ferriskleier/delta/internal/client"
	"github.com/ferriskleier/delta/internal/service"
)

func runGrid(ctx context.Context, args []string, stdout io.Writer) error {
	if containsHelpArg(args) {
		_, err := io.WriteString(stdout, "usage: delta grid [--year YYYY] [--view rating|habit] [--json]\n")
		return err
	}
	flags := flag.NewFlagSet("delta grid", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	yearFlag := flags.Int("year", 0, "calendar year (default current year)")
	viewFlag := flags.String("view", service.GridViewRating, "rating or habit")
	jsonFlag := flags.Bool("json", false, "write JSON")
	parsed, err := normalizeArgs(args, flags, nil)
	if err != nil {
		return err
	}
	if err := flags.Parse(parsed); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("grid accepts no positional arguments")
	}
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	query := url.Values{}
	if *yearFlag != 0 {
		query.Set("year", strconv.Itoa(*yearFlag))
	}
	query.Set("view", *viewFlag)
	status, body, err := client.Do(ctx, http.MethodGet, "/api/grid?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, body)
	}
	if *jsonFlag {
		return writeBody(stdout, body)
	}
	var grid service.GridResponse
	if err := json.Unmarshal(body, &grid); err != nil {
		return fmt.Errorf("decode grid response: %w", err)
	}
	average := "—"
	if grid.Summary.AverageRating != nil {
		average = fmt.Sprintf("%.1f", *grid.Summary.AverageRating)
	}
	habit := "—"
	if grid.Summary.HabitPercent != nil {
		habit = fmt.Sprintf("%.0f%%", *grid.Summary.HabitPercent)
	}
	_, err = fmt.Fprintf(stdout, "%d %s · %d entries · %d chars · avg %s · habit %s\n", grid.Year, grid.View, grid.Summary.Entries, grid.Summary.Characters, average, habit)
	return err
}
