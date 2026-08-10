package domain

import (
	"testing"
	"time"
)

func TestAllPiscines(t *testing.T) {
	got := AllPiscines()
	want := []PiscineType{PiscineGo, PiscineJS, PiscineAI_1, PiscineAI_2, PiscineAI_3, PiscineRUST}
	if len(got) != len(want) {
		t.Fatalf("AllPiscines length = %d, want %d", len(got), len(want))
	}
	for i, p := range want {
		if got[i] != p {
			t.Errorf("AllPiscines[%d] = %q, want %q", i, got[i], p)
		}
	}
}

func TestTotalWeeks(t *testing.T) {
	cases := []struct {
		p    PiscineType
		want int
	}{
		{PiscineGo, 4},
		{PiscineJS, 4},
		{PiscineAI_1, 4},
		{PiscineAI_2, 4},
		{PiscineAI_3, 4},
		{PiscineRUST, 4},
		{PiscineType("unknown"), 0},
		{"", 0},
	}
	for _, tc := range cases {
		if got := TotalWeeks(tc.p); got != tc.want {
			t.Errorf("TotalWeeks(%q) = %d, want %d", tc.p, got, tc.want)
		}
	}
}

func TestHasHackathon(t *testing.T) {
	cases := []struct {
		p    PiscineType
		want bool
	}{
		{PiscineGo, true},
		{PiscineJS, true},
		{PiscineAI_1, false},
		{PiscineAI_2, false},
		{PiscineAI_3, false},
		{PiscineRUST, false},
		{PiscineType("unknown"), false},
	}
	for _, tc := range cases {
		if got := HasHackathon(tc.p); got != tc.want {
			t.Errorf("HasHackathon(%q) = %v, want %v", tc.p, got, tc.want)
		}
	}
}

func TestIsFinalWeek(t *testing.T) {
	cases := []struct {
		p    PiscineType
		week int
		want bool
	}{
		{PiscineGo, 1, false},
		{PiscineGo, 3, false},
		{PiscineGo, 4, true},
		{PiscineJS, 4, true},
		{PiscineAI_1, 3, false},
		{PiscineAI_1, 4, true},
		{PiscineRUST, 3, false},
		{PiscineRUST, 4, true},
		// Unknown piscine has TotalWeeks == 0, so week 0 is "final" — document this.
		{PiscineType("unknown"), 0, true},
		{PiscineType("unknown"), 1, false},
	}
	for _, tc := range cases {
		if got := IsFinalWeek(tc.p, tc.week); got != tc.want {
			t.Errorf("IsFinalWeek(%q, %d) = %v, want %v", tc.p, tc.week, got, tc.want)
		}
	}
}

func TestGetRaidQueryName(t *testing.T) {
	cases := []struct {
		p    PiscineType
		want string
	}{
		{PiscineGo, "GetRaidsByPiscineGoId"},
		{PiscineJS, "GetRaidsByPiscineJsId"},
		{PiscineAI_1, "GetRaidsByPiscineAiId"},
		{PiscineAI_2, "GetRaidsByPiscineAiId"},
		{PiscineAI_3, "GetRaidsByPiscineAiId"},
		{PiscineRUST, "GetRaidsByParentId"},
		{PiscineType("unknown"), ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := GetRaidQueryName(tc.p); got != tc.want {
			t.Errorf("GetRaidQueryName(%q) = %q, want %q", tc.p, got, tc.want)
		}
	}
}

func TestWeekNumberByRaid(t *testing.T) {
	cases := []struct {
		p    PiscineType
		raid string
		want int
	}{
		// Piscine Go
		{PiscineGo, "quad", 1},
		{PiscineGo, "sudoku", 2},
		{PiscineGo, "quadchecker", 3},
		{PiscineGo, "unknown_raid", 0},

		// Piscine JS
		{PiscineJS, "crossword", 1},
		{PiscineJS, "sortable", 2},
		{PiscineJS, "clonernews", 3},
		{PiscineJS, "missing", 0},

		// Piscine AI (three streams share the same raid map).
		{PiscineAI_1, "backtesting-sp500", 1},
		{PiscineAI_1, "forest-prediction", 2},
		{PiscineAI_2, "backtesting-sp500", 1},
		{PiscineAI_3, "forest-prediction", 2},
		// AI has no week-3 raid.
		{PiscineAI_1, "anything", 0},

		// Rust has no hardcoded raid map (generic query + ordinal weeks).
		{PiscineRUST, "anything", 0},

		// Unknown piscine.
		{PiscineType("unknown"), "quad", 0},
	}
	for _, tc := range cases {
		if got := WeekNumberByRaid(tc.p, tc.raid); got != tc.want {
			t.Errorf("WeekNumberByRaid(%q, %q) = %d, want %d", tc.p, tc.raid, got, tc.want)
		}
	}
}

func TestPiscineEventLabel(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"campus_prefixed_nested", "/astanahub/ai-curriculum/prompt-piscine", "ai-curriculum/prompt-piscine"},
		{"campus_prefixed_module", "/astanahub/module/piscine-rust", "module/piscine-rust"},
		{"no_leading_slash", "astanahub/ai-curriculum/prompt-piscine", "ai-curriculum/prompt-piscine"},
		{"single_segment", "/astanahub", "astanahub"},
		{"single_segment_no_slash", "piscine", "piscine"},
		{"trailing_slash", "/astanahub/module/piscine-rust/", "module/piscine-rust"},
		{"empty", "", ""},
		{"only_slashes", "///", ""},
		{"whitespace", "  /astanahub/piscinego  ", "piscinego"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := PiscineEvent{Path: tc.path}.Label()
			if got != tc.want {
				t.Errorf("PiscineEvent{Path:%q}.Label() = %q, want %q", tc.path, got, tc.want)
			}
			if lf := LabelFromPath(tc.path); lf != tc.want {
				t.Errorf("LabelFromPath(%q) = %q, want %q", tc.path, lf, tc.want)
			}
		})
	}
}

