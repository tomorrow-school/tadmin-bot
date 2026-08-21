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

	// ActiveRaid is the raid the week revolves around, or nil on the final week.
	// CAUTION: a non-nil ActiveRaid does NOT mean the raid is running — between
	// raids this is the next, not-yet-started one. Check RaidStatus before doing
	// anything that assumes teams exist (e.g. building a defense table).
	ActiveRaid *domain.RaidInfo

	// RaidStatus distinguishes registration (Upcoming) from a running raid
	// (Active) and a finished one (Ended). None means no raid at all.
	RaidStatus domain.RaidStatus

	// HasRaids reports whether the piscine has any raid events at all. A running
	// piscine whose raids have not been created on the platform yet has none —
	// and must NOT be described as being on its final-exam week.
	HasRaids bool

	// RecentRaid is the raid that finished most recently and whose defense is
	// still ahead or under way — defenses happen after the raid ends, so its
	// data stays relevant through the Friday of the week it ended (see
	// DefenseWindowEnd). It is nil once that window closes, and it is never the
	// raid already reported through ActiveRaid.
	RecentRaid *domain.RaidInfo
}

// DefenseRaid returns the raid a defense table should be built for: the running
// one, or — between raids and on the final-exam week — the one that just ended
// and whose defense window is still open. nil means there is nothing to schedule.
func (i *CurrentWeekInfo) DefenseRaid() (*domain.RaidInfo, domain.RaidStatus) {
	if i == nil {
		return nil, domain.RaidStatusNone
	}
	if i.ActiveRaid != nil && i.RaidStatus.AllowsDefenseTable() {
		return i.ActiveRaid, i.RaidStatus
	}
	if i.RecentRaid != nil {
		return i.RecentRaid, domain.RaidStatusEnded
	}
	return nil, domain.RaidStatusNone
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
		// Not a failure: the piscine simply is not running right now. Callers
		// distinguish it from an upstream error via errors.Is.
		return nil, fmt.Errorf("%w: %s", domain.ErrNoActivePiscine, piscine)
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
			RaidStatus:  domain.RaidStatusActive,
			HasRaids:    true,
			RecentRaid:  findRecentlyEndedRaid(raids, now),
		}, nil
	}

	recent := findRecentlyEndedRaid(raids, now)

	// We're between raids; the upcoming raid tells us which week we're in. Its
	// registration window is still open, hence RaidStatusUpcoming — the raid in
	// ActiveRaid has NOT started.
	if next := findNextUpcomingRaid(raids, now); next != nil {
		return &CurrentWeekInfo{
			PiscineInfo: piscineInfo,
			WeekNumber:  next.WeekNumber,
			ActiveRaid:  next,
			RaidStatus:  domain.RaidStatusUpcoming,
			HasRaids:    true,
			RecentRaid:  recent,
		}, nil
	}

	// A running piscine with no raid events at all: the week is simply unknown
	// (they are usually created as the cohort progresses). Reporting it as the
	// final-exam week would be a plain lie, so leave the number at 0 and let
	// HasRaids tell callers why.
	if len(raids) == 0 {
		return &CurrentWeekInfo{
			PiscineInfo: piscineInfo,
			WeekNumber:  0,
			RaidStatus:  domain.RaidStatusNone,
			HasRaids:    false,
		}, nil
	}

	// Nothing running and nothing coming: the piscine is on its final-exam week.
	// The week number is derived from the raids that actually exist rather than
	// from TotalWeeks-1 — a cohort with fewer raids than the programme table
	// assumes (the AI streams run two) used to fall through every branch here
	// and fail with "could not determine current week", which is what turned
	// /create_tables into "❌ Ошибка при обновлении таблицы".
	return &CurrentWeekInfo{
		PiscineInfo: piscineInfo,
		WeekNumber:  finalWeekNumber(piscine, raids),
		ActiveRaid:  nil,
		RaidStatus:  domain.RaidStatusNone,
		HasRaids:    true,
		RecentRaid:  recent,
	}, nil
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
	WeekNumber int // 0 when the piscine has no raids at all

	// ActiveRaid carries the same caveat as CurrentWeekInfo.ActiveRaid: between
	// raids it holds the next, not-yet-started one. Consult RaidStatus.
	ActiveRaid *domain.RaidInfo
	RaidStatus domain.RaidStatus
	HasRaids   bool

	// RecentRaid carries the same meaning as CurrentWeekInfo.RecentRaid: the
	// just-finished raid whose defense window is still open.
	RecentRaid *domain.RaidInfo
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
		return &EventWeekInfo{Event: event, WeekNumber: 0, HasRaids: false, RaidStatus: domain.RaidStatusNone}, nil
	}

	// Order defines the week number: earliest raid is week 1.
	assignOrdinalWeeks(raids)

	now := time.Now()

	if active := findActiveRaid(raids, now); active != nil {
		return &EventWeekInfo{
			Event: event, WeekNumber: active.WeekNumber, ActiveRaid: active,
			RaidStatus: domain.RaidStatusActive, HasRaids: true,
			RecentRaid: findRecentlyEndedRaid(raids, now),
		}, nil
	}

	recent := findRecentlyEndedRaid(raids, now)

	// Between raids: the next upcoming raid tells us the week. Registration is
	// still open, so the status is Upcoming, not Active.
	if next := findNextUpcomingRaid(raids, now); next != nil {
		return &EventWeekInfo{
			Event: event, WeekNumber: next.WeekNumber, ActiveRaid: next,
			RaidStatus: domain.RaidStatusUpcoming, HasRaids: true,
			RecentRaid: recent,
		}, nil
	}

	// Nothing running and nothing coming: the final-exam week, numbered one past
	// the last raid.
	return &EventWeekInfo{
		Event: event, WeekNumber: len(raids) + 1, ActiveRaid: nil,
		RaidStatus: domain.RaidStatusNone, HasRaids: true,
		RecentRaid: recent,
	}, nil
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
//
// A mapped piscine can still arrive unnumbered: when a cohort renames its raids
// (Piscine AI 1 running wine-pca-analysis instead of backtesting-sp500) the map
// yields week 0 for every one of them. Ordering by start date is the only thing
// left to go on, so fall back to it — the same rule the discovered pools use.
func assignWeekNumbers(piscine domain.PiscineType, raids []domain.RaidInfo) {
	if len(domain.RaidWeekMap[piscine]) == 0 || hasUnnumberedRaid(raids) {
		assignOrdinalWeeks(raids)
	}
}

