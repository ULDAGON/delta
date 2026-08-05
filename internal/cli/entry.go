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

	apiclient "github.com/ferriskleier/delta/internal/client"
	"github.com/ferriskleier/delta/internal/service"
)

type optionalString struct {
	value string
	set   bool
}

func (f *optionalString) String() string { return f.value }
func (f *optionalString) Set(value string) error {
	f.value, f.set = value, true
	return nil
}

type optionalBool struct {
	value bool
	set   bool
}

func (f *optionalBool) String() string { return fmt.Sprintf("%t", f.value) }
func (*optionalBool) IsBoolFlag() bool { return true }
func (f *optionalBool) Set(value string) error {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	f.value, f.set = parsed, true
	return nil
}

type optionalInt struct {
	value int
	set   bool
}

func (f *optionalInt) String() string { return fmt.Sprintf("%d", f.value) }
func (f *optionalInt) Set(value string) error {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	f.value, f.set = parsed, true
	return nil
}

type optionalFloat struct {
	value float64
	set   bool
}

func (f *optionalFloat) String() string { return formatWorkHours(f.value) }
func (f *optionalFloat) Set(value string) error {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return err
	}
	f.value, f.set = parsed, true
	return nil
}

// formatWorkHours prints the shortest exact decimal, so 7.5 stays 7.5 and 8
// does not gain a trailing zero.
func formatWorkHours(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func runEntry(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: delta entry show|set|delete [date]")
	}
	subcommand := args[0]
	args = args[1:]
	switch subcommand {
	case "show":
		return runEntryShow(ctx, args, stdout)
	case "set":
		return runEntrySet(ctx, args, stdin, stdout)
	case "delete":
		return runEntryDelete(ctx, args, stdout)
	default:
		return fmt.Errorf("unknown entry command %q; try show, set, or delete", subcommand)
	}
}

func runEntryShow(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("delta entry show", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonFlag := flags.Bool("json", false, "write JSON")
	parsed, err := normalizeArgs(args, flags, atMostOneEntryPositional)
	if err != nil {
		return err
	}
	if err := flags.Parse(parsed); err != nil {
		return err
	}
	date, err := entryDate(flags.Args())
	if err != nil {
		return err
	}
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	status, body, err := client.Do(ctx, http.MethodGet, entryPath(date), nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, body)
	}
	if *jsonFlag {
		return writeBody(stdout, body)
	}
	var entry service.Entry
	if err := json.Unmarshal(body, &entry); err != nil {
		return fmt.Errorf("decode entry response: %w", err)
	}
	checkoffNames, err := loadHabitNames(ctx, client, entry.Checkoffs)
	if err != nil {
		return err
	}
	return writeHumanEntry(stdout, entry, checkoffNames)
}

func loadHabitNames(ctx context.Context, client *apiclient.Client, checkoffs []string) (map[string]string, error) {
	if len(checkoffs) == 0 {
		return nil, nil
	}
	status, body, err := client.Do(ctx, http.MethodGet, "/api/habits", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, apiclient.ErrorFromResponse(status, body)
	}
	var habits []service.Habit
	if err := json.Unmarshal(body, &habits); err != nil {
		return nil, fmt.Errorf("decode habits response: %w", err)
	}
	names := make(map[string]string, len(habits))
	for _, habit := range habits {
		names[strconv.FormatInt(habit.ID, 10)] = habit.Name
	}
	return names, nil
}

