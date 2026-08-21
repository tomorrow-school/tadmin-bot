package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"admin-bot/internal/domain"
)

// TestDetectCurrentWeek_RecentRaid covers the reason /raidgo went quiet the day
// a raid ended: defenses happen AFTER the raid, so the finished raid has to stay
// reportable until the end of that week (Friday).
func TestDetectCurrentWeek_RecentRaid(t *testing.T) {
	// A raid that ended a few hours ago is always inside its window; one that
	// ended two weeks ago never is, whatever weekday the test runs on.
	cases := []struct {
		name       string
		raids      []domain.RaidInfo
		wantRecent string // "" means RecentRaid must be nil
	}{
		{
			name: "raid_ended_hours_ago_is_still_reported",
			raids: []domain.RaidInfo{
				raidAt("quad", 1, -9*day, -8*day),
				raidAt("sudoku", 2, -2*day, -2*time.Hour),
			},
			wantRecent: "sudoku",
		},
		{
			name: "raid_ended_two_weeks_ago_is_gone",
			raids: []domain.RaidInfo{
				raidAt("quad", 1, -20*day, -19*day),
				raidAt("sudoku", 2, -15*day, -14*day),
			},
			wantRecent: "",
		},
		{
			name: "registration_open_for_next_raid_keeps_the_finished_one",
			raids: []domain.RaidInfo{
				raidAt("quad", 1, -2*day, -1*time.Hour),
				raidAt("sudoku", 2, 3*day, 5*day),
			},
			wantRecent: "quad",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			uc := NewRaidUseCase(&fakeRaidClient{
				piscine: &domain.PiscineInfo{ID: 42},
				raids:   tc.raids,
			}, nil, nil)

			got, err := uc.DetectCurrentWeek(context.Background(), domain.PiscineGo)
			if err != nil {
				t.Fatalf("DetectCurrentWeek: %v", err)
			}

			if tc.wantRecent == "" {
				if got.RecentRaid != nil {
					t.Fatalf("RecentRaid = %q, want nil", got.RecentRaid.RaidName)
				}
				return
			}
			if got.RecentRaid == nil {
				t.Fatalf("RecentRaid = nil, want %q", tc.wantRecent)
			}
			if got.RecentRaid.RaidName != tc.wantRecent {
				t.Errorf("RecentRaid = %q, want %q", got.RecentRaid.RaidName, tc.wantRecent)
			}

			// A finished raid is exactly what a defense table is built for.
			raid, status := got.DefenseRaid()
			if raid == nil || raid.RaidName != tc.wantRecent {
				t.Errorf("DefenseRaid() = %+v, want %q", raid, tc.wantRecent)
			}
			if status != domain.RaidStatusEnded {
				t.Errorf("DefenseRaid() status = %s, want ended", status)
			}
		})
	}
}

// TestDefenseRaid_PrefersRunningRaid verifies a running raid still wins over a
// finished one, so the table is never built for the wrong week.
func TestDefenseRaid_PrefersRunningRaid(t *testing.T) {
	info := &CurrentWeekInfo{
		ActiveRaid: &domain.RaidInfo{RaidName: "sudoku"},
		RaidStatus: domain.RaidStatusActive,
		RecentRaid: &domain.RaidInfo{RaidName: "quad"},
	}
	raid, status := info.DefenseRaid()
	if raid == nil || raid.RaidName != "sudoku" {
		t.Fatalf("DefenseRaid() = %+v, want sudoku", raid)
	}
	if status != domain.RaidStatusActive {
		t.Errorf("status = %s, want active", status)
	}

	// A raid still in registration has no teams, so it is not a defense subject.
	upcoming := &CurrentWeekInfo{
		ActiveRaid: &domain.RaidInfo{RaidName: "quadchecker"},
		RaidStatus: domain.RaidStatusUpcoming,
	}
	if raid, _ := upcoming.DefenseRaid(); raid != nil {
		t.Errorf("DefenseRaid() = %+v for an upcoming raid, want nil", raid)
	}
}