// programmeOf collapses the parallel AI streams onto a single programme key so
// their intentionally-shared raid names don't count as cross-wiring.
func programmeOf(p PiscineType) PiscineType {
	switch p {
	case PiscineAI_1, PiscineAI_2, PiscineAI_3:
		return "Piscine AI"
	default:
		return p
	}
}

// TestRaidWeekMap_NoCollisions makes sure no raid name maps to more than one
// programme. The three AI streams share one programme (and one raid map), so a
// name shared across them is fine; a name shared across different programmes is
// accidental cross-wiring.
func TestRaidWeekMap_NoCollisions(t *testing.T) {
	seen := map[string]PiscineType{}
	for p, m := range RaidWeekMap {
		prog := programmeOf(p)
		for name := range m {
			if other, ok := seen[name]; ok && other != prog {
				t.Errorf("raid name %q assigned to both %q and %q", name, other, prog)
			}
			seen[name] = prog
		}
	}
}

// TestRaidWeekMap_WeeksMatchTotalMinusOne ensures every raid week is in
// 1..TotalWeeks(p)-1 (the final week has no raid).
func TestRaidWeekMap_WeeksMatchTotalMinusOne(t *testing.T) {
	for p, m := range RaidWeekMap {
		maxWeek := TotalWeeks(p) - 1
		for name, week := range m {
			if week < 1 || week > maxWeek {
				t.Errorf("%s: raid %q has week=%d, want 1..%d", p, name, week, maxWeek)
			}
		}
	}
}

// TestRaidInfoStatusAt pins the window semantics, including the inclusive
// boundaries — a raid is Active exactly at its start and end instants, matching
// the active-raid lookup used by week detection.
func TestRaidInfoStatusAt(t *testing.T) {
	start := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 10, 18, 0, 0, 0, time.UTC)
	raid := RaidInfo{StartDate: start, EndDate: end}

	cases := []struct {
		name string
		now  time.Time
		want RaidStatus
	}{
		{"before_start", start.Add(-time.Hour), RaidStatusUpcoming},
		{"exactly_at_start", start, RaidStatusActive},
		{"mid_raid", start.Add(24 * time.Hour), RaidStatusActive},
		{"exactly_at_end", end, RaidStatusActive},
		{"after_end", end.Add(time.Second), RaidStatusEnded},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := raid.StatusAt(tc.now); got != tc.want {
				t.Errorf("StatusAt(%v) = %s, want %s", tc.now, got, tc.want)
			}
		})
	}
}

// TestRaidStatusAllowsDefenseTable documents the rule the handlers rely on: a
// defense table may only be built once the raid is under way or over.
func TestRaidStatusAllowsDefenseTable(t *testing.T) {
	cases := map[RaidStatus]bool{
		RaidStatusNone:     false,
		RaidStatusUpcoming: false,
		RaidStatusActive:   true,
		RaidStatusEnded:    true,
	}
	for status, want := range cases {
		if got := status.AllowsDefenseTable(); got != want {
			t.Errorf("%s.AllowsDefenseTable() = %v, want %v", status, got, want)
		}
	}
}
