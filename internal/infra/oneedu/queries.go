package oneedu

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"admin-bot/internal/domain"
)

// GetCurrentPiscineID fetches the active piscine event by name.
func (c *Client) GetCurrentPiscineID(ctx context.Context, piscine domain.PiscineType) (*domain.PiscineInfo, error) {
	vars := map[string]interface{}{"name": string(piscine)}

	var resp piscineResponse
	if err := c.runQuery(ctx, "GetCurrentPiscineId", vars, &resp); err != nil {
		return nil, err
	}

	if len(resp.Data.Event) == 0 {
		c.logger.Warn("no active piscine found", "name", piscine)
		return nil, nil
	}

	ev := resp.Data.Event[0]
	return &domain.PiscineInfo{ID: ev.ID, StartAt: ev.StartAt, EndAt: ev.EndAt}, nil
}

// genericRaidQuery fetches every raid child of an event with no name filter.
const genericRaidQuery = "GetRaidsByParentId"

// GetRaidsByPiscineID fetches all raid events for a given piscine event ID.
//
// The per-piscine queries filter raids by hardcoded names (quad/sudoku/... for
// Go, backtesting-sp500/forest-prediction for AI). When a cohort renames its
// raids — Piscine AI 1 now runs wine-pca-analysis, AI 2 titanic-survival — that
// filter matches nothing and the piscine looks raid-less, even though the
// path-based discovery behind /week sees the raids fine. So an empty result falls
// back to the unfiltered parent-ID query, which is what Piscine RUST and the
// dynamically discovered pools use anyway.
func (c *Client) GetRaidsByPiscineID(ctx context.Context, piscine domain.PiscineType, piscineEventID int) ([]domain.RaidInfo, error) {
	opName := domain.GetRaidQueryName(piscine)
	if opName == "" {
		return nil, fmt.Errorf("%w: %s", domain.ErrPiscineNotFound, piscine)
	}

	raids, err := c.fetchRaids(ctx, opName, piscine, piscineEventID)
	if err != nil {
		return nil, err
	}
	// Either the filter matched, or it was never a filtered query to begin with
	// (RUST already resolves to the generic one) — nothing left to retry.
	if len(raids) > 0 || opName == genericRaidQuery {
		return raids, nil
	}

	c.logger.Warn("no raids matched the per-piscine name filter, retrying without it",
		"piscine", piscine, "query", opName, "piscine_event_id", piscineEventID)
	return c.fetchRaids(ctx, genericRaidQuery, piscine, piscineEventID)
}

// fetchRaids runs a raid query for one parent event and maps the result. piscine
// is carried into the mapping so the raids know which piscine they belong to;
// pass "" when the caller has no fixed PiscineType (path-discovered pools).
func (c *Client) fetchRaids(ctx context.Context, opName string, piscine domain.PiscineType, parentEventID int) ([]domain.RaidInfo, error) {
	vars := map[string]interface{}{"id": parentEventID}

	var resp raidsResponse
	if err := c.runQuery(ctx, opName, vars, &resp); err != nil {
		return nil, err
	}

	raids := make([]domain.RaidInfo, 0, len(resp.Data.Event))
	for _, ev := range resp.Data.Event {
		raids = append(raids, mapEventToRaidInfo(piscine, ev))
	}
	return raids, nil
}

// GetCurrentPiscines fetches every currently active piscine event via
// path-based discovery (no name filter).
func (c *Client) GetCurrentPiscines(ctx context.Context) ([]domain.PiscineEvent, error) {
	return c.listPiscines(ctx, "GetCurrentPiscinesId", map[string]interface{}{})
}

// GetUpcomingPiscines fetches piscine events that start within the next month.
// The upper bound keeps updates focused on what opens soon rather than every
// far-future event.
func (c *Client) GetUpcomingPiscines(ctx context.Context) ([]domain.PiscineEvent, error) {
	upcomingBefore := time.Now().AddDate(0, 1, 0)
	vars := map[string]interface{}{"upcomingBefore": upcomingBefore.Format(time.RFC3339)}
	return c.listPiscines(ctx, "GetUpcomingPiscinesId", vars)
}

// listPiscines runs a path-based piscine-discovery query and maps the result.
func (c *Client) listPiscines(ctx context.Context, opName string, vars map[string]interface{}) ([]domain.PiscineEvent, error) {
	var resp piscinesResponse
	if err := c.runQuery(ctx, opName, vars, &resp); err != nil {
		return nil, err
	}

	events := make([]domain.PiscineEvent, 0, len(resp.Data.Event))
	for _, ev := range resp.Data.Event {
		events = append(events, domain.PiscineEvent{
			ID:      ev.ID,
			Path:    ev.Path,
			StartAt: ev.StartAt,
			EndAt:   ev.EndAt,
		})
	}
	return events, nil
}

