package telegram

import (
	"strings"
	"testing"
	"time"

	"admin-bot/internal/domain"
	"admin-bot/internal/usecase"
)

var almatyLoc = mustLoad("Asia/Almaty")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// TestRaidInfoText_FinishedRaid is the /raidgo case the admins hit: the raid
// ended on Monday, defenses run afterwards, and the command must still describe
// it instead of answering "Активных рейдов нет".
func TestRaidInfoText_FinishedRaid(t *testing.T) {
	info := &usecase.CurrentWeekInfo{
		WeekNumber: 4,
		RaidStatus: domain.RaidStatusNone,
		HasRaids:   true,
		RecentRaid: &domain.RaidInfo{
			RaidName:   "quadchecker",
			TeamsCount: 20,
			StartDate:  time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC),
			EndDate:    time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC),
		},
	}

	got := raidInfoText(domain.PiscineGo, info, time.UTC)
	for _, want := range []string{"quadchecker", "Команд: 20", "завершён", "до пятницы, 21.08"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Активных рейдов нет") {
		t.Errorf("a raid still being defended must not read as no raid:\n%s", got)
	}
}

// TestRaidInfoText_NoRaidAtAll keeps the old answer for a piscine with nothing
// to report.
func TestRaidInfoText_NoRaidAtAll(t *testing.T) {
	got := raidInfoText(domain.PiscineJS, &usecase.CurrentWeekInfo{
		WeekNumber: 4, RaidStatus: domain.RaidStatusNone, HasRaids: true,
	}, time.UTC)

	if !strings.Contains(got, "Активных рейдов нет") || !strings.Contains(got, "Final Exam") {
		t.Errorf("unexpected text:\n%s", got)
	}
}

// TestRaidInfoText_NoRaidsAtAllOnPlatform is the regression test for a piscine
// that is running but has no raid events yet: deriving the week from an empty
// raid list used to label it "Неделя 4 (Final Exam)".
func TestRaidInfoText_NoRaidsAtAllOnPlatform(t *testing.T) {
	got := raidInfoText(domain.PiscineJS, &usecase.CurrentWeekInfo{
		WeekNumber: 0, RaidStatus: domain.RaidStatusNone, HasRaids: false,
	}, time.UTC)

	if strings.Contains(got, "Неделя") || strings.Contains(got, "Final Exam") {
		t.Errorf("a piscine with no raids must not claim a week:\n%s", got)
	}
	if !strings.Contains(got, "Рейды не найдены") {
		t.Errorf("unexpected text:\n%s", got)
	}
}

// TestRaidInfoText_RunningRaid verifies a running raid is reported with local
// times, and that a finished one alongside it is shown too.
func TestRaidInfoText_RunningRaid(t *testing.T) {
	info := &usecase.CurrentWeekInfo{
		WeekNumber: 2,
		RaidStatus: domain.RaidStatusActive,
		HasRaids:   true,
		ActiveRaid: &domain.RaidInfo{
			RaidName:   "sudoku",
			TeamsCount: 18,
			StartDate:  time.Date(2026, 8, 22, 4, 0, 0, 0, time.UTC), // 09:00 in Almaty (UTC+5)
			EndDate:    time.Date(2026, 8, 24, 4, 0, 0, 0, time.UTC),
		},
		RecentRaid: &domain.RaidInfo{
			RaidName:  "quad",
			EndDate:   time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC),
			StartDate: time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC),
		},
	}

	got := raidInfoText(domain.PiscineGo, info, almatyLoc)
	for _, want := range []string{"⚔️ Рейд", "sudoku", "22.08 09:00", "quad", "завершён"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

// TestDefenseDateFor pins the defense date to the raid rather than to "today":
// a table built on Wednesday for a raid that ended on Monday must still be
// headed with that Monday.
func TestDefenseDateFor(t *testing.T) {
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC) // Wednesday

	ended := &domain.RaidInfo{EndDate: time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)} // Monday
	if got := defenseDateFor(ended, now).Format("2006-01-02"); got != "2026-08-17" {
		t.Errorf("finished raid: got %s, want 2026-08-17", got)
	}

	running := &domain.RaidInfo{EndDate: time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)} // next Monday
	if got := defenseDateFor(running, now).Format("2006-01-02"); got != "2026-08-24" {
		t.Errorf("running raid: got %s, want 2026-08-24", got)
	}

	// A raid with no end date falls back to the next Monday from today.
	if got := defenseDateFor(&domain.RaidInfo{}, now).Format("2006-01-02"); got != "2026-08-24" {
		t.Errorf("raid without dates: got %s, want 2026-08-24", got)
	}
}

// TestSkipLines verifies a single-piscine request explains itself line by line
// while a full sweep collapses the skips into one summary.
func TestSkipLines(t *testing.T) {
	skips := []skipNote{
		{domain.PiscineJS, "бассейн сейчас не идёт"},
		{domain.PiscineRUST, "нет рейда для защиты"},
	}

	verbose := skipLines(skips, true)
	if len(verbose) != 2 {
		t.Fatalf("verbose: got %d lines, want 2: %v", len(verbose), verbose)
	}
	if !strings.Contains(verbose[0], "Piscine JS") {
		t.Errorf("verbose line should name the piscine: %q", verbose[0])
	}

	summary := skipLines(skips, false)
	if len(summary) != 1 {
		t.Fatalf("summary: got %d lines, want 1: %v", len(summary), summary)
	}
	for _, want := range []string{"js", "rust", "Пропущены"} {
		if !strings.Contains(summary[0], want) {
			t.Errorf("summary missing %q: %q", want, summary[0])
		}
	}

	if got := skipLines(nil, true); got != nil {
		t.Errorf("no skips should produce no lines, got %v", got)
	}
}

