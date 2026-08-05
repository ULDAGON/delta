package service

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	searchschema "github.com/ferriskleier/delta/internal/search"
)

// SearchResult is one matching entry field and its highlighted FTS snippet.
type SearchResult struct {
	Date    string `json:"date"`
	Field   string `json:"field"`
	Snippet string `json:"snippet"`
}

const (
	maxSearchTerms         = 16
	maxSearchQueryLength   = 512
	searchSnippetWords     = 12
	searchSnippetStartMark = "\x01"
	searchSnippetEndMark   = "\x02"
	searchPublicStartMark  = "<mark>"
	searchPublicEndMark    = "</mark>"
)

// Search turns human input into the only FTS5 syntax DELTA uses internally:
// quoted terms joined by AND, with a prefix marker on the final term. Every
// punctuation rune is a separator, so users cannot inject FTS5 operators.
func (s *Service) Search(ctx context.Context, input string) ([]SearchResult, error) {
	query := sanitizeSearchQuery(input)
	if query == "" {
		return []SearchResult{}, nil
	}

	selectColumns := make([]string, 0, len(searchschema.Fields)+1)
	selectColumns = append(selectColumns, "date")
	for index := range searchschema.Fields {
		selectColumns = append(selectColumns, fmt.Sprintf(
			"snippet(fts, %d, '%s', '%s', '…', %d)",
			index+1, searchSnippetStartMark, searchSnippetEndMark, searchSnippetWords,
		))
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT `+strings.Join(selectColumns, ", ")+` FROM fts WHERE fts MATCH ? ORDER BY date DESC`, query)
	if err != nil {
		return nil, fmt.Errorf("search entries: %w", err)
	}
	defer rows.Close()

	results := make([]SearchResult, 0)
	for rows.Next() {
		var date string
		snippets := make([]string, len(searchschema.Fields))
		dest := make([]any, 0, len(searchschema.Fields)+1)
		dest = append(dest, &date)
		for index := range snippets {
			dest = append(dest, &snippets[index])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("read search result: %w", err)
		}
		for index, snippet := range snippets {
			if !strings.Contains(snippet, searchSnippetStartMark) {
				continue
			}
			results = append(results, SearchResult{
				Date:    date,
				Field:   searchschema.Fields[index].Label,
				Snippet: serializeSnippet(snippet),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search entries: %w", err)
	}
	return results, nil
}

func sanitizeSearchQuery(input string) string {
	terms := make([]string, 0, maxSearchTerms)
	var term strings.Builder
	for _, r := range input {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if term.Len() < maxSearchQueryLength {
				term.WriteRune(r)
			}
			if term.Len() >= maxSearchQueryLength {
				break
			}
			continue
		}
		if term.Len() == 0 {
			continue
		}
		terms = append(terms, term.String())
		term.Reset()
		if len(terms) == maxSearchTerms {
			break
		}
	}
	if len(terms) < maxSearchTerms && term.Len() > 0 {
		terms = append(terms, term.String())
	}
	if len(terms) == 0 {
		return ""
	}

	quoted := make([]string, 0, len(terms))
	queryLength := 1 // Reserve one byte for the final prefix marker.
	for _, term := range terms {
		separatorLength := 0
		if len(quoted) > 0 {
			separatorLength = len(" AND ")
		}
		available := maxSearchQueryLength - queryLength - separatorLength - 2
		if available <= 0 {
			break
		}
		if len(term) > available {
			term = truncateUTF8(term, available)
		}
		if term == "" {
			break
		}
		quoted = append(quoted, `"`+term+`"`)
		queryLength += separatorLength + len(term) + 2
	}
	if len(quoted) == 0 {
		return ""
	}
	quoted[len(quoted)-1] += "*"
	return strings.Join(quoted, " AND ")
}

func serializeSnippet(snippet string) string {
	escaped := strings.NewReplacer(
		searchPublicStartMark, "&lt;mark&gt;",
		searchPublicEndMark, "&lt;/mark&gt;",
	).Replace(snippet)
	return strings.NewReplacer(
		searchSnippetStartMark, searchPublicStartMark,
		searchSnippetEndMark, searchPublicEndMark,
	).Replace(escaped)
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && !utf8.RuneStart(value[maxBytes]) {
		maxBytes--
	}
	return value[:maxBytes]
}
