package usecase

import (
	"fmt"
	"testing"
)

// TestCalculateDefenseSchedule_Columns verifies that the number of columns
// drives the row count (rows = ceil(teams/columns)) and is carried through to
// the schedule, while the default 3-column layout reproduces the historical
// behavior.
func TestCalculateDefenseSchedule_Columns(t *testing.T) {
	cases := []struct {
		name       string
		teams      int
		columns    int
		wantRows   int
		wantSlots  int
		wantEnd    string // no breaks expected in these small cases OR verified separately
		wantEndSet bool
	}{
		{"default_3_cols_9_teams", 9, 3, 3, 9, "12:30", true},    // 3 rows < 5 → no break
		{"5_cols_30_teams", 30, 5, 6, 30, "", false},             // 6 rows → 1 break
		{"10_cols_30_teams", 30, 10, 3, 30, "12:30", true},       // 3 rows < 5 → no break
		{"1_col_4_teams", 4, 1, 4, 4, "13:00", true},             // 4 rows < 5 → no break
		{"zero_cols_defaults_to_one", 3, 0, 3, 3, "12:30", true}, // guarded to 1 column
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := CalculateDefenseSchedule(ScheduleParams{
				TeamsCount:    tc.teams,
				Columns:       tc.columns,
				StartHour:     11,
				StartMinute:   0,
				IncludeBreaks: true,
			})
			if got.Rows != tc.wantRows {
				t.Errorf("Rows = %d, want %d", got.Rows, tc.wantRows)
			}
			if got.TotalSlots != tc.wantSlots {
				t.Errorf("TotalSlots = %d, want %d", got.TotalSlots, tc.wantSlots)
			}
			wantCols := tc.columns
			if wantCols < 1 {
				wantCols = 1
			}
			if got.Columns != wantCols {
				t.Errorf("Columns = %d, want %d", got.Columns, wantCols)
			}
			if tc.wantEndSet && got.EndTime != tc.wantEnd {
				t.Errorf("EndTime = %q, want %q", got.EndTime, tc.wantEnd)
			}
		})
	}
}

// TestCalculateDefenseSchedule_NoBreaks verifies that IncludeBreaks=false skips
// break computation entirely: no break rows, and the end time reflects pure
// slot time with no inserted gaps.
func TestCalculateDefenseSchedule_NoBreaks(t *testing.T) {
	withBreaks := CalculateDefenseSchedule(ScheduleParams{
		TeamsCount: 30, Columns: 3, StartHour: 11, IncludeBreaks: true,
	})
	if len(withBreaks.BreakAfterRows) == 0 {
		t.Fatal("precondition: expected breaks with IncludeBreaks=true for 10 rows")
	}

	noBreaks := CalculateDefenseSchedule(ScheduleParams{
		TeamsCount: 30, Columns: 3, StartHour: 11, IncludeBreaks: false,
	})
	if len(noBreaks.BreakAfterRows) != 0 {
		t.Errorf("BreakAfterRows = %v, want empty when IncludeBreaks=false", noBreaks.BreakAfterRows)
	}
	if len(noBreaks.BreakTimes) != 0 {
		t.Errorf("BreakTimes = %v, want empty when IncludeBreaks=false", noBreaks.BreakTimes)
	}
	// 10 rows × 30min from 11:00, no breaks → 16:00 (vs 16:30 with one break).
	if noBreaks.EndTime != "16:00" {
		t.Errorf("EndTime = %q, want %q", noBreaks.EndTime, "16:00")
	}
}

// TestCalculateDefenseSchedule_StartHour verifies the start time flows into the
// computed times (so /edit_tables can shift the whole schedule).
func TestCalculateDefenseSchedule_StartHour(t *testing.T) {
	got := CalculateDefenseSchedule(ScheduleParams{
		TeamsCount: 3, Columns: 3, StartHour: 9, StartMinute: 30, IncludeBreaks: true,
	})
	if got.StartTime != "09:30" {
		t.Errorf("StartTime = %q, want %q", got.StartTime, "09:30")
	}
	// 1 row × 30min from 09:30 → 10:00.
	if got.EndTime != "10:00" {
		t.Errorf("EndTime = %q, want %q", got.EndTime, "10:00")
	}
}

