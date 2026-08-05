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
	"time"

	"github.com/ferriskleier/delta/internal/apperror"
	apiclient "github.com/ferriskleier/delta/internal/client"
	"github.com/ferriskleier/delta/internal/service"
)

func runHabit(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: delta habit list|add|check|uncheck|archive")
	}
	if isHelpArg(args[0]) {
		return writeHabitHelp(stdout, "")
	}
	subcommand := args[0]
	args = args[1:]
	switch subcommand {
	case "list":
		return runHabitList(ctx, args, stdout)
	case "add":
		return runHabitAdd(ctx, args, stdout)
	case "check":
		return runHabitCheckoff(ctx, args, stdout, true)
	case "uncheck":
		return runHabitCheckoff(ctx, args, stdout, false)
	case "archive":
		return runHabitArchive(ctx, args, stdout)
	default:
		return fmt.Errorf("unknown habit command %q; try list, add, check, uncheck, or archive", subcommand)
	}
}

func runHabitList(ctx context.Context, args []string, stdout io.Writer) error {
	if containsHelpArg(args) {
		return writeHabitHelp(stdout, "list")
	}
	flags := flag.NewFlagSet("delta habit list", flag.ContinueOnError)
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
		return errors.New("habit list accepts no positional arguments")
	}
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	status, body, err := client.Do(ctx, http.MethodGet, "/api/habits", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, body)
	}
	if *jsonFlag {
		return writeBody(stdout, body)
	}
	var habits []service.Habit
	if err := json.Unmarshal(body, &habits); err != nil {
		return fmt.Errorf("decode habits response: %w", err)
	}
	for _, habit := range habits {
		if _, err := fmt.Fprintf(stdout, "%d. %s (id %d)\n", habit.Position+1, habit.Name, habit.ID); err != nil {
			return err
		}
	}
	return nil
}

func runHabitAdd(ctx context.Context, args []string, stdout io.Writer) error {
	if containsHelpArg(args) {
		return writeHabitHelp(stdout, "add")
	}
	flags := flag.NewFlagSet("delta habit add", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	nameFlag := optionalString{}
	registerOptional(flags, "name", &nameFlag, "habit name")
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
		return errors.New("habit add accepts a name either as an argument or with --name, not both")
	}
	if name == "" {
		return errors.New("usage: delta habit add <name> [--json]")
	}
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return fmt.Errorf("encode habit: %w", err)
	}
	status, responseBody, err := client.Do(ctx, http.MethodPost, "/api/habits", body)
	if err != nil {
		return err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, responseBody)
	}
	if *jsonFlag {
		return writeBody(stdout, responseBody)
	}
	var habit service.Habit
	if err := json.Unmarshal(responseBody, &habit); err != nil {
		return fmt.Errorf("decode habit response: %w", err)
	}
	_, err = fmt.Fprintf(stdout, "habit %d %q added\n", habit.ID, habit.Name)
	return err
}

func runHabitCheckoff(ctx context.Context, args []string, stdout io.Writer, checked bool) error {
	command := "check"
	method := http.MethodPost
	if !checked {
		command = "uncheck"
		method = http.MethodDelete
	}
	if containsHelpArg(args) {
		return writeHabitHelp(stdout, command)
	}
	flags := flag.NewFlagSet("delta habit "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dateFlag := flags.String("date", "", "entry date (default today)")
	habitFlag := optionalString{}
	registerOptional(flags, "habit", &habitFlag, "habit id or exact name")
	jsonFlag := flags.Bool("json", false, "write JSON")
	parsed, err := normalizeArgs(args, flags, nil)
	if err != nil {
		return err
	}
	if err := flags.Parse(parsed); err != nil {
		return err
	}
	identifier, date, err := parseHabitActionArgs(flags.Args(), *dateFlag, habitFlag)
	if err != nil {
		return err
	}
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	habit, err := resolveHabit(ctx, client, identifier)
	if err != nil {
		return err
	}
	status, responseBody, err := client.Do(ctx, method, checkoffPath(date, habit.ID), nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		return apiclient.ErrorFromResponse(status, responseBody)
	}
	if *jsonFlag {
		if status == http.StatusNoContent && len(responseBody) == 0 {
			return writeBody(stdout, []byte(`{"ok":true}`))
		}
		return writeBody(stdout, responseBody)
	}
	verb := "checked"
	if !checked {
		verb = "unchecked"
	}
	_, err = fmt.Fprintf(stdout, "habit %q %s on %s\n", habit.Name, verb, date)
	return err
}

func runHabitArchive(ctx context.Context, args []string, stdout io.Writer) error {
	if containsHelpArg(args) {
		return writeHabitHelp(stdout, "archive")
	}
	flags := flag.NewFlagSet("delta habit archive", flag.ContinueOnError)
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
		return errors.New("usage: delta habit archive <habit-id-or-exact-name> [--json]")
	}
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	habit, err := resolveHabit(ctx, client, flags.Args()[0])
	if err != nil {
		return err
	}
	status, body, err := client.Do(ctx, http.MethodPatch, habitPath(habit.ID), []byte(`{"archived":true}`))
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, body)
	}
	if *jsonFlag {
		return writeBody(stdout, body)
	}
	_, err = fmt.Fprintf(stdout, "habit %q archived\n", habit.Name)
	return err
}

