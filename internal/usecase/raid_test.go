package usecase

import (
	"context"
	"testing"
	"time"

	"admin-bot/internal/domain"
)

// fakeRaidClient serves canned piscine/raid data. It embeds fakeUpdatesClient to
// inherit the no-op implementations of the rest of domain.OneEduClient and
// overrides only the three calls week detection makes.
type fakeRaidClient struct {
	fakeUpdatesClient

	piscine  *domain.PiscineInfo
	raids    []domain.RaidInfo
	byParent map[int][]domain.RaidInfo
}

func (f *fakeRaidClient) GetCurrentPiscineID(ctx context.Context, piscine domain.PiscineType) (*domain.PiscineInfo, error) {
	return f.piscine, nil
}

func (f *fakeRaidClient) GetRaidsByPiscineID(ctx context.Context, piscine domain.PiscineType, piscineEventID int) ([]domain.RaidInfo, error) {
	return f.raids, nil
}

func (f *fakeRaidClient) GetRaidsByParentID(ctx context.Context, parentEventID int) ([]domain.RaidInfo, error) {
	return f.byParent[parentEventID], nil
}

// raidAt builds a raid whose window is expressed as offsets from now, so the
// cases read as "ended two days ago" / "starts tomorrow".
func raidAt(name string, week int, startOffset, endOffset time.Duration) domain.RaidInfo {
	now := time.Now()
	return domain.RaidInfo{
		Piscine:    domain.PiscineGo,
		RaidName:   name,
		WeekNumber: week,
		TeamsCount: 18,
		StartDate:  now.Add(startOffset),
		EndDate:    now.Add(endOffset),
	}
}

const day = 24 * time.Hour

// TestDetectCurrentWeek_RaidStatus is the regression test for the bug where a
// not-yet-started raid was reported through the ActiveRaid field, letting
// /create_tables build a defense table during the registration window.
func TestDetectCurrentWeek_RaidStatus(t *testing.T) {
	cases := []struct {
		name          string
		raids         []domain.RaidInfo
		wantStatus    domain.RaidStatus
		wantRaidName  string // "" means ActiveRaid must be nil
		wantWeek      int
		wantAllowsTbl bool
	}{
		{
			name: "raid_running_now",
			raids: []domain.RaidInfo{
				raidAt("quad", 1, -3*day, -2*day),
				raidAt("sudoku", 2, -1*day, 2*day),
			},
			wantStatus:    domain.RaidStatusActive,
			wantRaidName:  "sudoku",
			wantWeek:      2,
			wantAllowsTbl: true,
		},
		{
			name: "registration_window_between_raids",
			raids: []domain.RaidInfo{
				raidAt("quad", 1, -5*day, -4*day),
				raidAt("sudoku", 2, 2*day, 4*day),
				raidAt("quadchecker", 3, 9*day, 11*day),
			},
			wantStatus:    domain.RaidStatusUpcoming,
			wantRaidName:  "sudoku", // the NEXT raid, not a running one
			wantWeek:      2,
			wantAllowsTbl: false,
		},
		{
			name: "all_raids_ended_final_week",
			raids: []domain.RaidInfo{
				raidAt("quad", 1, -20*day, -19*day),
				raidAt("sudoku", 2, -13*day, -12*day),
				raidAt("quadchecker", 3, -6*day, -5*day),
			},
			wantStatus:    domain.RaidStatusNone,
			wantRaidName:  "",
			wantWeek:      domain.TotalWeeks(domain.PiscineGo),
			wantAllowsTbl: false,
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
			if got.RaidStatus != tc.wantStatus {
				t.Errorf("RaidStatus = %s, want %s", got.RaidStatus, tc.wantStatus)
			}
			if got.WeekNumber != tc.wantWeek {
				t.Errorf("WeekNumber = %d, want %d", got.WeekNumber, tc.wantWeek)
			}
			if tc.wantRaidName == "" {
				if got.ActiveRaid != nil {
					t.Errorf("ActiveRaid = %+v, want nil", got.ActiveRaid)
				}
			} else {
				if got.ActiveRaid == nil {
					t.Fatalf("ActiveRaid = nil, want %q", tc.wantRaidName)
				}
				if got.ActiveRaid.RaidName != tc.wantRaidName {
					t.Errorf("ActiveRaid.RaidName = %q, want %q", got.ActiveRaid.RaidName, tc.wantRaidName)
				}
			}
			if got := got.RaidStatus.AllowsDefenseTable(); got != tc.wantAllowsTbl {
				t.Errorf("AllowsDefenseTable() = %v, want %v", got, tc.wantAllowsTbl)
			}
		})
	}
}

