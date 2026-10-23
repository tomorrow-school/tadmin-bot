package domain

import "time"

// Object types of the events reported by /regions_checkin_piscine.
const (
	ObjectTypeInterview = "interview" // check-in
	ObjectTypePiscine   = "piscine"   // recruitment piscine
)

// RecruitmentEvent is one check-in or piscine event of a campus with both of
// its head counts. Only one of them is reported — see Count.
type RecruitmentEvent struct {
	ID      int
	Type    string // object.type: ObjectTypeInterview | ObjectTypePiscine
	Name    string // object.name
	StartAt time.Time
	EndAt   time.Time

	// Participants is the number of users enrolled in the event (event_user).
	Participants int
	// Applications is the number of users registered through the event's
	// registration windows, summed over all of them.
	Applications int
}

// Count is the head count reported for the event: participants once anyone is
// enrolled, applications otherwise.
//
// Before the event starts only applications exist. When it starts they are
// converted into participants and applications drops to 0-1 while the cohort
// is alive; on a check-in people are also added by hand, so participants can
// exceed applications.
func (e RecruitmentEvent) Count() int {
	if e.Participants > 0 {
		return e.Participants
	}
	return e.Applications
}

// Ended reports whether the event finished before now.
func (e RecruitmentEvent) Ended(now time.Time) bool {
	return !e.EndAt.IsZero() && e.EndAt.Before(now)
}

// RegionRecruitment holds the check-in and piscine events of one region.
type RegionRecruitment struct {
	Region string // display name, e.g. "Oral"
	Events []RecruitmentEvent
	Err    error // non-nil when the region could not be fetched; Events is then empty
}
