package usecase

import (
	"admin-bot/internal/domain"
	"context"
	"fmt"
	"time"
)

// recruitmentLookback is how far back /regions_checkin_piscine looks: events
// that ended within the last week are still reported (marked as finished).
const recruitmentLookback = 7 * 24 * time.Hour

// recruitmentRegion pairs a region's display name with its campus path segment.
type recruitmentRegion struct {
	Name   string
	Campus string
}

// recruitmentRegions lists the regions of /regions_checkin_piscine in report
// order. Oral's campus is called "uralsk" on the platform: querying "/oral/"
// silently returns nothing.
var recruitmentRegions = []recruitmentRegion{
	{"Shymkent", "shymkent"},
	{"Semey", "semey"},
	{"Petropavlovsk", "petropavlovsk"},
	{"Kokshetau", "kokshetau"},
	{"Kostanay", "kostanay"},
	{"Taldykorgan", "taldykorgan"},
	{"Pavlodar", "pavlodar"},
	{"Oral", "uralsk"},
	{"Oskemen", "oskemen"},
	{"Turkistan", "turkistan"},
	{"Taraz", "taraz"},
}

// RecruitmentSince returns the start of the reporting window: midnight a week
// before now, in now's location.
func RecruitmentSince(now time.Time) time.Time {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return day.Add(-recruitmentLookback)
}

// GetRegionsCheckinPiscine returns the check-in and piscine events of every
// region that end on or after RecruitmentSince(now). A region that fails is
// reported with its Err set rather than failing the whole command.
func (u *UpdatesUseCase) GetRegionsCheckinPiscine(ctx context.Context, now time.Time) []domain.RegionRecruitment {
	since := RecruitmentSince(now)
	out := make([]domain.RegionRecruitment, 0, len(recruitmentRegions))
	for _, r := range recruitmentRegions {
		events, err := u.eduClient.GetRecruitmentEvents(ctx, r.Campus, since)
		if err != nil {
			out = append(out, domain.RegionRecruitment{
				Region: r.Name,
				Err:    fmt.Errorf("get recruitment events %s: %w", r.Campus, err),
			})
			continue
		}
		out = append(out, domain.RegionRecruitment{Region: r.Name, Events: events})
	}
	return out
}
