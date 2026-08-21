package sheets

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/api/sheets/v4"
)

// rowType distinguishes between slot rows, break rows, and the header.
type rowType int

const (
	rowSlot rowType = iota
	rowBreak
)

type tableRow struct {
	Type     rowType
	TimeSlot string // e.g. "11:00"
}

// buildRows generates the list of rows (time slots + breaks).
func buildRows(params DefenseTableParams) []tableRow {
	schedule := params.Schedule
	breakSet := make(map[int]bool)
	for _, b := range schedule.BreakAfterRows {
		breakSet[b] = true
	}

	var rows []tableRow
	// Start time and slot length come from the schedule so /edit_tables can vary
	// them; the automatic paths derive the start from the 17:30 deadline via
	// usecase.AutoScheduleParams. A break occupies one slot's worth of time.
	slot := time.Duration(schedule.SlotMinutes) * time.Minute
	if slot <= 0 {
		slot = slotDuration
	}
	startTime := time.Date(2000, 1, 1, schedule.StartHour, schedule.StartMinute, 0, 0, time.UTC)
	current := startTime

	for row := 1; row <= schedule.Rows; row++ {
		rows = append(rows, tableRow{
			Type:     rowSlot,
			TimeSlot: fmt.Sprintf("%02d:%02d", current.Hour(), current.Minute()),
		})
		current = current.Add(slot)

		if breakSet[row] {
			rows = append(rows, tableRow{
				Type:     rowBreak,
				TimeSlot: fmt.Sprintf("%02d:%02d", current.Hour(), current.Minute()),
			})
			current = current.Add(slot)
		}
	}

	return rows
}

// populateSheet fills the spreadsheet with headers and time slots. The number
// of group columns is taken from the schedule, so the header shows "Группа
// 1".."Группа N" and each data row spans N group cells plus the leading time
// column.
func (c *Client) populateSheet(ctx context.Context, spreadsheetID, tabName string, params DefenseTableParams, rows []tableRow) error {
	groupCols := params.Schedule.Columns
	if groupCols < 1 {
		groupCols = 1
	}

	// Build all values: header row + data rows.
	var values [][]interface{}

	// Header row: title cell + one "Группа N" per group column.
	header := []interface{}{
		fmt.Sprintf("%s — %s", params.RaidName, params.DefenseDate.Format("02.01.2006")),
	}
	for g := 1; g <= groupCols; g++ {
		header = append(header, fmt.Sprintf("Группа %d", g))
	}
	values = append(values, header)

	// Data rows.
	for _, row := range rows {
		cells := make([]interface{}, 0, groupCols+1)
		cells = append(cells, row.TimeSlot)
		fill := ""
		if row.Type == rowBreak {
			fill = "Перерыв"
		}
		for g := 0; g < groupCols; g++ {
			cells = append(cells, fill)
		}
		values = append(values, cells)
	}

	// totalColumns = time column + group columns.
	lastCol := columnLetter(groupCols + 1)
	rangeStr := fmt.Sprintf("'%s'!A1:%s%d", tabName, lastCol, len(values))
	_, err := c.sheetsSvc.Spreadsheets.Values.Update(spreadsheetID, rangeStr, &sheets.ValueRange{
		Values: values,
	}).ValueInputOption("RAW").Context(ctx).Do()

	return err
}

// columnLetter converts a 1-based column index to its A1 letter(s):
// 1→"A", 26→"Z", 27→"AA".
func columnLetter(n int) string {
	var s string
	for n > 0 {
		n--
		s = string(rune('A'+n%26)) + s
		n /= 26
	}
	return s
}
