package main

import (
	"strings"
	"testing"
)

const sampleJournal = `—————————————————————————————————————————————————————————————————————————————————————
2026-07-19
Sunday

no entry, low spirit

Daily Goals:
[x] Continue cutting video
[x] Watch Gronkh / world cup finale
[x] No relapse
[-] Workout
[-] Latin

—————————————————————————————————————————————————————————————————————————————————————
2026-07-17
Friday

I did not write the last two days. On Friday I worked some more and enjoyed the stream.

Daily Goals:
[x] Work for 6 hours
[x] watch Gronkh
[x] No relapse
[x] Play some Minecraft
[-] Workout

—————————————————————————————————————————————————————————————————————————————————————
2026-07-16
Thursday

Today I did not have such a faithful day.

I'm Grateful for:
Having a working spot in the city
Relaxing at Zereth'dar
Playing more Minecraft

Daily Goals:
[x] Work no more than 6 hours
[x] Play more Minecraft
[-] Read more
[-] Latin
[-] Workout

What went well today?
I enjoyed working in the city.

What could have gone better today?
I relapsed twice, although I had such a good streak. But now it's time to repeat that streak and grow it more.

What are my goals for tomorrow?
Enjoying the start into the weekend.
`

func TestParseJournalSample(t *testing.T) {
	entries, errs := parseJournal(strings.NewReader(sampleJournal))
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if len(entries) != 3 {
		t.Fatalf("parsed %d entries, want 3", len(entries))
	}

	first := entries[0]
	if first.Date != "2026-07-19" {
		t.Errorf("first date = %q, want 2026-07-19", first.Date)
	}
	if first.Text != "no entry, low spirit" {
		t.Errorf("first text = %q", first.Text)
	}
	if len(first.Goals) != 5 {
		t.Fatalf("first goals = %d, want 5", len(first.Goals))
	}
	if !first.Goals[0].Checked || first.Goals[0].Text != "Continue cutting video" {
		t.Errorf("first goal = %+v", first.Goals[0])
	}
	if first.Goals[3].Checked || first.Goals[3].Text != "Workout" {
		t.Errorf("fourth goal = %+v", first.Goals[3])
	}
	if len(first.Gratitudes) != 0 {
		t.Errorf("first gratitudes = %v, want none", first.Gratitudes)
	}
	if first.WentWell != "" || first.CouldHaveGoneBetter != "" || first.GoalForTomorrow != "" {
		t.Errorf("first entry has unexpected 3W content: %+v", first)
	}

	third := entries[2]
	if third.Date != "2026-07-16" {
		t.Errorf("third date = %q, want 2026-07-16", third.Date)
	}
	if third.Text != "Today I did not have such a faithful day." {
		t.Errorf("third text = %q", third.Text)
	}
	wantGratitudes := []string{
		"Having a working spot in the city",
		"Relaxing at Zereth'dar",
		"Playing more Minecraft",
	}
	if len(third.Gratitudes) != 3 {
		t.Fatalf("third gratitudes = %v", third.Gratitudes)
	}
	for i, want := range wantGratitudes {
		if third.Gratitudes[i] != want {
			t.Errorf("gratitude %d = %q, want %q", i, third.Gratitudes[i], want)
		}
	}
	if third.WentWell != "I enjoyed working in the city." {
		t.Errorf("went well = %q", third.WentWell)
	}
	if !strings.HasPrefix(third.CouldHaveGoneBetter, "I relapsed twice") {
		t.Errorf("could have gone better = %q", third.CouldHaveGoneBetter)
	}
	if third.GoalForTomorrow != "Enjoying the start into the weekend." {
		t.Errorf("goal for tomorrow = %q", third.GoalForTomorrow)
	}
}

func TestParseJournalMultiParagraphText(t *testing.T) {
	input := "———————————\n2026-01-02\nFriday\n\nFirst paragraph.\n\nSecond paragraph.\n\nDaily Goals:\n[x] One\n"
	entries, errs := parseJournal(strings.NewReader(input))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(entries) != 1 {
		t.Fatalf("parsed %d entries, want 1", len(entries))
	}
	want := "First paragraph.\n\nSecond paragraph."
	if entries[0].Text != want {
		t.Errorf("text = %q, want %q", entries[0].Text, want)
	}
	if len(entries[0].Goals) != 1 || !entries[0].Goals[0].Checked {
		t.Errorf("goals = %+v", entries[0].Goals)
	}
}

func TestParseJournalRejectsTooManyGoals(t *testing.T) {
	input := "———————————\n2026-01-02\nFriday\n\nText.\n\nDaily Goals:\n[x] a\n[x] b\n[x] c\n[x] d\n[x] e\n[x] f\n"
	entries, errs := parseJournal(strings.NewReader(input))
	if len(errs) == 0 {
		t.Fatal("want an error for six goals")
	}
	if len(entries) != 0 {
		t.Fatalf("entry with too many goals must be skipped, got %+v", entries)
	}
}

func TestParseJournalRejectsBadDate(t *testing.T) {
	input := "———————————\nnot-a-date\nFriday\n\nText.\n"
	entries, errs := parseJournal(strings.NewReader(input))
	if len(errs) == 0 {
		t.Fatal("want an error for a bad date")
	}
	if len(entries) != 0 {
		t.Fatalf("block with bad date must be skipped, got %+v", entries)
	}
}

func TestParseJournalWeekdayLineIsOptional(t *testing.T) {
	input := "———————————\n2022-10-26\n\nStraight into the text, no weekday.\n\nDaily Goals:\n[x] One\n"
	entries, errs := parseJournal(strings.NewReader(input))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(entries) != 1 {
		t.Fatalf("parsed %d entries, want 1", len(entries))
	}
	if entries[0].Text != "Straight into the text, no weekday." {
		t.Errorf("text = %q", entries[0].Text)
	}
	if len(entries[0].Goals) != 1 {
		t.Errorf("goals = %+v", entries[0].Goals)
	}
}

func TestParseJournalDateWithBracketedNumber(t *testing.T) {
	input := "———————————\n2022-02-07 (5)\n\nSome text.\n"
	entries, errs := parseJournal(strings.NewReader(input))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(entries) != 1 || entries[0].Date != "2022-02-07" {
		t.Fatalf("entries = %+v, want one entry dated 2022-02-07", entries)
	}
	if entries[0].Text != "Some text." {
		t.Errorf("text = %q", entries[0].Text)
	}
}

func TestParseJournalTextStartingWithLoneWordIsKept(t *testing.T) {
	input := "———————————\n2022-10-26\n\nExhausted.\nLong day.\n"
	entries, errs := parseJournal(strings.NewReader(input))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if entries[0].Text != "Exhausted.\nLong day." {
		t.Errorf("text = %q", entries[0].Text)
	}
}

func TestParseJournalUppercaseCheckmark(t *testing.T) {
	input := "———————————\n2026-01-02\nFriday\n\nText.\n\nDaily Goals:\n[X] shout\n[ ] rest\n"
	entries, errs := parseJournal(strings.NewReader(input))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	goals := entries[0].Goals
	if len(goals) != 2 || !goals[0].Checked || goals[1].Checked {
		t.Errorf("goals = %+v", goals)
	}
}
