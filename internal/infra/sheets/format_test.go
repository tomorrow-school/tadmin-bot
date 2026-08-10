package sheets

import (
	"strings"
	"testing"
	"time"

	"admin-bot/internal/usecase"
)

// TestResetRange verifies the wipe window is clamped to the sheet's real grid: a
// GridRange reaching past the grid edge is rejected by the Sheets API, which
// would abort the whole update.
func TestResetRange(t *testing.T) {
	cases := []struct {
		name             string
		meta             sheetMeta
		wantRows         int64
		wantCols         int64
		wantSkippedReset bool
	}{
		{"default_grid", sheetMeta{rowCount: 1000, columnCount: 26}, 1000, 26, false},
		{"small_grid_not_exceeded", sheetMeta{rowCount: 100, columnCount: 10}, 100, 10, false},
		{"large_grid_clamped", sheetMeta{rowCount: 5000, columnCount: 50}, 1000, 26, false},
		{"no_grid_info", sheetMeta{}, 0, 0, true},
		{"rows_but_no_columns", sheetMeta{rowCount: 100}, 100, 0, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rows, cols := resetRange(tc.meta)
			if rows != tc.wantRows || cols != tc.wantCols {
				t.Errorf("resetRange = (%d, %d), want (%d, %d)", rows, cols, tc.wantRows, tc.wantCols)
			}
			if skipped := rows <= 0 || cols <= 0; skipped != tc.wantSkippedReset {
				t.Errorf("reset skipped = %v, want %v", skipped, tc.wantSkippedReset)
			}
		})
	}
}

// TestClearCellsRequestClearsFormatting is the regression test for the styling
// bug: the wipe must cover userEnteredFormat, not just values. Clearing values
// alone left old fills and borders on rows the new (smaller) table no longer
// covers.
func TestClearCellsRequestClearsFormatting(t *testing.T) {
	req := clearCellsRequest(7, 1000, 26)
	if req.RepeatCell == nil {
		t.Fatal("expected a RepeatCell request")
	}

	fields := req.RepeatCell.Fields
	for _, want := range []string{"userEnteredFormat", "userEnteredValue"} {
		if !strings.Contains(fields, want) {
			t.Errorf("Fields = %q, must clear %q", fields, want)
		}
	}
	// An empty CellData is how the API expresses "unset these properties" — a nil
	// Cell would make the request a no-op.
	if req.RepeatCell.Cell == nil {
		t.Error("Cell must be an empty CellData, not nil")
	}

	r := req.RepeatCell.Range
	if r.SheetId != 7 || r.StartRowIndex != 0 || r.EndRowIndex != 1000 || r.StartColumnIndex != 0 || r.EndColumnIndex != 26 {
		t.Errorf("range = %+v, want the full A1:Z1000 window of sheet 7", r)
	}
}

// TestBreakRowRequestsShrink documents WHY the reset is needed: formatSheet only
// ever paints the rows the new table occupies, so shrinking from a 10-row table
// with two break rows to a 3-row table with none emits no request that would
// remove the old orange fills.
func TestBreakRowRequestsShrink(t *testing.T) {
	big := buildRows(DefenseTableParams{
		Schedule: usecase.CalculateDefenseSchedule(usecase.ScheduleParams{
			TeamsCount: 30, Columns: 3, StartHour: 11, IncludeBreaks: true,
		}),
	})
	bigBreaks := breakRowRequests(1, big)
	if len(bigBreaks) == 0 {
		t.Fatal("precondition: expected break rows in the 10-row table")
	}

	small := buildRows(DefenseTableParams{
		Schedule: usecase.CalculateDefenseSchedule(usecase.AutoScheduleParams(9)),
	})
	if got := breakRowRequests(1, small); len(got) != 0 {
		t.Errorf("small table emitted %d break-format requests, want 0", len(got))
	}
	if len(small) >= len(big) {
		t.Fatalf("precondition: small table (%d rows) should be shorter than big (%d rows)", len(small), len(big))
	}
	// Nothing in the small table's format pass touches the rows the big table
	// used, which is exactly why resetSheet has to clear them up front.
	for _, req := range append(breakRowRequests(1, small), boldHeaderRequest(1)) {
		if end := req.RepeatCell.Range.EndRowIndex; end > int64(len(small)+1) {
			t.Errorf("format request reaches row %d, beyond the new table (%d rows)", end, len(small))
		}
	}
}

// TestBuildRowsAutoSchedule checks the rendered rows of the automatic schedule:
// 18 teams give six 30-minute slots starting at 14:30 and no break rows.
func TestBuildRowsAutoSchedule(t *testing.T) {
	rows := buildRows(DefenseTableParams{
		RaidName:    "sudoku",
		DefenseDate: time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC),
		Schedule:    usecase.CalculateDefenseSchedule(usecase.AutoScheduleParams(18)),
	})

	want := []string{"14:30", "15:00", "15:30", "16:00", "16:30", "17:00"}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if rows[i].TimeSlot != w {
			t.Errorf("row %d = %q, want %q", i, rows[i].TimeSlot, w)
		}
		if rows[i].Type != rowSlot {
			t.Errorf("row %d is not a slot row", i)
		}
	}
}
