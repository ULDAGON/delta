package service

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestSanitizeSearchQueryBoundsTermsAndLength(t *testing.T) {
	longTerm := sanitizeSearchQuery(strings.Repeat("needle", maxSearchQueryLength*8))
	if len(longTerm) > maxSearchQueryLength {
		t.Fatalf("sanitized long term length = %d, want at most %d", len(longTerm), maxSearchQueryLength)
	}
	if !strings.HasSuffix(longTerm, "*") {
		t.Fatalf("sanitized long term = %q, want a final prefix marker", longTerm)
	}

	manyTerms := sanitizeSearchQuery(strings.Repeat("needle ", maxSearchTerms) + "ignored")
	if strings.Contains(manyTerms, "ignored") {
		t.Fatalf("sanitized terms = %q, unexpectedly included terms after the cap", manyTerms)
	}
	if got := strings.Count(manyTerms, `"needle"`); got != maxSearchTerms {
		t.Fatalf("sanitized term count = %d, want %d", got, maxSearchTerms)
	}
}

func TestPersistEntryUpdatesOnlyPatchedColumns(t *testing.T) {
	patch := EntryPatch{Ratings: RatingsPatch{
		Total: OptionalRating{Set: true},
	}}
	columns := entryUpdateColumns(patch)
	if len(columns) != 1 || columns[0] != "rating_total" {
		t.Fatalf("rating update columns = %#v, want only rating_total", columns)
	}

	writer := &recordingExecer{}
	if err := persistEntry(context.Background(), writer, blankEntry("2026-08-01"), columns...); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(writer.query, "rating_total=excluded.rating_total") {
		t.Fatalf("persist query = %q, missing rating update", writer.query)
	}
	if strings.Contains(writer.query, "text=excluded.text") || strings.Contains(writer.query, "checkoffs=excluded.checkoffs") {
		t.Fatalf("persist query = %q, unexpectedly updates searchable columns", writer.query)
	}
}

type recordingExecer struct {
	query string
}

func (r *recordingExecer) ExecContext(_ context.Context, query string, _ ...any) (sql.Result, error) {
	r.query = query
	return recordingResult{}, nil
}

type recordingResult struct{}

func (recordingResult) LastInsertId() (int64, error) { return 0, nil }
func (recordingResult) RowsAffected() (int64, error) { return 0, nil }
