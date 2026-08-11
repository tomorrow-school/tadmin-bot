package telegram

import (
	"strings"
	"testing"

	"admin-bot/internal/domain"
	"admin-bot/internal/usecase"
)

// TestSubscribeText verifies the settings screen reports the three states a user
// can be in: never configured, paused with a selection, and active.
func TestSubscribeText(t *testing.T) {
	fresh := subscribeText(domain.Subscription{UserID: 1})
	if !strings.Contains(fresh, "выключены") || !strings.Contains(fresh, "не выбраны") {
		t.Errorf("fresh subscription should read as off with no pools:\n%s", fresh)
	}

	paused := subscribeText(domain.Subscription{
		UserID: 1, Enabled: false, Piscines: []domain.PiscineType{domain.PiscineGo},
	})
	if !strings.Contains(paused, "выключены") {
		t.Errorf("paused subscription should read as off:\n%s", paused)
	}
	if !strings.Contains(paused, "Piscine Go") {
		t.Errorf("paused subscription should still list the selection:\n%s", paused)
	}

	active := subscribeText(domain.Subscription{
		UserID: 1, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineGo, domain.PiscineRUST},
	})
	if !strings.Contains(active, "включены") {
		t.Errorf("active subscription should read as on:\n%s", active)
	}
	for _, want := range []string{"Piscine Go", "Piscine RUST"} {
		if !strings.Contains(active, want) {
			t.Errorf("active subscription missing %q:\n%s", want, active)
		}
	}
}

// TestSubscribeKeyboard verifies every piscine gets a toggle, followed ones are
// checked, and the master switch reflects the current state.
func TestSubscribeKeyboard(t *testing.T) {
	kb := subscribeKeyboard(domain.Subscription{
		UserID: 1, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineGo},
	})

	var buttons []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text+"|"+btn.CallbackData)
		}
	}
	joined := strings.Join(buttons, "\n")

	// One toggle per piscine plus the master switch.
	if want := len(domain.AllPiscines()) + 1; len(buttons) != want {
		t.Errorf("got %d buttons, want %d:\n%s", len(buttons), want, joined)
	}
	if !strings.Contains(joined, "✅ Go|"+cbSubPiscine+string(domain.PiscineGo)) {
		t.Errorf("followed piscine should be checked:\n%s", joined)
	}
	if !strings.Contains(joined, "☐ JS|"+cbSubPiscine+string(domain.PiscineJS)) {
		t.Errorf("unfollowed piscine should be unchecked:\n%s", joined)
	}
	if !strings.Contains(joined, "🔕 Отключить анонсы|"+cbSubToggle) {
		t.Errorf("an enabled subscription should offer to turn announcements off:\n%s", joined)
	}

	off := subscribeKeyboard(domain.Subscription{UserID: 1})
	last := off.InlineKeyboard[len(off.InlineKeyboard)-1][0]
	if !strings.Contains(last.Text, "Включить") {
		t.Errorf("a disabled subscription should offer to turn announcements on, got %q", last.Text)
	}
}

// TestAnnounceAudienceKeyboard verifies the audience picker covers every piscine
// plus the "everyone" option.
func TestAnnounceAudienceKeyboard(t *testing.T) {
	kb := announceAudienceKeyboard()

	var data []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			data = append(data, btn.CallbackData)
		}
	}
	if want := len(domain.AllPiscines()) + 1; len(data) != want {
		t.Fatalf("got %d buttons, want %d: %v", len(data), want, data)
	}
	for _, p := range domain.AllPiscines() {
		want := cbAnnPiscine + string(p)
		if !contains(data, want) {
			t.Errorf("missing button for %q (%s)", p, want)
		}
	}
	if !contains(data, cbAnnPiscine+announceAllArg) {
		t.Error("missing the all-subscribers button")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestAnnounceReportText verifies the admin report states what actually
// happened — including failed deliveries and an interrupted run, so a partial
// broadcast is never presented as complete.
func TestAnnounceReportText(t *testing.T) {
	clean := announceReportText("Piscine Go", usecase.BroadcastReport{
		Piscine: domain.PiscineGo, Recipients: 3, Sent: 3,
	}, false)
	if !strings.Contains(clean, "✅ Успешно: 3 из 3") {
		t.Errorf("clean report should state the counts:\n%s", clean)
	}
	if strings.Contains(clean, "Не доставлено") || strings.Contains(clean, "прервана") {
		t.Errorf("clean report should carry no warnings:\n%s", clean)
	}

	withFailures := announceReportText("Piscine Go", usecase.BroadcastReport{
		Piscine: domain.PiscineGo, Recipients: 3, Sent: 2, FailedUsers: []int64{555},
	}, false)
	for _, want := range []string{"✅ Успешно: 2 из 3", "Не доставлено: 1", "555"} {
		if !strings.Contains(withFailures, want) {
			t.Errorf("failure report missing %q:\n%s", want, withFailures)
		}
	}

	interrupted := announceReportText("все подписчики", usecase.BroadcastReport{
		Recipients: 10, Sent: 4,
	}, true)
	if !strings.Contains(interrupted, "прервана") {
		t.Errorf("interrupted report must say so:\n%s", interrupted)
	}
}

func TestPiscineShortLabel(t *testing.T) {
	cases := map[domain.PiscineType]string{
		domain.PiscineGo:   "Go",
		domain.PiscineJS:   "JS",
		domain.PiscineAI_1: "AI 1",
		domain.PiscineRUST: "RUST",
	}
	for p, want := range cases {
		if got := piscineShortLabel(p); got != want {
			t.Errorf("piscineShortLabel(%q) = %q, want %q", p, got, want)
		}
	}
}
