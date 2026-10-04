package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/ferriskleier/delta/internal/apperror"
	apiclient "github.com/ferriskleier/delta/internal/client"
	"github.com/ferriskleier/delta/internal/service"
)

// periodCommand binds `delta era` or `delta dynasty` to its REST collection.
// The two commands are identical apart from these names.
type periodCommand struct {
	name         string
	path         string
	notFoundCode string
}

var (
	eraCommand     = periodCommand{name: "era", path: "/api/eras", notFoundCode: apperror.CodeEraNotFound}
	dynastyCommand = periodCommand{name: "dynasty", path: "/api/dynasties", notFoundCode: apperror.CodeDynastyNotFound}
)

func (command periodCommand) run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: delta %s list|add|edit|delete", command.name)
	}
	if isHelpArg(args[0]) {
		return command.writeHelp(stdout, "")
	}
	subcommand := args[0]
	args = args[1:]
	switch subcommand {
	case "list":
		return command.runList(ctx, args, stdout)
	case "add":
		return command.runAdd(ctx, args, stdout)
	case "edit":
		return command.runEdit(ctx, args, stdout)
	case "delete":
		return command.runDelete(ctx, args, stdout)
	default:
		return fmt.Errorf("unknown %s command %q; try list, add, edit, or delete", command.name, subcommand)
	}
}

func (command periodCommand) runList(ctx context.Context, args []string, stdout io.Writer) error {
	if containsHelpArg(args) {
		return command.writeHelp(stdout, "list")
	}
	flags := flag.NewFlagSet("delta "+command.name+" list", flag.ContinueOnError)
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
		return fmt.Errorf("%s list accepts no positional arguments", command.name)
	}
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	status, body, err := client.Do(ctx, http.MethodGet, command.path, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, body)
	}
	if *jsonFlag {
		return writeBody(stdout, body)
	}
	var periods []service.Period
	if err := json.Unmarshal(body, &periods); err != nil {
		return fmt.Errorf("decode %s list response: %w", command.name, err)
	}
	for _, period := range periods {
		if _, err := fmt.Fprintf(stdout, "%s to %s  %s  %s (id %d)\n", period.StartDate, periodEndLabel(period), period.Color, period.Name, period.ID); err != nil {
			return err
		}
	}
	return nil
}

func (command periodCommand) runAdd(ctx context.Context, args []string, stdout io.Writer) error {
	if containsHelpArg(args) {
		return command.writeHelp(stdout, "add")
	}
	flags := flag.NewFlagSet("delta "+command.name+" add", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	nameFlag := optionalString{}
	registerOptional(flags, "name", &nameFlag, command.name+" name")
	colorFlag := flags.String("color", "", "hex color in #rrggbb format")
	startFlag := flags.String("start", "", "first day, YYYY-MM-DD")
	endFlag := optionalString{}
	registerOptional(flags, "end", &endFlag, "last day, YYYY-MM-DD (omit while ongoing)")
	jsonFlag := flags.Bool("json", false, "write JSON")
	parsed, err := normalizeArgs(args, flags, nil)
	if err != nil {
		return err
	}
	if err := flags.Parse(parsed); err != nil {
		return err
	}
	name := strings.TrimSpace(nameFlag.value)
	if !nameFlag.set {
		name = strings.TrimSpace(strings.Join(flags.Args(), " "))
	} else if len(flags.Args()) != 0 {
		return fmt.Errorf("%s add accepts a name either as an argument or with --name, not both", command.name)
	}
	if name == "" || *colorFlag == "" || *startFlag == "" {
		return fmt.Errorf("usage: delta %s add <name> --color <#rrggbb> --start <YYYY-MM-DD> [--end <YYYY-MM-DD>] [--json]", command.name)
	}
	fields := map[string]any{"name": name, "color": *colorFlag, "start_date": *startFlag, "end_date": nil}
	if endFlag.set {
		fields["end_date"] = endFlag.value
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("encode %s: %w", command.name, err)
	}
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	status, responseBody, err := client.Do(ctx, http.MethodPost, command.path, body)
	if err != nil {
		return err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, responseBody)
	}
	if *jsonFlag {
		return writeBody(stdout, responseBody)
	}
	var period service.Period
	if err := json.Unmarshal(responseBody, &period); err != nil {
		return fmt.Errorf("decode %s response: %w", command.name, err)
	}
	_, err = fmt.Fprintf(stdout, "%s %d %q added (%s to %s)\n", command.name, period.ID, period.Name, period.StartDate, periodEndLabel(period))
	return err
}

