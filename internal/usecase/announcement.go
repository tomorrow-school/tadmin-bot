package usecase

import (
	"fmt"

	"admin-bot/internal/domain"
)

// RenderAnnouncement renders one ready-made announcement (see
// domain.AnnouncementKindsFor) for a piscine, so /announce can answer with a
// ready text instead of the admin retyping the same message every week.
//
// info is the piscine's current week, already fetched by the caller (it needs
// the week number to resolve the defense-table link anyway). It may be nil for
// an announcement that interpolates no raid data — those render for any
// audience, including "all subscribers", and even while the platform is
// unreachable.
//
// Unlike BuildMessage this does NOT refuse a message the programme does not
// schedule for the current week: the admin picked it deliberately and may well
// be resending an earlier text.
func (uc *RaidUseCase) RenderAnnouncement(
	piscine domain.PiscineType,
	kind domain.AnnouncementKind,
	info *CurrentWeekInfo,
	extra map[string]string,
) (string, error) {
	raid := announcementRaid(kind, info)
	if kind.NeedsRaid && (raid == nil || raid.RaidName == "") {
		return "", fmt.Errorf("%w: %s", domain.ErrNoRaidForAnnouncement, kind.ID)
	}

	// A stub keeps the template variables defined (as empty strings) for kinds
	// that do not need the raid, so no {{PLACEHOLDER}} leaks into a broadcast.
	if raid == nil {
		raid = &domain.RaidInfo{Piscine: piscine}
		if info != nil {
			raid.WeekNumber = info.WeekNumber
		}
	}

	if strat, ok := uc.strategies[piscine]; ok {
		vars := strat.TemplateVars(kind.Message, raid, extra)
		return uc.templates.Render(strat.TemplateKey(kind.Message), vars)
	}

	// No strategy (the "all subscribers" audience has no piscine): render with
	// the common variables directly.
	vars := map[string]string{
		"RAID_NAME":   raid.RaidName,
		"TEAMS_COUNT": fmt.Sprintf("%d", raid.TeamsCount),
	}
	for k, v := range extra {
		vars[k] = v
	}
	return uc.templates.Render(string(kind.Message), vars)
}

// announcementRaid picks the raid an announcement talks about: the one being
// defended for a defense message, otherwise the one students are registering
// for (running or upcoming), falling back to the just-finished raid.
func announcementRaid(kind domain.AnnouncementKind, info *CurrentWeekInfo) *domain.RaidInfo {
	if info == nil {
		return nil
	}
	if kind.AboutDefense {
		raid, _ := info.DefenseRaid()
		return raid
	}
	if info.ActiveRaid != nil {
		return info.ActiveRaid
	}
	return info.RecentRaid
}