// TestAnnounceKindsPerPiscine pins the rule the admins asked for: every pool
// announces the defense sign-up, and only Piscine Go carries the rest (FAQ,
// exam, hackathon, final exam).
func TestAnnounceKindsPerPiscine(t *testing.T) {
	goKinds := domain.AnnouncementKindsFor(domain.PiscineGo)
	if len(goKinds) < 2 {
		t.Fatalf("Piscine Go should offer the full menu, got %d", len(goKinds))
	}

	for _, p := range domain.AllPiscines() {
		kinds := domain.AnnouncementKindsFor(p)
		if p == domain.PiscineGo {
			continue
		}
		if len(kinds) != 1 {
			t.Errorf("%s: got %d announcements, want exactly 1", p, len(kinds))
			continue
		}
		if kinds[0].ID != "defense" {
			t.Errorf("%s: the single announcement is %q, want \"defense\"", p, kinds[0].ID)
		}
	}

	if got := domain.AnnouncementKindsFor("Piscine Nope"); got != nil {
		t.Errorf("an unknown piscine must offer nothing, got %v", got)
	}
}

// TestAnnounceKindKeyboard verifies the menu carries the piscine in its callback
// data (so no dialog state is needed) and stays inside Telegram's 64-byte limit.
func TestAnnounceKindKeyboard(t *testing.T) {
	kinds := domain.AnnouncementKindsFor(domain.PiscineGo)
	kb := announceKindKeyboard(domain.PiscineGo, kinds)

	var data []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			data = append(data, btn.CallbackData)
		}
	}
	if len(data) != len(kinds) {
		t.Fatalf("got %d buttons, want %d: %v", len(data), len(kinds), data)
	}
	for _, d := range data {
		if len(d) > 64 {
			t.Errorf("callback data %q is %d bytes, over Telegram's 64-byte limit", d, len(d))
		}
	}

	// Every button must round-trip back to its kind and piscine.
	for i, d := range data {
		kind, piscine, ok := parseAnnounceKindCallback(d)
		if !ok {
			t.Errorf("callback %q did not parse", d)
			continue
		}
		if kind.ID != kinds[i].ID || piscine != domain.PiscineGo {
			t.Errorf("callback %q parsed as %q/%q", d, kind.ID, piscine)
		}
	}
}

// TestParseAnnounceKindCallback_Rejects covers the malformed and unknown cases.
func TestParseAnnounceKindCallback_Rejects(t *testing.T) {
	cases := []string{
		cbAnnKind + "defense",                // no piscine
		cbAnnKind + "nosuch:Piscine Go",      // unknown announcement
		cbAnnKind + "defense:Piscine Nope",   // unknown piscine
		cbAnnKind + "hackathon:Piscine RUST", // Go-only announcement, other pool
		cbAnnKind + "faq:Piscine AI 1",       // Go-only announcement, other pool
		cbAnnKind,                            // empty
	}
	for _, data := range cases {
		if _, _, ok := parseAnnounceKindCallback(data); ok {
			t.Errorf("callback %q should have been rejected", data)
		}
	}
}

// TestAnnounceKindsForNonGo_NeedsSheet documents that the one announcement
// non-Go pools have is the one carrying the defense-table link, so a missing
// SHEET_* configuration must be reported rather than silently dropped.
func TestAnnounceKindsForNonGo_NeedsSheet(t *testing.T) {
	kinds := domain.AnnouncementKindsFor(domain.PiscineAI_1)
	if len(kinds) != 1 {
		t.Fatalf("got %d kinds, want 1", len(kinds))
	}
	if !kinds[0].NeedsSheet || !kinds[0].NeedsRaid || !kinds[0].AboutDefense {
		t.Errorf("the defense announcement should need the raid, the sheet and be about defense: %+v", kinds[0])
	}
}

// TestAnnounceRenderErrorText verifies each failure explains what to do next.
func TestAnnounceRenderErrorText(t *testing.T) {
	defense, _ := domain.AnnouncementKindFor(domain.PiscineGo, "defense")

	noRaid := announceRenderErrorText(defense, domain.PiscineAI_1, domain.ErrNoRaidForAnnouncement)
	if !strings.Contains(noRaid, "Piscine AI 1") || !strings.Contains(noRaid, "нет рейда") {
		t.Errorf("unexpected text:\n%s", noRaid)
	}

	noPiscine := announceRenderErrorText(defense, domain.PiscineRUST, domain.ErrNoActivePiscine)
	if !strings.Contains(noPiscine, "Piscine RUST") || !strings.Contains(noPiscine, "не идёт") {
		t.Errorf("unexpected text:\n%s", noPiscine)
	}

	noTemplate := announceRenderErrorText(defense, domain.PiscineGo, domain.ErrTemplateNotFound)
	if !strings.Contains(noTemplate, "messages/") {
		t.Errorf("a missing template should name where it belongs:\n%s", noTemplate)
	}
}
