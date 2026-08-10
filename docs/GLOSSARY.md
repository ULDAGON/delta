# DELTA — Ubiquitous Language

## Glossary

- **Entry** — the diary record for one calendar date. At most one entry per date.
- **Entry date** — the plain calendar date keying an entry. "Today" rolls over at local midnight; no timezone is stored or considered.
- **Goal checklist** — the five goals belonging to one day, checked off during that day. Authored directly on their own day — typically the evening before, via the next-day draft flow — never copied from or stored on any other day. Always editable.
- **Next-day draft flow** — after saving day N's entry, the app immediately opens a draft entry for day N+1 where its five goals are set. It feels like part of finishing day N, but everything typed there belongs to day N+1.
- **Draft entry** — any partially-filled entry (e.g., only the goal checklist set the night before, or a half-finished evening write-up). Not a stored state: completeness is derived from which fields are present.
- **Closing the day** — the Save on the entry wizard's final step. It does not persist data (auto-save already has); it is the ritual that ends the day and rolls into the next-day draft flow. The Save in the wizard footer is not that ritual: it only dismisses the wizard, leaving the writing to auto-save.
- **Freeform text** — the entry's main diary prose.
- **Gratitudes** — three single-line "grateful for" items per entry.
- **3 Ws** — three reflective single-line prompts per entry: *What went well today? What could have gone better? What is my goal for tomorrow?* Free text; the third W is not linked to the goal checklist.
- **Ratings** — the entry's four 1–5 scores: Total, Body, Mind, Spirit. All four are entered independently; Total is a felt, holistic verdict on the day, never derived from the other three.
- **Habit** — a daily-checkable label ("Meditate", "Gym") with no schedule and no weight. Every active habit applies every day; all habits count equally. A habit keeps one identity across renames and re-activations; renaming applies everywhere, including past days (typo-fix semantics). Changing what a habit *means* ("Read 30 minutes" → "Read 1 hour") is done by archiving the old habit and creating a new one — validity ranges keep old entries showing the old habit and new entries the new one. Habits are managed in settings and shown in a manually set order.
- **Habit validity range** — an `active_from`/`active_to` date span during which a habit is active. A habit owns one or more ranges (re-activating an archived habit opens a new one); it applies to day D iff D falls inside any range; archiving closes the open range with the archive day itself still active. Ranges are editable in habit settings (e.g., backdating `active_from` so backfilled entries can include the habit); a check-off on a day outside the habit's ranges doesn't count and isn't shown, but is not deleted. Days derive their habit list from these ranges — nothing is copied per day.
- **Habit check-offs** — per-entry marks of which habits were achieved that day.
- **Daily habit score** — checked ÷ active × 100 for one day, derived, never stored. A day with zero active habits has no score; stats and trends skip such days.
- **Pixel grid** — DELTA's primary navigation surface: a GitHub-style contribution grid for the whole selected calendar year, where each pixel is one calendar date, columns are Monday-start weeks, and a year selector picks the visible calendar year. A toggle switches what a pixel's color encodes: **rating view** (the entry's Total rating) or **habit view** (the daily habit score, in 5% buckets). Both encodings draw from a per-diary palette that can be customized and reset. Holding a key replaces the encoding while it is held: **phase hold** paints each entry's phase marker, **journal hold** shows only which dates carry journal text. All no-data days — skipped past days, days before the first entry, and future days — look identical and are clickable; today's pixel carries a marker. The year rail runs from the earliest entry's year through the current year.
- **Instance** — one running DELTA process bound to one configured database file. Instances are standalone and never know each other; two instances share data only by pointing at the same database file with the same key. An instance offers a *human surface* (the web UI) and a *machine surface* (REST API, CLI, MCP) intended for AI agents acting on the user's behalf.
- **Entry popup** — opened by clicking a pixel: a compact summary of that date's entry (ratings, habit check-offs, goals status, freeform excerpt) with edit and delete actions; on an empty date it offers creating the entry for that day instead.

## Rules

- Goals are date-bound and single-homed: day D's checklist lives only on day D. No copying, seeding, or linking between days — the next-day draft flow is merely a convenient moment to author them. If the flow didn't happen (skipped day, first entry ever), the day starts with an empty checklist to fill manually. The five lines start blank every night; goals are retyped deliberately, never carried forward.
- Entries auto-save as they are written; all fields are optional. Any past date can be backfilled, any entry edited or deleted, at any time.
- One entry per calendar date, at most.