// TestDetectCurrentWeek_FewerRaidsThanProgramme is the regression test for
// "❌ Ошибка при обновлении таблицы": a cohort whose raid count is smaller than
// TotalWeeks-1 (the AI streams run two raids, not three) matched no branch of
// week detection and failed outright.
func TestDetectCurrentWeek_FewerRaidsThanProgramme(t *testing.T) {
	uc := NewRaidUseCase(&fakeRaidClient{
		piscine: &domain.PiscineInfo{ID: 42},
		raids: []domain.RaidInfo{
			raidAt("backtesting-sp500", 1, -20*day, -19*day),
			raidAt("forest-prediction", 2, -13*day, -12*day),
		},
	}, nil, nil)

	got, err := uc.DetectCurrentWeek(context.Background(), domain.PiscineAI_1)
	if err != nil {
		t.Fatalf("DetectCurrentWeek: %v", err)
	}
	if got.RaidStatus != domain.RaidStatusNone || got.ActiveRaid != nil {
		t.Errorf("got status %s / raid %+v, want none / nil", got.RaidStatus, got.ActiveRaid)
	}
	if want := domain.TotalWeeks(domain.PiscineAI_1); got.WeekNumber != want {
		t.Errorf("WeekNumber = %d, want %d", got.WeekNumber, want)
	}
}

// TestDetectCurrentWeek_NoActivePiscine verifies a piscine that simply is not
// running is reported as such, so callers stop presenting it as a failure.
func TestDetectCurrentWeek_NoActivePiscine(t *testing.T) {
	uc := NewRaidUseCase(&fakeRaidClient{piscine: nil}, nil, nil)

	_, err := uc.DetectCurrentWeek(context.Background(), domain.PiscineRUST)
	if !errors.Is(err, domain.ErrNoActivePiscine) {
		t.Fatalf("err = %v, want ErrNoActivePiscine", err)
	}
}

// TestDefenseWindowEnd pins the rule the user described: a raid whose event
// ended on Monday 17.08 stays current through Friday 21.08.
func TestDefenseWindowEnd(t *testing.T) {
	cases := []struct {
		end  time.Time
		want string
	}{
		{time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), "2026-08-21"}, // Monday → Friday
		{time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC), "2026-08-21"}, // Saturday → next Friday
		{time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC), "2026-08-21"},  // Friday → same day
	}
	for _, tc := range cases {
		got := DefenseWindowEnd(tc.end)
		if got.Format("2006-01-02") != tc.want {
			t.Errorf("DefenseWindowEnd(%s) = %s, want %s",
				tc.end.Format("2006-01-02 Mon"), got.Format("2006-01-02"), tc.want)
		}
		if got.Hour() != 23 || got.Minute() != 59 {
			t.Errorf("DefenseWindowEnd(%s) = %v, want end of day", tc.end, got)
		}
	}
}

// TestDetectCurrentWeek_NoRaidsAtAll covers a running piscine whose raid events
// have not been created yet: deriving the week from an empty raid list would
// report the final-exam week for a cohort that has barely started.
func TestDetectCurrentWeek_NoRaidsAtAll(t *testing.T) {
	uc := NewRaidUseCase(&fakeRaidClient{
		piscine: &domain.PiscineInfo{ID: 42},
		raids:   nil,
	}, nil, nil)

	got, err := uc.DetectCurrentWeek(context.Background(), domain.PiscineJS)
	if err != nil {
		t.Fatalf("DetectCurrentWeek: %v", err)
	}
	if got.HasRaids {
		t.Error("HasRaids should be false when the piscine has no raid events")
	}
	if got.WeekNumber != 0 {
		t.Errorf("WeekNumber = %d, want 0 (unknown)", got.WeekNumber)
	}
	if raid, status := got.DefenseRaid(); raid != nil || status != domain.RaidStatusNone {
		t.Errorf("DefenseRaid() = %+v/%s, want nil/none", raid, status)
	}
}

// TestDetectCurrentWeek_HasRaids verifies the flag is set whenever raids exist,
// so the reporting side can tell "no raid now" from "no raids at all".
func TestDetectCurrentWeek_HasRaids(t *testing.T) {
	cases := map[string][]domain.RaidInfo{
		"running":  {raidAt("sudoku", 2, -1*day, 1*day)},
		"upcoming": {raidAt("sudoku", 2, 2*day, 4*day)},
		"ended":    {raidAt("quad", 1, -20*day, -19*day)},
	}
	for name, raids := range cases {
		uc := NewRaidUseCase(&fakeRaidClient{
			piscine: &domain.PiscineInfo{ID: 42},
			raids:   raids,
		}, nil, nil)

		got, err := uc.DetectCurrentWeek(context.Background(), domain.PiscineGo)
		if err != nil {
			t.Fatalf("%s: DetectCurrentWeek: %v", name, err)
		}
		if !got.HasRaids {
			t.Errorf("%s: HasRaids = false, want true", name)
		}
	}
}