// TestDetectCurrentWeekForEvent_RaidStatus covers the same distinction for
// dynamically discovered pools, which use ordinal week numbering.
func TestDetectCurrentWeekForEvent_RaidStatus(t *testing.T) {
	const eventID = 777
	event := domain.PiscineEvent{ID: eventID, Path: "/astanahub/module/piscine-rust"}

	cases := []struct {
		name         string
		raids        []domain.RaidInfo
		wantStatus   domain.RaidStatus
		wantWeek     int
		wantHasRaids bool
	}{
		{
			name:         "no_raids_at_all",
			raids:        nil,
			wantStatus:   domain.RaidStatusNone,
			wantWeek:     0,
			wantHasRaids: false,
		},
		{
			name: "running_raid_is_week_two",
			raids: []domain.RaidInfo{
				raidAt("r1", 0, -10*day, -9*day),
				raidAt("r2", 0, -1*day, 1*day),
			},
			wantStatus:   domain.RaidStatusActive,
			wantWeek:     2,
			wantHasRaids: true,
		},
		{
			name: "registration_for_second_raid",
			raids: []domain.RaidInfo{
				raidAt("r1", 0, -10*day, -9*day),
				raidAt("r2", 0, 3*day, 5*day),
			},
			wantStatus:   domain.RaidStatusUpcoming,
			wantWeek:     2,
			wantHasRaids: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			uc := NewRaidUseCase(&fakeRaidClient{
				byParent: map[int][]domain.RaidInfo{eventID: tc.raids},
			}, nil, nil)

			got, err := uc.DetectCurrentWeekForEvent(context.Background(), event)
			if err != nil {
				t.Fatalf("DetectCurrentWeekForEvent: %v", err)
			}
			if got.RaidStatus != tc.wantStatus {
				t.Errorf("RaidStatus = %s, want %s", got.RaidStatus, tc.wantStatus)
			}
			if got.WeekNumber != tc.wantWeek {
				t.Errorf("WeekNumber = %d, want %d", got.WeekNumber, tc.wantWeek)
			}
			if got.HasRaids != tc.wantHasRaids {
				t.Errorf("HasRaids = %v, want %v", got.HasRaids, tc.wantHasRaids)
			}
		})
	}
}

// TestBuildDefenseReminder_SkipsUpcomingRaid verifies the scheduled Sunday
// reminder does not fire for a raid that has not started: the admin would
// otherwise be asked to create a defense table for a raid still in registration.
func TestBuildDefenseReminder_SkipsUpcomingRaid(t *testing.T) {
	uc := NewRaidUseCase(&fakeRaidClient{
		piscine: &domain.PiscineInfo{ID: 42},
		raids: []domain.RaidInfo{
			raidAt("quad", 1, -5*day, -4*day),
			raidAt("sudoku", 2, 2*day, 4*day),
			raidAt("quadchecker", 3, 9*day, 11*day),
		},
	}, nil, nil)

	_, _, err := uc.BuildDefenseReminder(context.Background(), domain.PiscineGo)
	if err == nil {
		t.Fatal("expected an error for a raid that has not started yet")
	}
}