// GetRaidsByParentID fetches all raid child-events of a given event ID without
// filtering by raid name. Week numbers are left at 0 here — callers assign them
// from the raid ordering (see usecase.DetectCurrentWeekForEvent).
func (c *Client) GetRaidsByParentID(ctx context.Context, parentEventID int) ([]domain.RaidInfo, error) {
	return c.fetchRaids(ctx, genericRaidQuery, "", parentEventID)
}

// GetRegistrationCountByPath counts user registrations on an arbitrary event
// path whose registration ends after endDate.
func (c *Client) GetRegistrationCountByPath(ctx context.Context, path string, endDate time.Time) (int, error) {
	vars := map[string]interface{}{
		"path":    path,
		"endDate": endDate.Format(time.RFC3339),
	}

	var resp registrationCountResponse
	if err := c.runQuery(ctx, "GetRegistrationCountByPath", vars, &resp); err != nil {
		return 0, err
	}
	return resp.Data.Registrations.Aggregate.Count, nil
}

// GetRaidByName fetches a specific raid event by name.
func (c *Client) GetRaidByName(ctx context.Context, name string, startAt string) (*domain.RaidInfo, error) {
	vars := map[string]interface{}{"name": name, "startAt": startAt}

	var resp raidsResponse
	if err := c.runQuery(ctx, "GetRaidByName", vars, &resp); err != nil {
		return nil, err
	}

	if len(resp.Data.Event) == 0 {
		return nil, nil
	}
	info := mapEventToRaidInfo("", resp.Data.Event[0])
	return &info, nil
}

// GetAstanaUpdates returns the latest updates for Astana.
func (c *Client) GetAstanaUpdates(ctx context.Context) (*domain.AstanaUpdatesInfo, error) {
	now := time.Now()
	vars := map[string]interface{}{
		"endDate":   now.Format("2006-01-02T15:04"),
		"startDate": now.AddDate(0, 0, -regionUpdatesLookbackDays).Format("2006-01-02T15:04"),
	}

	var resp astanaUpdatesResponse
	if err := c.runQuery(ctx, "GetAstanaUpdates", vars, &resp); err != nil {
		return nil, err
	}

	info := domain.AstanaUpdatesInfo{
		Total:     resp.Data.TotalAstana.Aggregate.Count,
		Succeeded: resp.Data.SucceededAstana.Aggregate.Count,
		Checkin:   resp.Data.CheckinAstana.Aggregate.Count,
	}
	return &info, nil
}

// GetCampuses fetches all campus names from OneEdu.
func (c *Client) GetCampuses(ctx context.Context) ([]string, error) {
	var resp campusesResponse
	if err := c.runQuery(ctx, "all_campuses", map[string]interface{}{}, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("%w: empty campuses response", domain.ErrGraphQL)
	}
	if len(resp.Data.Object) == 0 {
		return nil, nil
	}

	campuses := make([]string, 0, len(resp.Data.Object))
	for _, obj := range resp.Data.Object {
		name := strings.TrimSpace(obj.Name)
		if name == "" {
			c.logger.Warn("skip campus with empty name")
			continue
		}
		campuses = append(campuses, name)
	}
	if len(campuses) == 0 {
		return nil, fmt.Errorf("%w: campuses response contains no valid names", domain.ErrGraphQL)
	}
	return campuses, nil
}

// GetEventByID fetches the metadata for a single event. Returns nil (no error)
// when no event with that ID exists, so callers can distinguish "not found"
// from a transport/GraphQL failure.
func (c *Client) GetEventByID(ctx context.Context, id int) (*domain.EventMeta, error) {
	var resp eventByIDResponse
	if err := c.runQuery(ctx, "GetEventByID", map[string]interface{}{"id": id}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data.Event) == 0 {
		return nil, nil
	}
	ev := resp.Data.Event[0]
	return &domain.EventMeta{
		ID:         ev.ID,
		Path:       ev.Path,
		ObjectType: ev.Object.Type,
		ObjectName: ev.Object.Name,
		StartAt:    ev.StartAt,
		EndAt:      ev.EndAt,
	}, nil
}