func parseHabitActionArgs(args []string, dateFlag string, habitFlag optionalString) (string, string, error) {
	if len(args) > 2 {
		return "", "", errors.New("habit check accepts a habit and optional date; use --date to be explicit")
	}
	identifier := strings.TrimSpace(habitFlag.value)
	date := strings.TrimSpace(dateFlag)
	if len(args) == 2 {
		if service.ValidateDate(args[0]) == nil {
			if date != "" {
				return "", "", errors.New("date specified twice")
			}
			date, identifier = args[0], args[1]
		} else if service.ValidateDate(args[1]) == nil {
			if date != "" {
				return "", "", errors.New("date specified twice")
			}
			identifier, date = args[0], args[1]
		} else {
			return "", "", errors.New("habit check needs one habit and one YYYY-MM-DD date")
		}
	} else if len(args) == 1 {
		if service.ValidateDate(args[0]) == nil {
			return "", "", errors.New("habit identifier is required")
		}
		if identifier != "" {
			return "", "", errors.New("habit specified twice")
		}
		identifier = args[0]
	}
	if identifier == "" {
		return "", "", errors.New("habit identifier is required (exact name or id)")
	}
	if date == "" {
		date = time.Now().In(time.Local).Format("2006-01-02")
	}
	if err := service.ValidateDate(date); err != nil {
		return "", "", err
	}
	return identifier, date, nil
}

func resolveHabit(ctx context.Context, client *apiclient.Client, identifier string) (service.Habit, error) {
	status, body, err := client.Do(ctx, http.MethodGet, "/api/habits", nil)
	if err != nil {
		return service.Habit{}, err
	}
	if status != http.StatusOK {
		return service.Habit{}, apiclient.ErrorFromResponse(status, body)
	}
	var habits []service.Habit
	if err := json.Unmarshal(body, &habits); err != nil {
		return service.Habit{}, fmt.Errorf("decode habits response: %w", err)
	}
	identifier = strings.TrimSpace(identifier)
	if id, err := strconv.ParseInt(identifier, 10, 64); err == nil {
		for _, habit := range habits {
			if habit.ID == id {
				return habit, nil
			}
		}
	} else {
		for _, habit := range habits {
			if habit.Name == identifier {
				return habit, nil
			}
		}
	}
	return service.Habit{}, apperror.New(apperror.CodeHabitNotFound, "habit not found")
}

func habitPath(id int64) string { return "/api/habits/" + strconv.FormatInt(id, 10) }

func checkoffPath(date string, habitID int64) string {
	return "/api/entries/" + url.PathEscape(date) + "/checkoffs/" + strconv.FormatInt(habitID, 10)
}

func isHelpArg(value string) bool { return value == "--help" || value == "-h" }

func containsHelpArg(args []string) bool {
	for _, arg := range args {
		if isHelpArg(arg) {
			return true
		}
	}
	return false
}

func writeHabitHelp(stdout io.Writer, command string) error {
	var help string
	switch command {
	case "list":
		help = "usage: delta habit list [--json]\n"
	case "add":
		help = "usage: delta habit add <name> [--json]\n"
	case "check", "uncheck":
		help = fmt.Sprintf("usage: delta habit %s <habit-id-or-exact-name> [date] [--json]\n", command)
		help += "  identifier accepts a numeric habit ID or an exact habit name.\n  date defaults to the server's local today; use --date to override it.\n"
	case "archive":
		help = "usage: delta habit archive <habit-id-or-exact-name> [--json]\n"
		help += "  identifier accepts a numeric habit ID or an exact habit name.\n"
	default:
		help = "usage: delta habit list|add|check|uncheck|archive\n"
	}
	_, err := io.WriteString(stdout, help)
	return err
}
