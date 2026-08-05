package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	apiclient "github.com/ferriskleier/delta/internal/client"
	"github.com/ferriskleier/delta/internal/service"
)

func runSearch(ctx context.Context, args []string, stdout io.Writer) error {
	if containsHelpArg(args) {
		_, err := io.WriteString(stdout, "usage: delta search <words> [--json]\n")
		return err
	}
	flags := flag.NewFlagSet("delta search", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonFlag := flags.Bool("json", false, "write JSON")
	parsed, err := normalizeArgs(args, flags, nil)
	if err != nil {
		return err
	}
	if err := flags.Parse(parsed); err != nil {
		return err
	}
	query := strings.Join(flags.Args(), " ")
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	status, body, err := client.Do(ctx, http.MethodGet, "/api/search?q="+url.QueryEscape(query), nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, body)
	}
	if *jsonFlag {
		return writeBody(stdout, body)
	}
	var results []service.SearchResult
	if err := json.Unmarshal(body, &results); err != nil {
		return fmt.Errorf("decode search response: %w", err)
	}
	for _, result := range results {
		if _, err := fmt.Fprintf(stdout, "%s · %s · %s\n", result.Date, result.Field, result.Snippet); err != nil {
			return err
		}
	}
	return nil
}
