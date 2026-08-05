package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/ferriskleier/delta/internal/service"
)

// ParsedEntry is one journal-file entry in DELTA's field vocabulary.
type ParsedEntry struct {
	Date                string
	Text                string
	Goals               []service.Goal
	Gratitudes          []string
	WentWell            string
	CouldHaveGoneBetter string
	GoalForTomorrow     string
}

var (
	delimiterPattern = regexp.MustCompile(`^[—-]{5,}$`)
	goalPattern      = regexp.MustCompile(`^\[(.?)\]\s*(.*)$`)
	// Some early entries carry a bracketed number after the date; it is noise.
	dateSuffixPattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\s*\(\d+\)$`)
)

const (
	sectionGratitudes = "i'm grateful for:"
	sectionGoals      = "daily goals:"
	sectionWentWell   = "what went well today?"
	sectionBetter     = "what could have gone better today?"
	sectionTomorrow   = "what are my goals for tomorrow?"
)

// parseJournal splits the plain-text journal into entries. Malformed blocks
// are reported as errors and skipped; well-formed blocks still import.
func parseJournal(r io.Reader) ([]ParsedEntry, []error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var blocks [][]string
	var current []string
	for scanner.Scan() {
		line := scanner.Text()
		if delimiterPattern.MatchString(strings.TrimSpace(line)) {
			if len(current) > 0 {
				blocks = append(blocks, current)
			}
			current = nil
			continue
		}
		current = append(current, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, []error{fmt.Errorf("read journal: %w", err)}
	}
	if len(current) > 0 {
		blocks = append(blocks, current)
	}

	var entries []ParsedEntry
	var errs []error
	for index, block := range blocks {
		entry, err := parseBlock(block)
		if err != nil {
			errs = append(errs, fmt.Errorf("block %d: %w", index+1, err))
			continue
		}
		if entry != nil {
			entries = append(entries, *entry)
		}
	}
	return entries, errs
}

var weekdayNames = map[string]bool{
	"monday": true, "tuesday": true, "wednesday": true, "thursday": true,
	"friday": true, "saturday": true, "sunday": true,
}

func isWeekdayName(line string) bool {
	return weekdayNames[strings.ToLower(line)]
}

// parseBlock returns nil, nil for blocks that contain nothing but blanks.
func parseBlock(lines []string) (*ParsedEntry, error) {
	position := 0
	skipBlanks := func() {
		for position < len(lines) && strings.TrimSpace(lines[position]) == "" {
			position++
		}
	}

	skipBlanks()
	if position == len(lines) {
		return nil, nil
	}
	date := strings.TrimSpace(lines[position])
	if match := dateSuffixPattern.FindStringSubmatch(date); match != nil {
		date = match[1]
	}
	if err := service.ValidateDate(date); err != nil {
		return nil, fmt.Errorf("expected a YYYY-MM-DD date, got %q", date)
	}
	position++

	// The weekday line under the date is decorative and not present in every
	// era of the journal; it is consumed only when it really is a weekday.
	skipBlanks()
	if position < len(lines) && isWeekdayName(strings.TrimSpace(lines[position])) {
		position++
	}

	entry := ParsedEntry{Date: date}
	section := ""
	var textLines []string
	flushText := func(collected []string) string {
		return strings.TrimSpace(strings.Join(collected, "\n"))
	}
	var sectionLines []string
	closeSection := func() error {
		switch section {
		case "":
			entry.Text = flushText(textLines)
		case sectionWentWell:
			entry.WentWell = flushText(sectionLines)
		case sectionBetter:
			entry.CouldHaveGoneBetter = flushText(sectionLines)
		case sectionTomorrow:
			entry.GoalForTomorrow = flushText(sectionLines)
		}
		sectionLines = nil
		return nil
	}

	for ; position < len(lines); position++ {
		line := lines[position]
		trimmed := strings.TrimSpace(line)
		lowered := strings.ToLower(trimmed)
		switch lowered {
		case sectionGratitudes, sectionGoals, sectionWentWell, sectionBetter, sectionTomorrow:
			if err := closeSection(); err != nil {
				return nil, err
			}
			section = lowered
			continue
		}
		switch section {
		case "":
			textLines = append(textLines, line)
		case sectionGratitudes:
			if trimmed == "" {
				continue
			}
			if len(entry.Gratitudes) == 3 {
				return nil, fmt.Errorf("%s: more than three gratitude lines", date)
			}
			entry.Gratitudes = append(entry.Gratitudes, trimmed)
		case sectionGoals:
			if trimmed == "" {
				continue
			}
			match := goalPattern.FindStringSubmatch(trimmed)
			if match == nil {
				return nil, fmt.Errorf("%s: goal line %q does not start with [x] or [-]", date, trimmed)
			}
			if len(entry.Goals) == 5 {
				return nil, fmt.Errorf("%s: more than five goal lines", date)
			}
			entry.Goals = append(entry.Goals, service.Goal{
				Text:    strings.TrimSpace(match[2]),
				Checked: strings.EqualFold(match[1], "x"),
			})
		default:
			sectionLines = append(sectionLines, line)
		}
	}
	if err := closeSection(); err != nil {
		return nil, err
	}
	return &entry, nil
}