// GetEventInfo fetches the detailed view of a single event: its window, every
// registration window, and the participant count per registration. Returns nil
// (no error) when no event with that ID exists, so callers can distinguish
// "not found" from a transport/GraphQL failure.
func (c *Client) GetEventInfo(ctx context.Context, id int) (*domain.EventInfo, error) {
	var resp eventInfoResponse
	if err := c.runQuery(ctx, "GetEventInfo", map[string]interface{}{"id": id}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data.Event) == 0 {
		return nil, nil
	}

	ev := resp.Data.Event[0]
	info := &domain.EventInfo{
		ID:      ev.ID,
		Path:    ev.Path,
		StartAt: ev.StartAt,
		EndAt:   ev.EndAt,
	}
	for _, r := range ev.Registrations {
		count := r.UsersAggregate.Aggregate.Count
		info.Registrations = append(info.Registrations, domain.EventRegistration{
			StartAt:      r.StartAt,
			EndAt:        r.EndAt,
			Participants: count,
		})
		info.Participants += count
	}
	return info, nil
}

// GetRegionUpdates fetches onboarding and registration stats for one campus.
//
// The pinned event IDs in events are the source of truth: each configured ID is
// fetched and verified (exists, belongs to this region, not ended) before its
// authoritative path is used to count. Unpinned metrics keep the historical
// path-based lookup derived from the campus name. Events that fail verification
// are recorded in the returned RegionUpdatesInfo.StaleEvents (and logged) rather
// than being silently trusted; the rest of the region is still reported.
func (c *Client) GetRegionUpdates(ctx context.Context, campus string, events domain.RegionUpdateEventsConfig) (*domain.RegionUpdatesInfo, error) {
	campus = strings.TrimSpace(campus)
	if campus == "" {
		return nil, fmt.Errorf("empty campus name")
	}

	now := time.Now()
	startDate := now.AddDate(0, 0, -regionUpdatesLookbackDays)
	vars := buildRegionStatsVariables(campus, startDate, now)

	// Resolve each pinned event ID to its authoritative path, verifying it. A
	// verified, active event overrides the default path variable so the count
	// tracks exactly the pinned event; a failed one is flagged stale and left on
	// its default path (whose count the caller will present as unavailable).
	stale := c.resolvePinnedEvents(ctx, campus, now, events, vars)

	var resp regionUpdatesResponse
	if err := c.runQuery(ctx, "region_stats", vars, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("%w: empty region stats response for %s", domain.ErrGraphQL, campus)
	}

	info, err := mapRegionUpdates(campus, *resp.Data)
	if err != nil {
		return nil, err
	}
	info.StaleEvents = stale
	return info, nil
}

// resolvePinnedEvents validates each pinned event ID for a region and, for the
// ones that pass, overrides the matching path variable in vars so region_stats
// counts the exact pinned event. It returns the events that failed validation.
func (c *Client) resolvePinnedEvents(
	ctx context.Context,
	campus string,
	now time.Time,
	events domain.RegionUpdateEventsConfig,
	vars map[string]interface{},
) []domain.StaleEvent {
	pins := []struct {
		typ    domain.EventType
		id     int
		varKey string
		// toVar adapts the verified event path to what the query variable
		// expects: a plain path for corePath, an anchored pattern for the
		// regex-matched check-in.
		toVar func(path string) string
	}{
		{domain.EventCheckin, events.CheckinEventID, "checkinPathRegex", exactPathPattern},
		{domain.EventModule, events.ModuleEventID, "corePath", func(p string) string { return p }},
	}

	var stale []domain.StaleEvent
	for _, p := range pins {
		if p.id == 0 {
			continue // not pinned — keep the default path-based lookup
		}

		meta, err := c.GetEventByID(ctx, p.id)
		if err != nil {
			c.logger.Warn("region event lookup failed",
				"region", campus, "eventType", p.typ, "eventID", p.id, "err", err)
			stale = append(stale, domain.StaleEvent{Type: p.typ, EventID: p.id, Reason: "lookup failed"})
			continue
		}

		status := classifyPinnedEvent(campus, meta, now)
		if status.Reason != "" {
			c.logger.Warn("pinned region event unusable",
				"region", campus, "eventType", p.typ, "eventID", p.id, "reason", status.Reason)
			stale = append(stale, domain.StaleEvent{Type: p.typ, EventID: p.id, Reason: status.Reason})
			continue
		}

		// Verified and active: pin the authoritative path for counting.
		if status.Path != "" {
			vars[p.varKey] = p.toVar(status.Path)
		}
	}
	return stale
}

