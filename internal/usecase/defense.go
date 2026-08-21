package usecase

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	slotDuration       = 30 * time.Minute
	breakDuration      = 30 * time.Minute
	maxConsecutiveRows = 5

	// Defaults for callers that do not let the admin override the layout (the
	// automatic /create_tables run and the scheduled defense reminder).
	DefaultColumns     = 3
	DefaultSlotMinutes = 30

	// AutoEndHour/AutoEndMinute is the hard deadline for the automatic defense
	// schedule: defenses must be over by 17:30. The start time is therefore
	// derived backwards from how many rows the teams need, rather than fixed
	// (it used to be a fixed 11:00 start, which is why an 18-team raid was
	// scheduled from 11:00 instead of 14:30).
	AutoEndHour   = 17
	AutoEndMinute = 30
)

// ScheduleParams captures the tunable inputs of a defense schedule. The
// automatic paths pass the Default* values; /edit_tables supplies admin choices.
type ScheduleParams struct {
	TeamsCount    int
	Columns       int // number of auditor / group columns
	StartHour     int
	StartMinute   int
	IncludeBreaks bool
}

// DefenseSchedule holds the calculated defense table parameters.
type DefenseSchedule struct {
	TeamsCount          int
	Rows                int
	Columns             int // group columns, carried through to the sheet renderer
	TotalSlots          int
	StartHour           int // start time, so the sheet renderer needs no package constant
	StartMinute         int
	SlotMinutes         int    // slot length, so the sheet renderer needs no package constant
	BreakAfterRows      []int  // slot-row numbers after which a break is inserted
	RecommendedSchedule string // e.g. "11:00–17:30\nПерерывы: 13:30 и 15:30"
	StartTime           string
	EndTime             string
	BreakTimes          []string // formatted break times
}

// CalculateDefenseSchedule computes the defense table layout for the given
// parameters. With IncludeBreaks=false no breaks are inserted at all.
func CalculateDefenseSchedule(params ScheduleParams) DefenseSchedule {
	columns := params.Columns
	if columns < 1 {
		columns = 1
	}

	rows := scheduleRows(params.TeamsCount, columns)
	totalSlots := rows * columns

	var breakAfterRows []int
	if params.IncludeBreaks {
		breakAfterRows = computeBreaks(rows)
		breakAfterRows = enforceMaxConsecutive(breakAfterRows, rows)
	}

	startTime := time.Date(2000, 1, 1, params.StartHour, params.StartMinute, 0, 0, time.UTC)
	endTime, breakTimes := computeTimes(startTime, rows, breakAfterRows)

	schedule := formatSchedule(startTime, endTime, breakTimes)

	return DefenseSchedule{
		TeamsCount:          params.TeamsCount,
		Rows:                rows,
		Columns:             columns,
		TotalSlots:          totalSlots,
		StartHour:           params.StartHour,
		StartMinute:         params.StartMinute,
		SlotMinutes:         int(slotDuration / time.Minute),
		BreakAfterRows:      breakAfterRows,
		RecommendedSchedule: schedule,
		StartTime:           formatTime(startTime),
		EndTime:             formatTime(endTime),
		BreakTimes:          breakTimes,
	}
}

// WindowScheduleParams drives the /edit_tables layout: the admin gives a time
// window, a per-slot length, a column count, and optionally a break at a
// specific time. The number of rows is however many whole slots fit in the
// window — the team count does NOT limit it (an admin may build a table for a
// pool the platform reports as empty).
type WindowScheduleParams struct {
	StartHour, StartMinute int
	EndHour, EndMinute     int
	SlotMinutes            int
	Columns                int
	IncludeBreaks          bool
	BreakHour, BreakMinute int // used only when IncludeBreaks
}

// CalculateDefenseScheduleWindow fills [start, end] with back-to-back slots of
// SlotMinutes each. When breaks are enabled, a single break (one slot long) is
// placed at the first slot boundary at or after the requested break time. Rows
// that would run past the window end are not added.
func CalculateDefenseScheduleWindow(p WindowScheduleParams) DefenseSchedule {
	cols := p.Columns
	if cols < 1 {
		cols = 1
	}
	slot := p.SlotMinutes
	if slot < 1 {
		slot = DefaultSlotMinutes
	}

	start := p.StartHour*60 + p.StartMinute
	end := p.EndHour*60 + p.EndMinute
	breakAt := p.BreakHour*60 + p.BreakMinute

	var breakAfterRows []int
	var breakTimes []string
	current := start
	rows := 0
	breakDone := false

	for {
		// Insert the break once we've placed at least one slot and reached the
		// requested time. It occupies one slot's worth of time.
		if p.IncludeBreaks && !breakDone && rows >= 1 && current >= breakAt && breakAt > start {
			if current+slot > end {
				break // no room for the break itself
			}
			breakTimes = append(breakTimes, formatMinutes(current))
			breakAfterRows = append(breakAfterRows, rows)
			current += slot
			breakDone = true
			continue
		}
		if current+slot > end {
			break
		}
		rows++
		current += slot
	}

	return DefenseSchedule{
		Rows:                rows,
		Columns:             cols,
		TotalSlots:          rows * cols,
		StartHour:           p.StartHour,
		StartMinute:         p.StartMinute,
		SlotMinutes:         slot,
		BreakAfterRows:      breakAfterRows,
		BreakTimes:          breakTimes,
		StartTime:           formatMinutes(start),
		EndTime:             formatMinutes(current),
		RecommendedSchedule: formatMinutes(start) + "–" + formatMinutes(current),
	}
}

