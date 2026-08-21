package domain

import (
	"strings"
	"time"
)

// PiscineType identifies a piscine programme.
type PiscineType string

const (
	PiscineGo PiscineType = "Piscine Go"
	PiscineJS PiscineType = "Piscine JS"
	// Piscine AI runs as three independent parallel streams; each is its own
	// piscine instance with the same programme logic.
	PiscineAI_1 PiscineType = "Piscine AI 1"
	PiscineAI_2 PiscineType = "Piscine AI 2"
	PiscineAI_3 PiscineType = "Piscine AI 3"
	PiscineRUST PiscineType = "Piscine RUST"
)

// AllPiscines returns every known piscine type.
func AllPiscines() []PiscineType {
	return []PiscineType{PiscineGo, PiscineJS, PiscineAI_1, PiscineAI_2, PiscineAI_3, PiscineRUST}
}

// TotalWeeks returns how many weeks a piscine lasts (including final exam week).
func TotalWeeks(p PiscineType) int {
	switch p {
	case PiscineGo, PiscineJS, PiscineAI_1, PiscineAI_2, PiscineAI_3, PiscineRUST:
		return 4
	default:
		return 0
	}
}

// HasHackathon returns true if this piscine type includes a hackathon on week 3.
// Only Go & JS have one; the AI streams and Rust do not.
func HasHackathon(p PiscineType) bool {
	return p == PiscineGo || p == PiscineJS
}

// IsFinalWeek returns true if weekNumber is the last week (no raid, no defense table).
func IsFinalWeek(p PiscineType, weekNumber int) bool {
	return weekNumber == TotalWeeks(p)
}

// RaidWeekMap maps raid names to week numbers for each piscine type.
var RaidWeekMap = map[PiscineType]map[string]int{
	PiscineGo: {
		"quad":        1,
		"sudoku":      2,
		"quadchecker": 3,
	},
	PiscineJS: {
		"crossword":  1,
		"sortable":   2,
		"clonernews": 3,
	},
	// The three AI streams run the same programme, so they share the same raid
	// map. Piscine RUST is intentionally absent: its raid names are not known, so
	// it uses the generic parent-ID raid query and ordinal week numbering (see
	// GetRaidQueryName / usecase week detection).
	PiscineAI_1: {
		"backtesting-sp500": 1,
		"forest-prediction": 2,
	},
	PiscineAI_2: {
		"backtesting-sp500": 1,
		"forest-prediction": 2,
	},
	PiscineAI_3: {
		"backtesting-sp500": 1,
		"forest-prediction": 2,
	},
}

// GetRaidQueryName returns the GraphQL operation name for fetching raids
// of a specific piscine type.
func GetRaidQueryName(p PiscineType) string {
	switch p {
	case PiscineGo:
		return "GetRaidsByPiscineGoId"
	case PiscineJS:
		return "GetRaidsByPiscineJsId"
	case PiscineAI_1, PiscineAI_2, PiscineAI_3:
		// All three AI streams share the same raid names; they differ only by
		// parent event ID, which is passed as $id to the query.
		return "GetRaidsByPiscineAiId"
	case PiscineRUST:
		// Rust raid names are not known, so fall back to the generic parent-ID
		// query (no name filter). Week numbers are then assigned by raid order.
		return "GetRaidsByParentId"
	default:
		return ""
	}
}

// WeekNumberByRaid returns the week number for a given raid name within a piscine.
// Returns 0 if the raid name is not recognized.
func WeekNumberByRaid(p PiscineType, raidName string) int {
	if m, ok := RaidWeekMap[p]; ok {
		return m[raidName]
	}
	return 0
}

// Team represents a single raid team (group).
type Team struct {
	Captain string
	Members []string
	Status  string
}

// RaidStatus says where a raid sits relative to a moment in time. It exists so
// callers stop inferring "the raid is on" from a non-nil raid pointer: week
// detection also reports the NEXT raid while its registration window is open,
// and a defense table must not be generated then.
type RaidStatus int

const (
	// RaidStatusNone means there is no raid to talk about at all (the piscine's
	// final-exam week, or a piscine with no raid events).
	RaidStatusNone RaidStatus = iota
	// RaidStatusUpcoming means the raid has not started yet — registration is
	// still open.
	RaidStatusUpcoming
	// RaidStatusActive means the raid is running right now.
	RaidStatusActive
	// RaidStatusEnded means the raid is over.
	RaidStatusEnded
)

func (s RaidStatus) String() string {
	switch s {
	case RaidStatusUpcoming:
		return "upcoming"
	case RaidStatusActive:
		return "active"
	case RaidStatusEnded:
		return "ended"
	default:
		return "none"
	}
}

// AllowsDefenseTable reports whether a defense table may be generated for a raid
// in this status. Teams only exist once the raid is under way, so the automatic
// paths refuse during registration and build the table while the raid runs or
// after it ends.
func (s RaidStatus) AllowsDefenseTable() bool {
	return s == RaidStatusActive || s == RaidStatusEnded
}

// RaidInfo is the aggregated data about a specific raid event.
type RaidInfo struct {
	Piscine    PiscineType
	EventID    int
	RaidName   string
	WeekNumber int
	TeamsCount int
	Teams      []Team
	StartDate  time.Time
	EndDate    time.Time
}

// StatusAt reports the raid's status at the given moment. The bounds are
// inclusive on both ends, matching the active-raid lookup in week detection
// (StartDate <= now <= EndDate).
func (r RaidInfo) StatusAt(now time.Time) RaidStatus {
	switch {
	case r.StartDate.After(now):
		return RaidStatusUpcoming
	case r.EndDate.Before(now):
		return RaidStatusEnded
	default:
		return RaidStatusActive
	}
}

// PiscineInfo holds the active piscine event data.
type PiscineInfo struct {
	ID      int
	StartAt time.Time
	EndAt   time.Time
}

// PiscineEvent is a discovered piscine instance (current or upcoming),
// identified by its event ID rather than a fixed name/type. Path-based
// discovery can surface several concurrent events sharing one Path (e.g. two
// parallel streams of the same piscine), so identity is the event ID, not the
// path or a PiscineType.
type PiscineEvent struct {
	ID      int
	Path    string // e.g. "/astanahub/ai-curriculum/prompt-piscine"
	StartAt time.Time
	EndAt   time.Time
}

// Label returns a human-readable identifier derived from Path, stripping the
// leading campus segment so the output shows which module/curriculum the
// piscine belongs to, e.g. "/astanahub/ai-curriculum/prompt-piscine" ->
// "ai-curriculum/prompt-piscine".
func (p PiscineEvent) Label() string { return LabelFromPath(p.Path) }

// LabelFromPath trims the leading "/" and the first path segment (the campus
// name) from an event path. A path with a single segment (or empty) is returned
// trimmed but otherwise unchanged.
func LabelFromPath(path string) string {
	trimmed := strings.Trim(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return ""
	}
	if i := strings.IndexByte(trimmed, '/'); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}
