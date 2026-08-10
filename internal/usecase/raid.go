package usecase

import (
	"context"
	"fmt"
	"sort"
	"time"

	"admin-bot/internal/domain"
	"admin-bot/internal/usecase/strategy"
)

// RaidUseCase orchestrates fetching data and building announcements.
type RaidUseCase struct {
	eduClient  domain.OneEduClient
	templates  domain.TemplateRenderer
	strategies map[domain.PiscineType]strategy.PiscineStrategy
}

// NewRaidUseCase constructs a RaidUseCase with the provided dependencies.
func NewRaidUseCase(
	eduClient domain.OneEduClient,
	templates domain.TemplateRenderer,
	strategies []strategy.PiscineStrategy,
) *RaidUseCase {
	m := make(map[domain.PiscineType]strategy.PiscineStrategy, len(strategies))
	for _, s := range strategies {
		m[s.Type()] = s
	}
	return &RaidUseCase{
		eduClient:  eduClient,
		templates:  templates,
		strategies: m,
	}
}

// CurrentWeekInfo holds the result of detecting the current week.
type CurrentWeekInfo struct {
	PiscineInfo *domain.PiscineInfo
	WeekNumber  int
	ActiveRaid  *domain.RaidInfo // nil on final week (no raid)
}

// DetectCurrentWeek determines which week it is for a given piscine.
// It fetches the active piscine, then its raids, and finds which raid
// is currently active (startAt <= now <= endAt).
func (uc *RaidUseCase) DetectCurrentWeek(ctx context.Context, piscine domain.PiscineType) (*CurrentWeekInfo, error) {
	piscineInfo, err := uc.eduClient.GetCurrentPiscineID(ctx, piscine)
	if err != nil {
		return nil, fmt.Errorf("get piscine ID: %w", err)
	}
	if piscineInfo == nil {
		return nil, fmt.Errorf("no active piscine found for %s", piscine)
	}

	raids, err := uc.eduClient.GetRaidsByPiscineID(ctx, piscine, piscineInfo.ID)
	if err != nil {
		return nil, fmt.Errorf("get raids: %w", err)
	}

	assignWeekNumbers(piscine, raids)

	now := time.Now()

	if active := findActiveRaid(raids, now); active != nil {
		return &CurrentWeekInfo{
			PiscineInfo: piscineInfo,
			WeekNumber:  active.WeekNumber,
			ActiveRaid:  active,
		}, nil
	}

	// No active raid. If every raid has ended, it's the final-exam week.
	// (Final week has no raid, so total raid weeks = TotalWeeks-1.)
	totalRaidWeeks := domain.TotalWeeks(piscine) - 1
	if countEndedRaids(raids, now) >= totalRaidWeeks {
		return &CurrentWeekInfo{
			PiscineInfo: piscineInfo,
			WeekNumber:  domain.TotalWeeks(piscine),
			ActiveRaid:  nil,
		}, nil
	}

	// We're between raids; the upcoming raid tells us which week we're in.
	if next := findNextUpcomingRaid(raids, now); next != nil {
		return &CurrentWeekInfo{
			PiscineInfo: piscineInfo,
			WeekNumber:  next.WeekNumber,
			ActiveRaid:  next,
		}, nil
	}

	return nil, fmt.Errorf("could not determine current week for %s", piscine)
}

// GetCurrentPiscines returns every currently active piscine discovered by path.
// Handlers go through this wrapper rather than reaching into the edu client
// directly.
func (uc *RaidUseCase) GetCurrentPiscines(ctx context.Context) ([]domain.PiscineEvent, error) {
	return uc.eduClient.GetCurrentPiscines(ctx)
}

// GetUpcomingPiscines returns piscines that have not started yet.
func (uc *RaidUseCase) GetUpcomingPiscines(ctx context.Context) ([]domain.PiscineEvent, error) {
	return uc.eduClient.GetUpcomingPiscines(ctx)
}

// EventWeekInfo is the week-detection result for a dynamically discovered
// piscine event (as opposed to a fixed PiscineType). Week numbers come from the
// ordering of the event's raids, not a hardcoded raid-name map.
type EventWeekInfo struct {
	Event      domain.PiscineEvent
	WeekNumber int              // 0 when the piscine has no raids at all
	ActiveRaid *domain.RaidInfo // nil between raids, on the final week, or when there are no raids
	HasRaids   bool
}