// formatMinutes renders minutes-since-midnight as "HH:MM".
func formatMinutes(m int) string {
	return fmt.Sprintf("%02d:%02d", (m/60)%24, m%60)
}

// AutoScheduleParams builds the parameter set for the automatic paths
// (/create_tables and the scheduled defense reminder): the schedule ends at
// AutoEndHour:AutoEndMinute (17:30) and starts exactly as early as the team
// count requires — rows = ceil(teams / DefaultColumns), each row one slot long.
// So 18 teams over 3 columns give 6 rows and a 14:30–17:30 window.
//
// Breaks are deliberately off here: the automatic grid is back-to-back so the
// arithmetic stays "число команд → время старта". The break logic
// (computeBreaks/enforceMaxConsecutive) is untouched and still serves the manual
// /edit_tables path.
func AutoScheduleParams(teamsCount int) ScheduleParams {
	rows := scheduleRows(teamsCount, DefaultColumns)
	startHour, startMinute := startForEnd(AutoEndHour, AutoEndMinute, rows*DefaultSlotMinutes)
	return ScheduleParams{
		TeamsCount:    teamsCount,
		Columns:       DefaultColumns,
		StartHour:     startHour,
		StartMinute:   startMinute,
		IncludeBreaks: false,
	}
}

// scheduleRows is the row count a team count needs at the given column count:
// ceil(teams / columns), never negative. columns is assumed >= 1.
func scheduleRows(teamsCount, columns int) int {
	if teamsCount <= 0 {
		return 0
	}
	return int(math.Ceil(float64(teamsCount) / float64(columns)))
}

// startForEnd returns the clock time durationMinutes before endHour:endMinute.
// A schedule too long to fit the day is clamped to 00:00 rather than wrapping
// into the previous day, which would render as a nonsensical late-night start.
func startForEnd(endHour, endMinute, durationMinutes int) (hour, minute int) {
	start := endHour*60 + endMinute - durationMinutes
	if start < 0 {
		start = 0
	}
	return start / 60, start % 60
}

// computeBreaks determines after which rows breaks should be placed.
//   - fewer than 5 rows: no break
//   - 5–10 rows: one break, slightly biased toward the front when odd
//   - more than 10 rows: two breaks, splitting into three roughly equal segments
func computeBreaks(rows int) []int {
	const (
		oneBreakMin = 5
		oneBreakMax = 10
	)

	switch {
	case rows < oneBreakMin:
		return nil
	case rows <= oneBreakMax:
		// (rows+1)/2 == ceil(rows/2): for 5→3, 6→3, 7→4, 8→4, 9→5, 10→5.
		return []int{(rows + 1) / 2}
	default:
		seg1, seg2, _ := splitIntoThree(rows)
		return []int{seg1, seg1 + seg2}
	}
}

// splitIntoThree splits n into three segments as equal as possible.
// Any remainder is distributed to the earlier segments first.
func splitIntoThree(n int) (a, b, c int) {
	base := n / 3
	rem := n % 3
	a, b, c = base, base, base
	if rem >= 1 {
		a++
	}
	if rem >= 2 {
		b++
	}
	return a, b, c
}

// enforceMaxConsecutive ensures no more than maxConsecutiveRows consecutive rows
// without a break. Inserts additional breaks if needed.
func enforceMaxConsecutive(breaks []int, totalRows int) []int {
	var result []int
	prev := 0

	for _, b := range breaks {
		gap := b - prev
		for gap > maxConsecutiveRows {
			prev += maxConsecutiveRows
			result = append(result, prev)
			gap = b - prev
		}
		result = append(result, b)
		prev = b
	}

	// Check the last segment (from last break to end).
	remaining := totalRows - prev
	for remaining > maxConsecutiveRows {
		prev += maxConsecutiveRows
		result = append(result, prev)
		remaining = totalRows - prev
	}

	return result
}

// computeTimes calculates the end time and break start times.
func computeTimes(start time.Time, rows int, breakAfterRows []int) (endTime time.Time, breakTimes []string) {
	breakSet := make(map[int]bool)
	for _, b := range breakAfterRows {
		breakSet[b] = true
	}

	current := start
	for row := 1; row <= rows; row++ {
		current = current.Add(slotDuration)

		if breakSet[row] {
			breakTimes = append(breakTimes, formatTime(current))
			current = current.Add(breakDuration)
		}
	}

	return current, breakTimes
}

// formatSchedule builds the human-readable schedule string.
func formatSchedule(start, end time.Time, breakTimes []string) string {
	// End time displayed is the start of the last slot, not the end.
	// Actually we display as range: "11:00–17:30"
	// The end time from computeTimes is AFTER the last slot, so subtract one slot.
	lastSlotStart := end.Add(-slotDuration)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s–%s", formatTime(start), formatTime(lastSlotStart)))

	switch len(breakTimes) {
	case 0:
		// no breaks
	case 1:
		b.WriteString(fmt.Sprintf("\nПерерыв: %s", breakTimes[0]))
	default:
		b.WriteString(fmt.Sprintf("\nПерерывы: %s", strings.Join(breakTimes, " и ")))
	}

	return b.String()
}

func formatTime(t time.Time) string {
	return fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute())
}