func (command periodCommand) runEdit(ctx context.Context, args []string, stdout io.Writer) error {
	if containsHelpArg(args) {
		return command.writeHelp(stdout, "edit")
	}
	flags := flag.NewFlagSet("delta "+command.name+" edit", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var nameFlag, colorFlag, startFlag, endFlag optionalString
	registerOptional(flags, "name", &nameFlag, "new name")
	registerOptional(flags, "color", &colorFlag, "new hex color in #rrggbb format")
	registerOptional(flags, "start", &startFlag, "new first day, YYYY-MM-DD")
	registerOptional(flags, "end", &endFlag, "new last day, YYYY-MM-DD")
	ongoingFlag := flags.Bool("ongoing", false, "clear the end date")
	jsonFlag := flags.Bool("json", false, "write JSON")
	parsed, err := normalizeArgs(args, flags, nil)
	if err != nil {
		return err
	}
	if err := flags.Parse(parsed); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New(command.editUsage())
	}
	if endFlag.set && *ongoingFlag {
		return fmt.Errorf("%s edit accepts either --end or --ongoing, not both", command.name)
	}
	fields := map[string]any{}
	if nameFlag.set {
		fields["name"] = nameFlag.value
	}
	if colorFlag.set {
		fields["color"] = colorFlag.value
	}
	if startFlag.set {
		fields["start_date"] = startFlag.value
	}
	if endFlag.set {
		fields["end_date"] = endFlag.value
	}
	if *ongoingFlag {
		fields["end_date"] = nil
	}
	if len(fields) == 0 {
		return errors.New(command.editUsage())
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("encode %s: %w", command.name, err)
	}
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	period, err := command.resolve(ctx, client, flags.Args()[0])
	if err != nil {
		return err
	}
	status, responseBody, err := client.Do(ctx, http.MethodPatch, command.itemPath(period.ID), body)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, responseBody)
	}
	if *jsonFlag {
		return writeBody(stdout, responseBody)
	}
	if err := json.Unmarshal(responseBody, &period); err != nil {
		return fmt.Errorf("decode %s response: %w", command.name, err)
	}
	_, err = fmt.Fprintf(stdout, "%s %d %q updated (%s to %s)\n", command.name, period.ID, period.Name, period.StartDate, periodEndLabel(period))
	return err
}

func (command periodCommand) runDelete(ctx context.Context, args []string, stdout io.Writer) error {
	if containsHelpArg(args) {
		return command.writeHelp(stdout, "delete")
	}
	flags := flag.NewFlagSet("delta "+command.name+" delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonFlag := flags.Bool("json", false, "write JSON")
	parsed, err := normalizeArgs(args, flags, nil)
	if err != nil {
		return err
	}
	if err := flags.Parse(parsed); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return fmt.Errorf("usage: delta %s delete <%s-id-or-exact-name> [--json]", command.name, command.name)
	}
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	period, err := command.resolve(ctx, client, flags.Args()[0])
	if err != nil {
		return err
	}
	status, body, err := client.Do(ctx, http.MethodDelete, command.itemPath(period.ID), nil)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, body)
	}
	if *jsonFlag {
		return writeBody(stdout, []byte(fmt.Sprintf(`{"ok":true,"id":%d}`, period.ID)))
	}
	_, err = fmt.Fprintf(stdout, "%s %q deleted\n", command.name, period.Name)
	return err
}

// resolve accepts a numeric ID or an exact name, as habit identifiers do.
func (command periodCommand) resolve(ctx context.Context, client *apiclient.Client, identifier string) (service.Period, error) {
	status, body, err := client.Do(ctx, http.MethodGet, command.path, nil)
	if err != nil {
		return service.Period{}, err
	}
	if status != http.StatusOK {
		return service.Period{}, apiclient.ErrorFromResponse(status, body)
	}
	var periods []service.Period
	if err := json.Unmarshal(body, &periods); err != nil {
		return service.Period{}, fmt.Errorf("decode %s list response: %w", command.name, err)
	}
	identifier = strings.TrimSpace(identifier)
	if id, err := strconv.ParseInt(identifier, 10, 64); err == nil {
		for _, period := range periods {
			if period.ID == id {
				return period, nil
			}
		}
	} else {
		for _, period := range periods {
			if period.Name == identifier {
				return period, nil
			}
		}
	}
	return service.Period{}, apperror.New(command.notFoundCode, command.name+" not found")
}

func (command periodCommand) itemPath(id int64) string {
	return command.path + "/" + strconv.FormatInt(id, 10)
}

func (command periodCommand) editUsage() string {
	return fmt.Sprintf("usage: delta %s edit <%s-id-or-exact-name> [--name <name>] [--color <#rrggbb>] [--start <YYYY-MM-DD>] [--end <YYYY-MM-DD> | --ongoing] [--json]", command.name, command.name)
}

func periodEndLabel(period service.Period) string {
	if period.EndDate == nil {
		return "ongoing"
	}
	return *period.EndDate
}

func (command periodCommand) writeHelp(stdout io.Writer, subcommand string) error {
	var help string
	switch subcommand {
	case "list":
		help = fmt.Sprintf("usage: delta %s list [--json]\n", command.name)
		help += "  newest start date first.\n"
	case "add":
		help = fmt.Sprintf("usage: delta %s add <name> --color <#rrggbb> --start <YYYY-MM-DD> [--end <YYYY-MM-DD>] [--json]\n", command.name)
		help += "  dates are inclusive; without --end it is ongoing.\n  quote the color so the shell does not read # as a comment.\n"
	case "edit":
		help = command.editUsage() + "\n"
		help += fmt.Sprintf("  identifier accepts a numeric %s ID or an exact %s name.\n  only the given flags change; --ongoing clears the end date.\n", command.name, command.name)
	case "delete":
		help = fmt.Sprintf("usage: delta %s delete <%s-id-or-exact-name> [--json]\n", command.name, command.name)
		help += fmt.Sprintf("  identifier accepts a numeric %s ID or an exact %s name.\n", command.name, command.name)
	default:
		help = fmt.Sprintf("usage: delta %s list|add|edit|delete\n", command.name)
	}
	_, err := io.WriteString(stdout, help)
	return err
}
