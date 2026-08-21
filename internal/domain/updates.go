package domain

import "time"

type AstanaUpdatesInfo struct {
	Total     int
	Succeeded int
	Checkin   int

	// PiscineRegistrations holds registration counts for every currently active
	// and upcoming piscine, discovered by path rather than a single hardcoded
	// "piscinego" path.
	PiscineRegistrations []PiscineRegistrationCount
}

// PiscineRegistrationCount is the number of registrations on a discovered
// piscine, identified by its path (see PiscineEvent).
type PiscineRegistrationCount struct {
	Label    string    // PiscineEvent.Label(), e.g. "ai-curriculum/prompt-piscine"
	Path     string    // full event path, e.g. "/astanahub/ai-curriculum/prompt-piscine"
	Count    int       // registration_user_aggregate count for the path
	Upcoming bool      // true when the piscine has not started yet
	StartAt  time.Time // start of the (earliest) event for this path; used for upcoming
}

// LeadCounts holds application (lead) counts for a campus: the all-time total
// and the number submitted today and yesterday, bucketed by the configured
// timezone.
type LeadCounts struct {
	Total     int
	Today     int
	Yesterday int
}

type RegionUpdatesInfo struct {
	Region                    string
	SignedUpWithoutOnboarding int
	SucceededOnboardingGames  int
	CheckinRegistrations      int
	// PiscineRegistrations holds per-piscine registration counts for this
	// region, discovered by path (current + upcoming) instead of a single
	// hardcoded piscinego path.
	PiscineRegistrations []PiscineRegistrationCount
	CoreUsers            int

	// LeadApplications holds the application (заявки) counts submitted for this
	// campus through the public apply/leadform site (apply.tomorrow-school.ai):
	// all-time total plus today's and yesterday's submissions. Meaningful only
	// when HasLeadApplications is true; a campus with no submissions legitimately
	// reports zeros.
	LeadApplications LeadCounts
	// HasLeadApplications reports whether lead data was successfully fetched for
	// this run. When false (lead client unconfigured or the fetch failed) the
	// LeadApplications counts are not trustworthy and callers should omit them
	// rather than show misleading zeros.
	HasLeadApplications bool

	// StaleEvents lists pinned events (see RegionUpdateEventsConfig) that were
	// resolved but failed verification — they do not exist, belong to another
	// region, or have already ended. The counts for the corresponding metrics
	// are NOT trustworthy and callers should surface them as unavailable rather
	// than silently reporting a stale (often zero) number.
	StaleEvents []StaleEvent
}

// IsStale reports whether the given event type was flagged unusable for this
// region (missing / wrong region / ended). See StaleEvents.
func (r RegionUpdatesInfo) IsStale(t EventType) bool {
	for _, s := range r.StaleEvents {
		if s.Type == t {
			return true
		}
	}
	return false
}

// StaleEvent records why a pinned region event could not be used.
type StaleEvent struct {
	Type    EventType
	EventID int
	Reason  string // "not found" | "region mismatch" | "ended" | "lookup failed"
}

// EventMeta is the subset of a 01-edu event needed to validate a pinned event
// ID before trusting it as the source of truth for a region's stats.
type EventMeta struct {
	ID         int
	Path       string
	ObjectType string
	ObjectName string
	StartAt    time.Time
	EndAt      time.Time
}

// EventInfo is the detailed view of a single 01-edu event returned by the
// /get_event command: the event window, its registration window(s), and the
// number of participants. An event may expose more than one registration
// window; Participants is the total across all of them.
type EventInfo struct {
	ID            int
	Path          string
	StartAt       time.Time
	EndAt         time.Time
	Registrations []EventRegistration
	Participants  int // total participants across all registrations
}

// EventRegistration is one registration window of an event and the number of
// users registered through it.
type EventRegistration struct {
	StartAt      time.Time
	EndAt        time.Time
	Participants int
}

type RegionUpdatesError struct {
	Region string
	Err    error
}

type RegionUpdatesReport struct {
	Regions []RegionUpdatesInfo
	Errors  []RegionUpdatesError
}
