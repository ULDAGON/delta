package search

// Field is one textual entry column indexed by the FTS table.
type Field struct {
	Column string
	Label  string
}

// Fields is the single source of truth for the searchable entry columns and
// their user-facing attribution labels.
var Fields = [...]Field{
	{Column: "text", Label: "freeform"},
	{Column: "gratitude1", Label: "gratitude"},
	{Column: "gratitude2", Label: "gratitude"},
	{Column: "gratitude3", Label: "gratitude"},
	{Column: "went_well", Label: "went well"},
	{Column: "could_have_gone_better", Label: "could be better"},
	{Column: "goal_for_tomorrow", Label: "goal tomorrow"},
	{Column: "goal1_text", Label: "goal"},
	{Column: "goal2_text", Label: "goal"},
	{Column: "goal3_text", Label: "goal"},
	{Column: "goal4_text", Label: "goal"},
	{Column: "goal5_text", Label: "goal"},
}