// TestCalculateDefenseSchedule_Shortfall documents the data behind the
// /edit_tables shortfall warning: with too few columns the computed end time
// runs past a requested window. Here 30 teams at 3 columns from 11:00 finishes
// at 16:30 — later than a requested 14:00 close.
func TestCalculateDefenseSchedule_Shortfall(t *testing.T) {
	got := CalculateDefenseSchedule(ScheduleParams{
		TeamsCount: 30, Columns: 3, StartHour: 11, IncludeBreaks: true,
	})
	if got.EndTime != "16:30" {
		t.Errorf("EndTime = %q, want %q", got.EndTime, "16:30")
	}
	// The computed end (16:30) exceeds a desired 14:00 window → shortfall.
	if got.EndTime <= "14:00" {
		t.Errorf("expected computed end %q to exceed the 14:00 window", got.EndTime)
	}
}

// TestCalculateDefenseScheduleWindow verifies the /edit_tables layout: rows are
// however many whole slots fit the window, independent of team count, with an
// optional break of one slot's length placed at the requested time.
func TestCalculateDefenseScheduleWindow(t *testing.T) {
	t.Run("fills_window_no_breaks", func(t *testing.T) {
		got := CalculateDefenseScheduleWindow(WindowScheduleParams{
			StartHour: 11, EndHour: 17, SlotMinutes: 30, Columns: 3,
		})
		if got.Rows != 12 { // 360 min / 30
			t.Errorf("Rows = %d, want 12", got.Rows)
		}
		if got.TotalSlots != 36 { // 12 * 3
			t.Errorf("TotalSlots = %d, want 36", got.TotalSlots)
		}
		if got.EndTime != "17:00" {
			t.Errorf("EndTime = %q, want 17:00", got.EndTime)
		}
		if len(got.BreakAfterRows) != 0 {
			t.Errorf("BreakAfterRows = %v, want none", got.BreakAfterRows)
		}
	})

	t.Run("slot_length_changes_row_count", func(t *testing.T) {
		got := CalculateDefenseScheduleWindow(WindowScheduleParams{
			StartHour: 11, EndHour: 17, SlotMinutes: 40, Columns: 2,
		})
		if got.Rows != 9 { // 360 / 40
			t.Errorf("Rows = %d, want 9", got.Rows)
		}
		if got.SlotMinutes != 40 {
			t.Errorf("SlotMinutes = %d, want 40", got.SlotMinutes)
		}
		if got.EndTime != "17:00" {
			t.Errorf("EndTime = %q, want 17:00", got.EndTime)
		}
	})

	t.Run("break_at_requested_time", func(t *testing.T) {
		got := CalculateDefenseScheduleWindow(WindowScheduleParams{
			StartHour: 11, EndHour: 17, SlotMinutes: 30, Columns: 3,
			IncludeBreaks: true, BreakHour: 14,
		})
		// 6 slots (11:00–14:00), a break at 14:00, then 5 slots (14:30–17:00).
		if got.Rows != 11 {
			t.Errorf("Rows = %d, want 11", got.Rows)
		}
		if len(got.BreakAfterRows) != 1 || got.BreakAfterRows[0] != 6 {
			t.Errorf("BreakAfterRows = %v, want [6]", got.BreakAfterRows)
		}
		if len(got.BreakTimes) != 1 || got.BreakTimes[0] != "14:00" {
			t.Errorf("BreakTimes = %v, want [14:00]", got.BreakTimes)
		}
		if got.EndTime != "17:00" {
			t.Errorf("EndTime = %q, want 17:00", got.EndTime)
		}
	})

	t.Run("window_shorter_than_one_slot", func(t *testing.T) {
		got := CalculateDefenseScheduleWindow(WindowScheduleParams{
			StartHour: 11, StartMinute: 0, EndHour: 11, EndMinute: 20, SlotMinutes: 30, Columns: 3,
		})
		if got.Rows != 0 || got.TotalSlots != 0 {
			t.Errorf("Rows=%d TotalSlots=%d, want 0/0", got.Rows, got.TotalSlots)
		}
	})
}

