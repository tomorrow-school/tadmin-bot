package domain

import "testing"

// TestRecruitmentEventCount verifies participants win once anyone is enrolled
// and applications are reported before that.
func TestRecruitmentEventCount(t *testing.T) {
	cases := []struct {
		name string
		ev   RecruitmentEvent
		want int
	}{
		{"not_started_only_applications", RecruitmentEvent{Participants: 0, Applications: 114}, 114},
		{"started_applications_converted", RecruitmentEvent{Participants: 92, Applications: 0}, 92},
		{"check_in_seated_by_hand", RecruitmentEvent{Participants: 105, Applications: 49}, 105},
		{"nothing_yet", RecruitmentEvent{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ev.Count(); got != tc.want {
				t.Errorf("Count() = %d, want %d", got, tc.want)
			}
		})
	}
}