// hasUnnumberedRaid reports whether any raid lacks a week number, i.e. its name
// is absent from the piscine's raid-name→week map.
func hasUnnumberedRaid(raids []domain.RaidInfo) bool {
	for i := range raids {
		if raids[i].WeekNumber == 0 {
			return true
		}
	}
	return false
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

// finalWeekNumber is the week number of the final-exam week: one past the last
// raid, or the programme's nominal length when that is later. Deriving it from
// the raids keeps week detection working for a cohort whose raid count differs
// from what domain.TotalWeeks assumes.
func finalWeekNumber(piscine domain.PiscineType, raids []domain.RaidInfo) int {
	week := domain.TotalWeeks(piscine) // 0 for an unknown piscine

	for i := range raids {
		if w := raids[i].WeekNumber + 1; w > week {
			week = w
		}
	}
	if week < 1 {
		week = 1
	}
	return week
}

// DefenseWindowEnd returns the moment a finished raid stops being the current
// subject: the end of the Friday on or after its end date.
//
// Defenses happen AFTER the raid ends — a raid running Saturday-to-Monday is
// defended during the days that follow — so the bot has to keep reporting it
// (and keep building its defense table) until that week is over. Before this,
// a raid that ended on Monday vanished from /raidgo the same day.
func DefenseWindowEnd(end time.Time) time.Time {
	daysUntilFriday := (int(time.Friday) - int(end.Weekday()) + 7) % 7
	friday := end.AddDate(0, 0, daysUntilFriday)
	return time.Date(friday.Year(), friday.Month(), friday.Day(), 23, 59, 59, 0, friday.Location())
}

// findRecentlyEndedRaid returns the most recently finished raid whose defense
// window (through the Friday of the week it ended) is still open, or nil.
func findRecentlyEndedRaid(raids []domain.RaidInfo, now time.Time) *domain.RaidInfo {
	var best *domain.RaidInfo
	for i := range raids {
		r := raids[i]
		if !r.EndDate.Before(now) {
			continue // still running, or not started
		}
		// Compute the window in the caller's timezone so the Friday cut-off is
		// the local one, not the one the API happens to serialize with.
		if now.After(DefenseWindowEnd(r.EndDate.In(now.Location()))) {
			continue
		}
		if best == nil || r.EndDate.After(best.EndDate) {
			rCopy := r
			best = &rCopy
		}
	}
	return best
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
	// During the registration window ActiveRaid holds the NEXT raid, which has no
	// teams yet — reminding admins to build its defense table would be premature.
	if !weekInfo.RaidStatus.AllowsDefenseTable() {
		return "", nil, fmt.Errorf("raid %q has not started yet (%s)",
			weekInfo.ActiveRaid.RaidName, weekInfo.RaidStatus)
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