// DetectCurrentWeekForEvent determines the current week of a discovered piscine
// event. Unlike DetectCurrentWeek it does not rely on RaidWeekMap: raids are
// fetched generically, sorted by start date, and numbered 1..N. A piscine with
// no raids at all (e.g. a plain module) yields WeekNumber 0 and HasRaids=false
// rather than an error.
func (uc *RaidUseCase) DetectCurrentWeekForEvent(ctx context.Context, event domain.PiscineEvent) (*EventWeekInfo, error) {
	raids, err := uc.eduClient.GetRaidsByParentID(ctx, event.ID)
	if err != nil {
		return nil, fmt.Errorf("get raids for event %d: %w", event.ID, err)
	}

	if len(raids) == 0 {
		return &EventWeekInfo{Event: event, WeekNumber: 0, HasRaids: false}, nil
	}

	// Order defines the week number: earliest raid is week 1.
	assignOrdinalWeeks(raids)

	now := time.Now()

	if active := findActiveRaid(raids, now); active != nil {
		return &EventWeekInfo{Event: event, WeekNumber: active.WeekNumber, ActiveRaid: active, HasRaids: true}, nil
	}

	// No active raid. If every raid has ended, we're on the final-exam week,
	// numbered one past the last raid.
	if countEndedRaids(raids, now) >= len(raids) {
		return &EventWeekInfo{Event: event, WeekNumber: len(raids) + 1, ActiveRaid: nil, HasRaids: true}, nil
	}

	// Between raids: the next upcoming raid tells us the week.
	if next := findNextUpcomingRaid(raids, now); next != nil {
		return &EventWeekInfo{Event: event, WeekNumber: next.WeekNumber, ActiveRaid: next, HasRaids: true}, nil
	}

	// Shouldn't happen (not active, not all ended, none upcoming), but fall back
	// to week 1 rather than erroring on a discovered piscine.
	return &EventWeekInfo{Event: event, WeekNumber: 1, ActiveRaid: nil, HasRaids: true}, nil
}

// ListRaidsWithWeeks returns every raid of a named piscine's current active
// instance, each annotated with its week number. Used by the /edit_tables
// dialog to offer a raid/week picker. Week numbering follows the same rule as
// DetectCurrentWeek (see assignWeekNumbers).
func (uc *RaidUseCase) ListRaidsWithWeeks(ctx context.Context, piscine domain.PiscineType) ([]domain.RaidInfo, error) {
	piscineInfo, err := uc.eduClient.GetCurrentPiscineID(ctx, piscine)
	if err != nil {
		return nil, fmt.Errorf("get piscine ID: %w", err)
	}
	if piscineInfo == nil {
		return nil, fmt.Errorf("no active piscine found for %s", piscine)
	}

	raids, err := uc.eduClient.GetRaidsByPiscineID(ctx, piscine, piscineInfo.ID)
	if err != nil {
		return nil, fmt.Errorf("get raids: %w", err)
	}

	assignWeekNumbers(piscine, raids)
	return raids, nil
}

// ListRaidsForEvent returns every raid of a dynamically discovered piscine
// event, numbered ordinally by start date (the event has no raid-name map).
func (uc *RaidUseCase) ListRaidsForEvent(ctx context.Context, eventID int) ([]domain.RaidInfo, error) {
	raids, err := uc.eduClient.GetRaidsByParentID(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("get raids for event %d: %w", eventID, err)
	}
	assignOrdinalWeeks(raids)
	return raids, nil
}

// assignWeekNumbers fills in raid week numbers for a named piscine. Piscines
// with a hardcoded raid-name→week map (Go/JS/AI) already have their weeks set by
// the edu client during mapping; those without one (e.g. Rust, fetched via the
// generic parent-ID query) are numbered by raid order here. Centralizing this
// rule keeps DetectCurrentWeek and ListRaidsWithWeeks in agreement.
func assignWeekNumbers(piscine domain.PiscineType, raids []domain.RaidInfo) {
	if len(domain.RaidWeekMap[piscine]) == 0 {
		assignOrdinalWeeks(raids)
	}
}

// assignOrdinalWeeks sorts raids by start date and numbers them 1..N. Used for
// piscines without a hardcoded raid-name→week map (the raid order defines the
// week).
func assignOrdinalWeeks(raids []domain.RaidInfo) {
	sort.Slice(raids, func(i, j int) bool {
		return raids[i].StartDate.Before(raids[j].StartDate)
	})
	for i := range raids {
		raids[i].WeekNumber = i + 1
	}
}

