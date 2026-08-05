package main

// Habit names as they appear in the spreadsheet's final header. Renamed
// habits import once under their final name; Duolingo is the one habit that
// ended (removed 2025-12-20).
const (
	// NoFap and Diet use the names as they exist in the live DELTA database
	// (the user renamed them there), so imports match instead of duplicating.
	habitNoFap       = "Nofap"
	habitDuolingo    = "Duolingo 1 daily quest"
	habitNoScreen    = "No screen 1 hour before bed (+red glasses)"
	habitWorkout     = "Workout or 50 Situps or 20k+ steps"
	habitWakeEarly   = "Wake early (no later than 6AM)"
	habitSocial      = "Social media only 30 minutes"
	habitMeditate    = "Meditate for 10 minutes"
	habitJournal     = "Journal"
	habitRead        = "Read at least 30 minutes"
	habitWork        = "Work for 6 hours or 1 hour of personal projects on free days"
	habitSupplements = "Supplements"
	habitDiet        = "Diet (no fast food or vape)"
	habitNoNails     = "No biting nails"
	habitShowerCold  = "Shower cold"
	habitRosetta     = "ROSETTA 15 minutes"
)

// headerShiftDate is the day Duolingo was removed from the tracker. The
// checkbox columns never moved, but every header from Duolingo's slot onward
// was renamed one column to the left, so column meaning changes at this date.
const headerShiftDate = "2025-12-20"

// restructureDate is when per-habit tracking began ("New Habits structure").
// The single checkbox before it meant "did all habits" and names no specific
// habit, so earlier days import no checkoffs at all.
const restructureDate = "2024-07-02"

type habitDef struct {
	Name       string
	ActiveFrom string
	ActiveTo   string // empty = still active
}

// habitTimeline is ordered: creation order becomes DELTA position order, so
// the current header order comes first and the ended Duolingo habit sits last.
var habitTimeline = []habitDef{
	{habitNoFap, "2024-07-02", ""},
	{habitNoScreen, "2024-07-02", ""},
	{habitWorkout, "2024-07-02", ""},
	{habitWakeEarly, "2024-07-02", ""},
	{habitSocial, "2024-07-02", ""},
	{habitMeditate, "2024-07-02", ""},
	{habitJournal, "2024-07-02", ""},
	{habitRead, "2024-10-19", ""},
	{habitWork, "2024-10-19", ""},
	{habitSupplements, "2025-04-19", ""},
	{habitDiet, "2025-05-25", ""},
	{habitNoNails, "2025-05-25", ""},
	{habitShowerCold, "2025-07-13", ""},
	{habitRosetta, "2026-01-27", ""},
	{habitDuolingo, "2024-07-02", "2025-12-19"},
}

// columnHabits maps a spreadsheet checkbox column (0-based, columns 12-25)
// to the habit whose checkmark it held on the given date.
func columnHabits(date string) map[int]string {
	if date < restructureDate {
		return map[int]string{}
	}
	if date < headerShiftDate {
		return map[int]string{
			12: habitNoFap,
			13: habitDuolingo,
			14: habitNoScreen,
			15: habitWorkout,
			16: habitWakeEarly,
			17: habitSocial,
			18: habitMeditate,
			19: habitJournal,
			20: habitRead,
			21: habitWork,
			22: habitSupplements,
			23: habitDiet,
			24: habitNoNails,
			25: habitShowerCold,
		}
	}
	return map[int]string{
		12: habitNoFap,
		13: habitNoScreen,
		14: habitWorkout,
		15: habitWakeEarly,
		16: habitSocial,
		17: habitMeditate,
		18: habitJournal,
		19: habitRead,
		20: habitWork,
		21: habitSupplements,
		22: habitDiet,
		23: habitNoNails,
		24: habitShowerCold,
		25: habitRosetta,
	}
}