func writeHumanEntry(stdout io.Writer, entry service.Entry, checkoffNames map[string]string) error {
	if _, err := fmt.Fprintf(stdout, "Date: %s\nText: %s\n\nGoals:\n", entry.Date, displayEntryValue(entry.Text)); err != nil {
		return err
	}
	for index, goal := range entry.Goals {
		marker := " "
		if goal.Checked {
			marker = "x"
		}
		if _, err := fmt.Fprintf(stdout, "  %d. [%s] %s\n", index+1, marker, displayEntryValue(goal.Text)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(stdout, "\nGratitudes:"); err != nil {
		return err
	}
	for index, gratitude := range entry.Gratitudes {
		if _, err := fmt.Fprintf(stdout, "  %d. %s\n", index+1, displayEntryValue(gratitude)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(stdout, "\n3 Ws:\n  Went well: %s\n  Could have gone better: %s\n  Goal for tomorrow: %s\n\nRatings:\n", displayEntryValue(entry.Ws.WentWell), displayEntryValue(entry.Ws.CouldHaveGoneBetter), displayEntryValue(entry.Ws.GoalForTomorrow)); err != nil {
		return err
	}
	for _, rating := range []struct {
		name  string
		value *int
	}{
		{name: "Total", value: entry.Ratings.Total},
		{name: "Body", value: entry.Ratings.Body},
		{name: "Mind", value: entry.Ratings.Mind},
		{name: "Spirit", value: entry.Ratings.Spirit},
	} {
		value := "absent"
		if rating.value != nil {
			value = strconv.Itoa(*rating.value)
		}
		if _, err := fmt.Fprintf(stdout, "  %s: %s\n", rating.name, value); err != nil {
			return err
		}
	}
	workHours := "absent"
	if entry.WorkHours != nil {
		workHours = formatWorkHours(*entry.WorkHours)
	}
	if _, err := fmt.Fprintf(stdout, "\nWork hours: %s\n", workHours); err != nil {
		return err
	}
	pixel := "grey"
	switch entry.Pixel {
	case 1:
		pixel = "green"
	case 2:
		pixel = "orange"
	}
	if _, err := fmt.Fprintf(stdout, "\nPixel: %s\n", pixel); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, "\nCheck-offs:"); err != nil {
		return err
	}
	if len(entry.Checkoffs) == 0 {
		_, err := fmt.Fprintln(stdout, "  absent")
		return err
	}
	for _, checkoff := range entry.Checkoffs {
		label := checkoff
		if name, ok := checkoffNames[checkoff]; ok {
			label = name
		}
		if _, err := fmt.Fprintf(stdout, "  - %s\n", label); err != nil {
			return err
		}
	}
	return nil
}

func displayEntryValue(value string) string {
	if value == "" {
		return "absent"
	}
	return value
}

func runEntrySet(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	textFlag := optionalString{}
	goalText := make([]optionalString, 5)
	goalChecked := make([]optionalBool, 5)
	gratitude := make([]optionalString, 3)
	wentWell, couldImprove, tomorrow := optionalString{}, optionalString{}, optionalString{}
	total, body, mind, spirit := optionalInt{}, optionalInt{}, optionalInt{}, optionalInt{}
	pixel := optionalInt{}
	workHours := optionalFloat{}
	flags := flag.NewFlagSet("delta entry set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	registerOptional(flags, "text", &textFlag, "entry text; - reads stdin")
	for i := range goalText {
		index := i + 1
		registerOptional(flags, fmt.Sprintf("goal-%d", index), &goalText[i], "goal line text")
		registerOptional(flags, fmt.Sprintf("goal-%d-checked", index), &goalChecked[i], "goal checked state")
	}
	for i := range gratitude {
		index := i + 1
		registerOptional(flags, fmt.Sprintf("gratitude-%d", index), &gratitude[i], "gratitude line")
	}
	registerOptional(flags, "went-well", &wentWell, "what went well")
	registerOptional(flags, "could-have-gone-better", &couldImprove, "what could have gone better")
	registerOptional(flags, "goal-for-tomorrow", &tomorrow, "goal for tomorrow")
	registerOptional(flags, "total", &total, "total rating from 1 to 5")
	registerOptional(flags, "body", &body, "body rating from 1 to 5")
	registerOptional(flags, "mind", &mind, "mind rating from 1 to 5")
	registerOptional(flags, "spirit", &spirit, "spirit rating from 1 to 5")
	registerOptional(flags, "pixel", &pixel, "pixel marker 0 (grey), 1 (green), or 2 (orange)")
	registerOptional(flags, "work-hours", &workHours, "hours worked from 0 to 24, decimals allowed")
	jsonFlag := flags.Bool("json", false, "write JSON")

	parsed, err := normalizeArgs(args, flags, atMostOneEntryPositional)
	if err != nil {
		return err
	}
	if err := flags.Parse(parsed); err != nil {
		return err
	}
	date, err := entryDate(flags.Args())
	if err != nil {
		return err
	}
	if textFlag.set && textFlag.value == "-" {
		piped, err := io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("read text from stdin: %w", err)
		}
		textFlag.value = string(piped)
	}

	patch := make(map[string]any)
	if textFlag.set {
		patch["text"] = textFlag.value
	}
	needsGoals := false
	for i := range goalText {
		needsGoals = needsGoals || goalText[i].set || goalChecked[i].set
	}
	needsGratitudes := false
	for i := range gratitude {
		needsGratitudes = needsGratitudes || gratitude[i].set
	}
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	var existing service.Entry
	if needsGoals || needsGratitudes {
		status, bodyBytes, requestErr := client.Do(ctx, http.MethodGet, entryPath(date), nil)
		if requestErr != nil {
			return requestErr
		}
		if status == http.StatusOK {
			if err := json.Unmarshal(bodyBytes, &existing); err != nil {
				return fmt.Errorf("decode existing entry: %w", err)
			}
		} else if status != http.StatusNotFound {
			return apiclient.ErrorFromResponse(status, bodyBytes)
		}
	}
	if needsGoals {
		goals := append([]service.Goal(nil), existing.Goals...)
		if len(goals) != 5 {
			goals = make([]service.Goal, 5)
		}
		for i := range goals {
			if goalText[i].set {
				goals[i].Text = goalText[i].value
			}
			if goalChecked[i].set {
				goals[i].Checked = goalChecked[i].value
			}
		}
		patch["goals"] = goals
	}
	if needsGratitudes {
		gratitudes := append([]string(nil), existing.Gratitudes...)
		if len(gratitudes) != 3 {
			gratitudes = make([]string, 3)
		}
		for i := range gratitude {
			if gratitude[i].set {
				gratitudes[i] = gratitude[i].value
			}
		}
		patch["gratitudes"] = gratitudes
	}
	ws := map[string]any{}
	if wentWell.set {
		ws["went_well"] = wentWell.value
	}
	if couldImprove.set {
		ws["could_have_gone_better"] = couldImprove.value
	}
	if tomorrow.set {
		ws["goal_for_tomorrow"] = tomorrow.value
	}
	if len(ws) > 0 {
		patch["ws"] = ws
	}
	ratings := map[string]any{}
	if total.set {
		ratings["total"] = total.value
	}
	if body.set {
		ratings["body"] = body.value
	}
	if mind.set {
		ratings["mind"] = mind.value
	}
	if spirit.set {
		ratings["spirit"] = spirit.value
	}
	if len(ratings) > 0 {
		patch["ratings"] = ratings
	}
	if pixel.set {
		if pixel.value < 0 || pixel.value > 2 {
			return errors.New("pixel must be 0 (grey), 1 (green), or 2 (orange)")
		}
		patch["pixel"] = pixel.value
	}
	if workHours.set {
		if workHours.value < 0 || workHours.value > service.WorkHoursMax {
			return errors.New("work hours must be between 0 and 24")
		}
		patch["work_hours"] = workHours.value
	}
	bodyBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("encode entry update: %w", err)
	}
	status, responseBody, err := client.Do(ctx, http.MethodPut, entryPath(date), bodyBytes)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, responseBody)
	}
	if *jsonFlag {
		return writeBody(stdout, responseBody)
	}
	_, err = fmt.Fprintf(stdout, "entry %s saved\n", date)
	return err
}

