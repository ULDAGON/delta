package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"

	apiclient "github.com/ferriskleier/delta/internal/client"
	"github.com/ferriskleier/delta/internal/service"
)

func runBackup(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("delta backup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonFlag := flags.Bool("json", false, "write JSON")
	parsed, err := normalizeArgs(args, flags, nil)
	if err != nil {
		return err
	}
	if err := flags.Parse(parsed); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("delta backup accepts no positional arguments")
	}

	client, err := newAPIClient()
	if err != nil {
		return err
	}
	status, body, err := client.Do(ctx, http.MethodPost, "/api/backup", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, body)
	}
	if *jsonFlag {
		return writeBody(stdout, body)
	}
	var backup service.BackupResult
	if err := json.Unmarshal(body, &backup); err != nil {
		return fmt.Errorf("decode backup response: %w", err)
	}
	_, err = fmt.Fprintf(stdout, "backup created: %s\n", backup.Path)
	return err
}