// findActiveRaid returns a pointer to the raid currently in progress
// (startAt <= now <= endAt), or nil. The returned pointer references a copy,
// not the slice element, so the caller can hold onto it safely.
func findActiveRaid(raids []domain.RaidInfo, now time.Time) *domain.RaidInfo {
	for i := range raids {
		r := raids[i]
		if !r.StartDate.After(now) && !r.EndDate.Before(now) {
			return &r
		}
	}
	return nil
}

// countEndedRaids returns the number of raids whose EndDate is strictly before now.
func countEndedRaids(raids []domain.RaidInfo, now time.Time) int {
	n := 0
	for i := range raids {
		if raids[i].EndDate.Before(now) {
			n++
		}
	}
	return n
}

// findNextUpcomingRaid returns the earliest-starting raid whose StartDate is
// after now, or nil if none.
func findNextUpcomingRaid(raids []domain.RaidInfo, now time.Time) *domain.RaidInfo {
	var best *domain.RaidInfo
	for i := range raids {
		r := raids[i]
		if !r.StartDate.After(now) {
			continue
		}
		if best == nil || r.StartDate.Before(best.StartDate) {
			rCopy := r
			best = &rCopy
		}
	}
	return best
}

// BuildMessage builds a message of the given type for the given piscine.
// Returns the rendered text and an error.
func (uc *RaidUseCase) BuildMessage(
	ctx context.Context,
	piscine domain.PiscineType,
	msgType domain.MessageType,
	extra map[string]string,
) (string, error) {
	strat, ok := uc.strategies[piscine]
	if !ok {
		return "", fmt.Errorf("%w: %s", domain.ErrPiscineNotFound, piscine)
	}

	// Detect current week.
	weekInfo, err := uc.DetectCurrentWeek(ctx, piscine)
	if err != nil {
		return "", fmt.Errorf("detect week: %w", err)
	}

	// Check if this message type is applicable for this week.
	if !strat.SupportsMessage(msgType, weekInfo.WeekNumber) {
		return "", fmt.Errorf("message type %s not supported for %s week %d",
			msgType, piscine, weekInfo.WeekNumber)
	}

	// Build template vars.
	raidInfo := weekInfo.ActiveRaid
	if raidInfo == nil {
		// Final week — create a stub RaidInfo for template rendering.
		raidInfo = &domain.RaidInfo{
			Piscine:    piscine,
			WeekNumber: weekInfo.WeekNumber,
		}
	}

	vars := strat.TemplateVars(msgType, raidInfo, extra)
	templateKey := strat.TemplateKey(msgType)

	text, err := uc.templates.Render(templateKey, vars)
	if err != nil {
		return "", fmt.Errorf("render template %q: %w", templateKey, err)
	}

	return text, nil
}

// BuildDefenseReminder builds the admin reminder about creating the defense table.
// Returns the rendered text and the calculated schedule info.
func (uc *RaidUseCase) BuildDefenseReminder(
	ctx context.Context,
	piscine domain.PiscineType,
) (string, *DefenseSchedule, error) {
	weekInfo, err := uc.DetectCurrentWeek(ctx, piscine)
	if err != nil {
		return "", nil, fmt.Errorf("detect week: %w", err)
	}

	if weekInfo.ActiveRaid == nil {
		return "", nil, fmt.Errorf("no active raid for defense reminder")
	}

	raid := weekInfo.ActiveRaid
	schedule := CalculateDefenseSchedule(AutoScheduleParams(raid.TeamsCount))

	strat, ok := uc.strategies[piscine]
	if !ok {
		return "", nil, fmt.Errorf("%w: %s", domain.ErrPiscineNotFound, piscine)
	}

	extra := map[string]string{
		"ROWS":                 fmt.Sprintf("%d", schedule.Rows),
		"TOTAL_SLOTS":          fmt.Sprintf("%d", schedule.TotalSlots),
		"RECOMMENDED_SCHEDULE": schedule.RecommendedSchedule,
	}

	vars := strat.TemplateVars(domain.MsgDefenseReminder, raid, extra)
	templateKey := strat.TemplateKey(domain.MsgDefenseReminder)

	text, err := uc.templates.Render(templateKey, vars)
	if err != nil {
		return "", nil, fmt.Errorf("render template: %w", err)
	}

	return text, &schedule, nil
}

// GetStrategy returns the strategy for a piscine type (used by handlers).
func (uc *RaidUseCase) GetStrategy(piscine domain.PiscineType) (strategy.PiscineStrategy, bool) {
	s, ok := uc.strategies[piscine]
	return s, ok
}