func runEntryDelete(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("delta entry delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonFlag := flags.Bool("json", false, "write JSON")
	parsed, err := normalizeArgs(args, flags, atMostOneEntryPositional)
	if err != nil {
		return err
	}
	if err := flags.Parse(parsed); err != nil {
		return err
	}
	date, err := entryDate(flags.Args())
	if err != nil {
		return err
	}
	client, err := newAPIClient()
	if err != nil {
		return err
	}
	status, body, err := client.Do(ctx, http.MethodDelete, entryPath(date), nil)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusOK {
		return apiclient.ErrorFromResponse(status, body)
	}
	if *jsonFlag {
		return writeBody(stdout, []byte(fmt.Sprintf(`{"ok":true,"date":%q}`, date)))
	}
	_, err = fmt.Fprintf(stdout, "entry %s deleted\n", date)
	return err
}

func entryDate(args []string) (string, error) {
	if len(args) > 1 {
		return "", errors.New("entry accepts at most one date")
	}
	if len(args) == 1 {
		if err := service.ValidateDate(args[0]); err != nil {
			return "", err
		}
		return args[0], nil
	}
	return time.Now().Format("2006-01-02"), nil
}

func entryPath(date string) string { return "/api/entries/" + url.PathEscape(date) }

func registerOptional(flags *flag.FlagSet, name string, target flag.Value, usage string) {
	flags.Var(target, name, usage)
}

// normalizeArgs lets documented positional arguments appear before or after
// flags, while still letting flag.Value consume values such as "-".
func normalizeArgs(args []string, flags *flag.FlagSet, checkPositionalCount func(int) error) ([]string, error) {
	normalizedFlags, positional := make([]string, 0, len(args)), make([]string, 0, 1)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		normalizedFlags = append(normalizedFlags, arg)
		name := strings.TrimLeft(arg, "-")
		if equal := strings.IndexByte(name, '='); equal >= 0 {
			name = name[:equal]
		}
		definition := flagDefinition(flags, name)
		if !strings.Contains(arg, "=") && flagNeedsValue(definition) {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag needs a value: --%s", name)
			}
			i++
			normalizedFlags = append(normalizedFlags, args[i])
		}
	}
	if checkPositionalCount != nil {
		if err := checkPositionalCount(len(positional)); err != nil {
			return nil, err
		}
	}
	return append(normalizedFlags, positional...), nil
}

func atMostOneEntryPositional(count int) error {
	if count > 1 {
		return errors.New("entry accepts at most one date")
	}
	return nil
}

func flagDefinition(flags *flag.FlagSet, name string) *flag.Flag {
	if flags == nil {
		return nil
	}
	return flags.Lookup(name)
}

func flagNeedsValue(definition *flag.Flag) bool {
	if definition == nil {
		return true
	}
	if boolFlag, ok := definition.Value.(interface{ IsBoolFlag() bool }); ok && boolFlag.IsBoolFlag() {
		return false
	}
	return true
}

func newAPIClient() (*apiclient.Client, error) { return apiclient.FromConfig() }

func writeBody(writer io.Writer, body []byte) error {
	if len(body) == 0 || body[len(body)-1] != '\n' {
		body = append(body, '\n')
	}
	_, err := writer.Write(body)
	return err
}