// TestAutoScheduleParams pins the automatic layout: 3 columns, no breaks, and a
// start time derived backwards from the 17:30 deadline. The 18-team case is the
// worked example from the admin: 18 teams → 6 rows → 14:30–17:30.
func TestAutoScheduleParams(t *testing.T) {
	cases := []struct {
		name        string
		teams       int
		wantStartHM string
	}{
		{"18_teams_6_rows", 18, "14:30"},    // 6 rows × 30 min back from 17:30
		{"9_teams_3_rows", 9, "16:00"},      // 3 rows × 30 min
		{"1_team_1_row", 1, "17:00"},        // ceil(1/3) = 1 row
		{"19_teams_7_rows", 19, "14:00"},    // ceil(19/3) = 7 rows
		{"no_teams_no_rows", 0, "17:30"},    // degenerate: empty table, start == end
		{"absurd_team_count", 300, "00:00"}, // 100 rows would need 50h → clamped to midnight
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := AutoScheduleParams(tc.teams)
			if p.TeamsCount != tc.teams {
				t.Errorf("TeamsCount = %d, want %d", p.TeamsCount, tc.teams)
			}
			if p.Columns != DefaultColumns {
				t.Errorf("Columns = %d, want %d", p.Columns, DefaultColumns)
			}
			if p.IncludeBreaks {
				t.Error("IncludeBreaks = true, want false for the automatic path")
			}
			gotHM := fmt.Sprintf("%02d:%02d", p.StartHour, p.StartMinute)
			if gotHM != tc.wantStartHM {
				t.Errorf("start = %s, want %s", gotHM, tc.wantStartHM)
			}
		})
	}
}

// TestAutoScheduleEndsAt1730 is the end-to-end check of the deadline rule: for
// every plausible team count the rendered schedule ends exactly at 17:30 and
// carries no breaks.
func TestAutoScheduleEndsAt1730(t *testing.T) {
	for teams := 1; teams <= 60; teams++ {
		got := CalculateDefenseSchedule(AutoScheduleParams(teams))
		if got.EndTime != "17:30" {
			t.Errorf("teams=%d: EndTime = %q, want 17:30", teams, got.EndTime)
		}
		if len(got.BreakAfterRows) != 0 || len(got.BreakTimes) != 0 {
			t.Errorf("teams=%d: expected no breaks, got rows=%v times=%v",
				teams, got.BreakAfterRows, got.BreakTimes)
		}
		if wantRows := (teams + 2) / 3; got.Rows != wantRows { // ceil(teams/3)
			t.Errorf("teams=%d: Rows = %d, want %d", teams, got.Rows, wantRows)
		}
	}
}

// TestAutoSchedule18Teams spells out the admin's example end to end, including
// the human-readable line that goes into the reminder message. The displayed
// range ends at the START of the last slot (17:00), which is the pre-existing
// formatSchedule convention — the last defense still runs 17:00–17:30.
func TestAutoSchedule18Teams(t *testing.T) {
	got := CalculateDefenseSchedule(AutoScheduleParams(18))
	if got.Rows != 6 {
		t.Errorf("Rows = %d, want 6", got.Rows)
	}
	if got.Columns != 3 {
		t.Errorf("Columns = %d, want 3", got.Columns)
	}
	if got.TotalSlots != 18 {
		t.Errorf("TotalSlots = %d, want 18", got.TotalSlots)
	}
	if got.StartTime != "14:30" || got.EndTime != "17:30" {
		t.Errorf("window = %s–%s, want 14:30–17:30", got.StartTime, got.EndTime)
	}
	if got.RecommendedSchedule != "14:30–17:00" {
		t.Errorf("RecommendedSchedule = %q, want %q", got.RecommendedSchedule, "14:30–17:00")
	}
}
