package usecase

import (
	"errors"
	"strings"
	"testing"

	"admin-bot/internal/domain"
	"admin-bot/internal/usecase/strategy"
)

// fakeRenderer substitutes {{KEY}} the same way the file loader does, so the
// tests assert on variable resolution rather than on file contents.
type fakeRenderer struct {
	bodies map[string]string
}

func (f *fakeRenderer) Render(key string, vars map[string]string) (string, error) {
	body, ok := f.bodies[key]
	if !ok {
		return "", domain.ErrTemplateNotFound
	}
	for k, v := range vars {
		body = strings.ReplaceAll(body, "{{"+k+"}}", v)
	}
	return body, nil
}

func announceUC(bodies map[string]string) *RaidUseCase {
	return NewRaidUseCase(&fakeRaidClient{}, &fakeRenderer{bodies: bodies},
		[]strategy.PiscineStrategy{strategy.NewGoStrategy()})
}

func kind(t *testing.T, id string) domain.AnnouncementKind {
	t.Helper()
	k, ok := domain.AnnouncementKindFor(domain.PiscineGo, id)
	if !ok {
		t.Fatalf("unknown announcement kind %q", id)
	}
	return k
}

// TestRenderAnnouncement_UsesRunningRaid verifies the exam/raid announcement
// names the raid students are registering for.
func TestRenderAnnouncement_UsesRunningRaid(t *testing.T) {
	uc := announceUC(map[string]string{
		"exam_announcement": "Регистрация на {{RAID_NAME}} открыта",
	})

	info := &CurrentWeekInfo{
		WeekNumber: 2,
		ActiveRaid: &domain.RaidInfo{RaidName: "sudoku", TeamsCount: 18},
		RaidStatus: domain.RaidStatusUpcoming,
	}

	got, err := uc.RenderAnnouncement(domain.PiscineGo, kind(t, "exam"), info, nil)
	if err != nil {
		t.Fatalf("RenderAnnouncement: %v", err)
	}
	if got != "Регистрация на sudoku открыта" {
		t.Errorf("got %q", got)
	}
}

// TestRenderAnnouncement_DefenseUsesFinishedRaid verifies the sign-up message is
// about the raid being defended — which by then has usually finished — and
// carries the table link.
func TestRenderAnnouncement_DefenseUsesFinishedRaid(t *testing.T) {
	uc := announceUC(map[string]string{
		"student_message": "Защита {{RAID_NAME}}: {{SHEET_URL}}",
	})

	info := &CurrentWeekInfo{
		WeekNumber: 4,
		RaidStatus: domain.RaidStatusNone,
		RecentRaid: &domain.RaidInfo{RaidName: "quadchecker", TeamsCount: 20},
	}

	got, err := uc.RenderAnnouncement(domain.PiscineGo, kind(t, "defense"), info,
		map[string]string{"SHEET_URL": "https://sheet"})
	if err != nil {
		t.Fatalf("RenderAnnouncement: %v", err)
	}
	if got != "Защита quadchecker: https://sheet" {
		t.Errorf("got %q", got)
	}
}

// TestRenderAnnouncement_NoRaid verifies a raid-bearing announcement refuses
// rather than sending "{{RAID_NAME}}" to every subscriber.
func TestRenderAnnouncement_NoRaid(t *testing.T) {
	uc := announceUC(map[string]string{"exam_announcement": "{{RAID_NAME}}"})

	_, err := uc.RenderAnnouncement(domain.PiscineGo, kind(t, "exam"),
		&CurrentWeekInfo{WeekNumber: 4, RaidStatus: domain.RaidStatusNone}, nil)
	if !errors.Is(err, domain.ErrNoRaidForAnnouncement) {
		t.Fatalf("err = %v, want ErrNoRaidForAnnouncement", err)
	}
}

// TestRenderAnnouncement_WithoutPiscine verifies the announcements that carry no
// raid data render for the "all subscribers" audience, which has no piscine and
// therefore no strategy.
func TestRenderAnnouncement_WithoutPiscine(t *testing.T) {
	uc := announceUC(map[string]string{"faq": "Ответы на вопросы"})

	got, err := uc.RenderAnnouncement("", kind(t, "faq"), nil, nil)
	if err != nil {
		t.Fatalf("RenderAnnouncement: %v", err)
	}
	if got != "Ответы на вопросы" {
		t.Errorf("got %q", got)
	}
}
