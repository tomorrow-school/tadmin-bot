package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"admin-bot/internal/domain"
)

// TestGetRegionsCheckinPiscine verifies every region is queried in report order
// under its platform campus name (Oral -> "uralsk"), a failing region is
// reported with its error instead of dropping the others, and the window starts
// at midnight a week ago in the caller's timezone.
func TestGetRegionsCheckinPiscine(t *testing.T) {
	almaty := time.FixedZone("Asia/Almaty", 5*3600)
	now := time.Date(2026, 10, 23, 15, 30, 0, 0, almaty)

	client := &fakeUpdatesClient{
		recruitment: map[string][]domain.RecruitmentEvent{
			"uralsk": {{ID: 1148, Type: domain.ObjectTypeInterview, Name: "Check-in", Applications: 80}},
		},
		recruitmentErr: map[string]error{"semey": errors.New("boom")},
	}

	got := NewUpdatesUseCase(client, nil, nil).GetRegionsCheckinPiscine(context.Background(), now)

	if len(got) != len(recruitmentRegions) {
		t.Fatalf("regions = %d, want %d", len(got), len(recruitmentRegions))
	}
	for i, r := range recruitmentRegions {
		if got[i].Region != r.Name {
			t.Errorf("region[%d] = %q, want %q", i, got[i].Region, r.Name)
		}
		if client.recruitmentCalls[i] != r.Campus {
			t.Errorf("campus[%d] = %q, want %q", i, client.recruitmentCalls[i], r.Campus)
		}
	}

	byName := map[string]domain.RegionRecruitment{}
	for _, r := range got {
		byName[r.Region] = r
	}
	if oral := byName["Oral"]; oral.Err != nil || len(oral.Events) != 1 || oral.Events[0].ID != 1148 {
		t.Errorf("Oral must be read from campus uralsk: %+v", oral)
	}
	if byName["Semey"].Err == nil {
		t.Error("Semey failure must be reported on the region")
	}
	if byName["Taraz"].Err != nil {
		t.Errorf("a failing region must not affect the others: %v", byName["Taraz"].Err)
	}

	wantSince := time.Date(2026, 10, 16, 0, 0, 0, 0, almaty)
	if !client.recruitmentSince.Equal(wantSince) {
		t.Errorf("since = %s, want %s", client.recruitmentSince, wantSince)
	}
}
