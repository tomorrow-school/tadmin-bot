package domain

import (
	"context"
	"time"
)

// OneEduClient communicates with the 01-edu GraphQL API.
type OneEduClient interface {
	// RefreshToken obtains or renews the JWT token.
	RefreshToken(ctx context.Context) error

	// GetCurrentPiscineID returns the active piscine event for the given piscine name.
	// Returns nil if no active piscine is found.
	GetCurrentPiscineID(ctx context.Context, piscine PiscineType) (*PiscineInfo, error)

	// GetRaidsByPiscineID returns all raid events for a given piscine event ID.
	GetRaidsByPiscineID(ctx context.Context, piscine PiscineType, piscineEventID int) ([]RaidInfo, error)

	// GetCurrentPiscines returns every currently active piscine event, discovered
	// by path (regexp on "astanahub" + "piscine") rather than a fixed name. The
	// same path may appear more than once (parallel streams); each is a distinct
	// event identified by its ID.
	GetCurrentPiscines(ctx context.Context) ([]PiscineEvent, error)

	// GetUpcomingPiscines returns piscine events that have not started yet.
	GetUpcomingPiscines(ctx context.Context) ([]PiscineEvent, error)

	// GetRaidsByParentID returns all raid child-events of a given event ID, with
	// no filtering by raid name.
	GetRaidsByParentID(ctx context.Context, parentEventID int) ([]RaidInfo, error)

	// GetRegistrationCountByPath returns the number of user registrations on the
	// given event path whose registration ends after endDate. Mirrors the legacy
	// piscinego count but for an arbitrary, dynamically discovered path.
	GetRegistrationCountByPath(ctx context.Context, path string, endDate time.Time) (int, error)

	// GetRaidByName returns a specific raid event by name, starting from a given date.
	GetRaidByName(ctx context.Context, name string, startAt string) (*RaidInfo, error)

	// GetAstanaUpdates returns the latest updates for Astana.
	GetAstanaUpdates(ctx context.Context) (*AstanaUpdatesInfo, error)

	// GetCampuses returns all campus names from OneEdu.
	GetCampuses(ctx context.Context) ([]string, error)

	// GetEventByID returns the metadata for a single event, or nil if no event
	// with that ID exists. Used to verify pinned region events.
	GetEventByID(ctx context.Context, id int) (*EventMeta, error)

	// GetEventInfo returns the detailed view of a single event (window,
	// registration windows, participant count), or nil if no event with that ID
	// exists. Backs the /get_event command.
	GetEventInfo(ctx context.Context, id int) (*EventInfo, error)

	// GetRegionUpdates returns onboarding and registration stats for a campus.
	// events pins the authoritative event IDs for the region; a zero-valued
	// config makes every metric fall back to the default path-based lookup.
	GetRegionUpdates(ctx context.Context, campus string, events RegionUpdateEventsConfig) (*RegionUpdatesInfo, error)
}

// LeadClient fetches application (lead) counts from the public apply/leadform
// site (apply.tomorrow-school.ai). It is optional: when no lead client is
// configured the region report simply omits the lead line.
type LeadClient interface {
	// GetLeadCountsByCampus returns the application counts per campus (total plus
	// today's and yesterday's submissions), keyed by campus ID (e.g. "shymkent").
	// Campuses with no submissions may be absent from the map — a missing key
	// means zeros, not an error.
	GetLeadCountsByCampus(ctx context.Context) (map[string]LeadCounts, error)
}

// TemplateRenderer renders message templates with variable substitution.
type TemplateRenderer interface {
	Render(key string, vars map[string]string) (string, error)
}

// BotSender sends messages via Telegram.
type BotSender interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
}

// Scheduler manages periodic job execution.
type Scheduler interface {
	Start()
	Stop()
}

// AccessStore persists access requests. Implementations must be safe for
// concurrent use.
type AccessStore interface {
	// Get returns the stored request for a user and whether one exists.
	Get(userID int64) (AccessRequest, bool)
	// Save inserts or overwrites the request and durably persists the store.
	Save(req AccessRequest) error
	// ListPending returns every request with status pending, ordered by
	// RequestedAt (oldest first).
	ListPending() ([]AccessRequest, error)
}