// pinnedEventStatus is the outcome of validating a fetched pinned event.
type pinnedEventStatus struct {
	Path   string // authoritative path to count with, when usable
	Reason string // non-empty when the event is unusable (see StaleEvent.Reason)
}

// classifyPinnedEvent validates a fetched event for a region against the clock.
// A nil meta means the pinned ID resolved to no event. Pure — no I/O, no logging
// — so the verification rules (existence, region, not-ended) are unit-testable.
func classifyPinnedEvent(campus string, meta *domain.EventMeta, now time.Time) pinnedEventStatus {
	if meta == nil {
		return pinnedEventStatus{Reason: "not found"}
	}
	if r := domain.RegionOfPath(meta.Path); r != "" && !strings.EqualFold(r, campus) {
		return pinnedEventStatus{Reason: "region mismatch"}
	}
	if !meta.EndAt.IsZero() && !meta.EndAt.After(now) {
		return pinnedEventStatus{Reason: "ended"}
	}
	return pinnedEventStatus{Path: meta.Path}
}

func buildRegionStatsVariables(region string, startDate, endDate time.Time) map[string]interface{} {
	region = strings.TrimSpace(region)
	return map[string]interface{}{
		"campus":    region,
		"startDate": startDate.Format(time.RFC3339),
		"endDate":   endDate.Format(time.RFC3339),
		"adminRole": "campus_admin_" + region,
		// games and module ARE spelled identically across campuses (verified
		// against the platform), so they stay exact matches.
		"gamesPath":        "/" + region + "/onboarding/games",
		"checkinPathRegex": checkinPathPattern(region),
		"corePath":         "/" + region + "/module",
	}
}

// checkinPathPattern is the anchored pattern for a campus's check-in
// registration path.
//
// The spelling is not uniform on the platform: Pavlodar's event lives at
// /pavlodar/onboarding/check-in while every other campus uses
// /<campus>/onboarding/checkin. An exact comparison therefore matched nothing
// for Pavlodar and the region report showed 0 registrations instead of its real
// count. The optional hyphen covers both spellings without hardcoding a
// per-campus exception; the anchors keep it from matching a longer path.
func checkinPathPattern(region string) string {
	return "^/" + regexp.QuoteMeta(region) + "/onboarding/check-?in$"
}

// exactPathPattern turns a known path into a pattern that matches only that
// path, so a pinned event ID still counts exactly its own registrations.
func exactPathPattern(path string) string {
	return "^" + regexp.QuoteMeta(path) + "$"
}

func mapRegionUpdates(region string, data regionUpdatesNode) (*domain.RegionUpdatesInfo, error) {
	signedUp, err := strictCount(data.SignedUpNoOnboarding, "signed_up_no_onboarding")
	if err != nil {
		return nil, err
	}
	succeeded, err := strictCount(data.Succeeded, "succeeded")
	if err != nil {
		return nil, err
	}
	checkin, err := strictCount(data.Checkin, "checkin")
	if err != nil {
		return nil, err
	}
	core, err := strictCount(data.Core, "core")
	if err != nil {
		return nil, err
	}

	return &domain.RegionUpdatesInfo{
		Region:                    region,
		SignedUpWithoutOnboarding: signedUp,
		SucceededOnboardingGames:  succeeded,
		CheckinRegistrations:      checkin,
		CoreUsers:                 core,
	}, nil
}

func strictCount(node strictAggregateCountNode, field string) (int, error) {
	if node.Aggregate == nil {
		return 0, fmt.Errorf("%w: missing aggregate for %s", domain.ErrGraphQL, field)
	}
	return node.Aggregate.Count, nil
}

func mapEventToRaidInfo(piscine domain.PiscineType, ev raidEventNode) domain.RaidInfo {
	teams := make([]domain.Team, 0, len(ev.Groups))
	for _, g := range ev.Groups {
		members := make([]string, 0, len(g.Members))
		for _, m := range g.Members {
			members = append(members, m.UserLogin)
		}
		teams = append(teams, domain.Team{
			Captain: g.Captain.Login,
			Members: members,
			Status:  g.GroupStatus.Status,
		})
	}

	weekNum := 0
	if piscine != "" {
		weekNum = domain.WeekNumberByRaid(piscine, ev.Object.Name)
	}

	return domain.RaidInfo{
		Piscine:    piscine,
		EventID:    ev.ID,
		RaidName:   ev.Object.Name,
		WeekNumber: weekNum,
		TeamsCount: len(teams),
		Teams:      teams,
		StartDate:  ev.StartAt,
		EndDate:    ev.EndAt,
	}
}
