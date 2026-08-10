package usecase

import (
	"admin-bot/internal/domain"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// astanaRegion is the campus segment used for the Astana report.
const astanaRegion = "astanahub"

type UpdatesUseCase struct {
	eduClient domain.OneEduClient

	// regionEvents maps a lowercased campus name to its pinned event-ID config.
	// A campus with no entry (or a zero-valued config) uses the default
	// path-based lookup for every metric.
	regionEvents map[string]domain.RegionUpdateEventsConfig

	// leadClient fetches per-campus application (lead) counts from the apply
	// site. Optional: when nil the region report omits the lead line.
	leadClient domain.LeadClient
}

type AstanaUpdatesUseCase = UpdatesUseCase

func NewUpdatesUseCase(eduClient domain.OneEduClient, regionEvents map[string]domain.RegionUpdateEventsConfig, leadClient domain.LeadClient) *UpdatesUseCase {
	if regionEvents == nil {
		regionEvents = map[string]domain.RegionUpdateEventsConfig{}
	}
	return &UpdatesUseCase{
		eduClient:    eduClient,
		regionEvents: regionEvents,
		leadClient:   leadClient,
	}
}

func NewAstanaUpdatesUseCase(eduClient domain.OneEduClient) *AstanaUpdatesUseCase {
	return NewUpdatesUseCase(eduClient, nil, nil)
}

// eventsForRegion returns the pinned event-ID config for a campus, or a
// zero-valued config (all metrics path-based) when nothing is configured.
func (u *UpdatesUseCase) eventsForRegion(campus string) domain.RegionUpdateEventsConfig {
	return u.regionEvents[strings.ToLower(strings.TrimSpace(campus))]
}

func (u *UpdatesUseCase) GetAstanaUpdates(ctx context.Context) (domain.AstanaUpdatesInfo, error) {
	astanaUpdate, err := u.eduClient.GetAstanaUpdates(ctx)
	if err != nil {
		return domain.AstanaUpdatesInfo{}, fmt.Errorf("get astana updates: %w", err)
	}

	now := time.Now()
	current, upcoming := u.discoverPiscines(ctx)

	return domain.AstanaUpdatesInfo{
		Total:                astanaUpdate.Total,
		Succeeded:            astanaUpdate.Succeeded,
		Checkin:              astanaUpdate.Checkin,
		PiscineRegistrations: u.piscineRegistrations(ctx, astanaRegion, current, upcoming, now),
	}, nil
}

// discoverPiscines fetches current and upcoming piscines once. It is
// best-effort: a discovery failure yields an empty list so the base stats are
// still reported rather than failing the whole command.
func (u *UpdatesUseCase) discoverPiscines(ctx context.Context) (current, upcoming []domain.PiscineEvent) {
	current, _ = u.eduClient.GetCurrentPiscines(ctx)
	upcoming, _ = u.eduClient.GetUpcomingPiscines(ctx)
	return current, upcoming
}

// piscineRegistrations counts registrations for every unique piscine path that
// belongs to region, across current and upcoming piscines. Several events may
// share one path (parallel streams); counting is path-based, so those collapse
// to a single line. A path present only among upcoming piscines is marked
// Upcoming with the earliest start date of its events.
func (u *UpdatesUseCase) piscineRegistrations(
	ctx context.Context,
	region string,
	current, upcoming []domain.PiscineEvent,
	endDate time.Time,
) []domain.PiscineRegistrationCount {
	type agg struct {
		inCurrent bool
		start     time.Time
	}
	byPath := map[string]*agg{}
	order := make([]string, 0)

	add := func(events []domain.PiscineEvent, isCurrent bool) {
		for _, ev := range events {
			if !strings.EqualFold(domain.RegionOfPath(ev.Path), region) {
				continue
			}
			a, ok := byPath[ev.Path]
			if !ok {
				a = &agg{start: ev.StartAt}
				byPath[ev.Path] = a
				order = append(order, ev.Path)
			}
			if isCurrent {
				a.inCurrent = true
			}
			if ev.StartAt.Before(a.start) {
				a.start = ev.StartAt
			}
		}
	}
	add(current, true)
	add(upcoming, false)

	counts := make([]domain.PiscineRegistrationCount, 0, len(order))
	for _, path := range order {
		a := byPath[path]
		count, err := u.eduClient.GetRegistrationCountByPath(ctx, path, endDate)
		if err != nil {
			// Best-effort: skip a path we couldn't count rather than failing the
			// whole report.
			continue
		}
		counts = append(counts, domain.PiscineRegistrationCount{
			Label:    domain.LabelFromPath(path),
			Path:     path,
			Count:    count,
			Upcoming: !a.inCurrent,
			StartAt:  a.start,
		})
	}
	return counts
}

// GetEventInfo returns the detailed view of a single event by ID, or nil when
// no event with that ID exists. It delegates to the 01-edu client.
func (u *UpdatesUseCase) GetEventInfo(ctx context.Context, id int) (*domain.EventInfo, error) {
	info, err := u.eduClient.GetEventInfo(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get event info %d: %w", id, err)
	}
	return info, nil
}

func (u *UpdatesUseCase) GetRegionUpdates(ctx context.Context) (domain.RegionUpdatesReport, error) {
	campuses, err := u.eduClient.GetCampuses(ctx)
	if err != nil {
		return domain.RegionUpdatesReport{}, fmt.Errorf("get campuses: %w", err)
	}
	if len(campuses) == 0 {
		return domain.RegionUpdatesReport{}, domain.ErrNoCampuses
	}

	report := domain.RegionUpdatesReport{
		Regions: make([]domain.RegionUpdatesInfo, 0, len(campuses)),
	}

	// Discover piscines once and reuse across every campus (each campus filters
	// the shared list by its own path segment).
	now := time.Now()
	current, upcoming := u.discoverPiscines(ctx)

	// Fetch website application (lead) counts once. Best-effort: leadCounts is nil
	// when the lead client is unconfigured or the fetch failed, in which case the
	// report simply omits the lead line rather than failing the whole command.
	leadCounts := u.leadCounts(ctx)

	for _, campus := range campuses {
		campus = strings.TrimSpace(campus)
		if campus == "" {
			report.Errors = append(report.Errors, domain.RegionUpdatesError{
				Err: errors.New("empty campus name"),
			})
			continue
		}
		// Astana has its own dedicated command (/get_astana_updates); skip it here
		// so /get_region_updates doesn't duplicate that report.
		if strings.EqualFold(campus, astanaRegion) {
			continue
		}

		info, err := u.eduClient.GetRegionUpdates(ctx, campus, u.eventsForRegion(campus))
		if err != nil {
			report.Errors = append(report.Errors, domain.RegionUpdatesError{
				Region: campus,
				Err:    err,
			})
			continue
		}
		if info == nil {
			report.Errors = append(report.Errors, domain.RegionUpdatesError{
				Region: campus,
				Err:    errors.New("empty region stats response"),
			})
			continue
		}
		if strings.TrimSpace(info.Region) == "" {
			info.Region = campus
		}

		info.PiscineRegistrations = u.piscineRegistrations(ctx, campus, current, upcoming, now)

		if leadCounts != nil {
			// A campus absent from the map has zero submissions, not missing data.
			info.LeadApplications = leadCounts[applyCampusKey(campus)]
			info.HasLeadApplications = true
		}

		report.Regions = append(report.Regions, *info)
	}

	return report, nil
}

// applyCampusAliases maps a 01-edu campus name (lowercased) to the campus_id
// the apply site uses when the two systems spell the same city differently.
// 01-edu reports "petropavlovsk"; the apply export keys it as "petropavl".
// Add an entry here for any future mismatch surfaced by the lead line reading 0
// for a campus that clearly has applications.
var applyCampusAliases = map[string]string{
	"petropavlovsk": "petropavl",
}

// applyCampusKey normalizes a 01-edu campus name to the apply site's campus_id
// key: lowercased, with known cross-system aliases applied.
func applyCampusKey(campus string) string {
	key := strings.ToLower(strings.TrimSpace(campus))
	if alias, ok := applyCampusAliases[key]; ok {
		return alias
	}
	return key
}

// leadCounts fetches per-campus application counts from the lead client, keyed
// by lowercased campus ID. It is best-effort: a nil client or a fetch error
// yields nil so the caller reports regions without a lead line rather than
// failing. Errors are logged, not surfaced to the chat.
func (u *UpdatesUseCase) leadCounts(ctx context.Context) map[string]domain.LeadCounts {
	if u.leadClient == nil {
		return nil
	}
	counts, err := u.leadClient.GetLeadCountsByCampus(ctx)
	if err != nil {
		slog.Warn("fetch lead counts failed; region report will omit lead line", "err", err)
		return nil
	}
	return counts
}
