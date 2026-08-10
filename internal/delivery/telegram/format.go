package telegram

import (
	"fmt"
	"strings"
	"time"

	"admin-bot/internal/domain"
)

// formatRegionUpdatesMessage renders one region's block, mirroring the Astana
// reference format (dated header + metric lines). Metrics whose pinned event
// failed verification are shown as unavailable instead of a stale number, so a
// single bad event ID never masquerades as real data.
func formatRegionUpdatesMessage(info domain.RegionUpdatesInfo, date string) string {
	region := strings.TrimSpace(info.Region)
	if region == "" {
		region = "unknown"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "### %s - %s\n", date, escapeHTML(region))
	if info.HasLeadApplications {
		l := info.LeadApplications
		fmt.Fprintf(&sb, "- %d заявок с сайта (сегодня: %d, вчера: %d)\n", l.Total, l.Today, l.Yesterday)
	}
	fmt.Fprintf(&sb, "- %d заявок\n", info.SignedUpWithoutOnboarding)
	fmt.Fprintf(&sb, "- %d прошли игры\n", info.SucceededOnboardingGames)
	writeRegionMetric(&sb, info, domain.EventCheckin, info.CheckinRegistrations, "reg на check-in")
	writePiscineRegistrations(&sb, info.PiscineRegistrations)
	return sb.String()
}

// writePiscineRegistrations renders one line per discovered piscine (current
// and upcoming), showing its module/curriculum path. Upcoming piscines are
// annotated with their start date.
func writePiscineRegistrations(sb *strings.Builder, regs []domain.PiscineRegistrationCount) {
	for _, r := range regs {
		label := r.Label
		if label == "" {
			label = r.Path
		}
		if r.Upcoming {
			fmt.Fprintf(sb, "- %d reg на %s (скоро старт: %s)\n",
				r.Count, escapeHTML(label), r.StartAt.Format("02.01"))
			continue
		}
		fmt.Fprintf(sb, "- %d reg на %s\n", r.Count, escapeHTML(label))
	}
}

// writeRegionMetric writes a metric line, or an "unavailable" notice when the
// metric's pinned event was flagged stale (missing / wrong region / ended).
func writeRegionMetric(sb *strings.Builder, info domain.RegionUpdatesInfo, t domain.EventType, count int, label string) {
	if info.IsStale(t) {
		fmt.Fprintf(sb, "- ⚠️ %s: данные неактуальны (ивент недоступен или завершён)\n", label)
		return
	}
	fmt.Fprintf(sb, "- %d %s\n", count, label)
}

// formatEventInfoMessage renders a single event's detail block for /get_event:
// participant count, event window, and registration window(s). Times are shown
// in loc (the configured timezone) so they match what admins see locally. A
// single registration collapses to one line; multiple windows are listed with
// their individual participant counts.
func formatEventInfoMessage(info domain.EventInfo, loc *time.Location) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "📅 <b>Ивент</b> (id %d)\n", info.ID)
	if p := strings.TrimSpace(info.Path); p != "" {
		fmt.Fprintf(&sb, "🔗 %s\n", escapeHTML(p))
	}
	fmt.Fprintf(&sb, "👥 Участников: %d\n", info.Participants)
	fmt.Fprintf(&sb, "🚀 Ивент: %s — %s\n",
		fmtEventTime(info.StartAt, loc), fmtEventTime(info.EndAt, loc))

	switch len(info.Registrations) {
	case 0:
		sb.WriteString("📝 Регистрация: —\n")
	case 1:
		r := info.Registrations[0]
		fmt.Fprintf(&sb, "📝 Регистрация: %s — %s\n",
			fmtEventTime(r.StartAt, loc), fmtEventTime(r.EndAt, loc))
	default:
		sb.WriteString("📝 Регистрации:\n")
		for _, r := range info.Registrations {
			fmt.Fprintf(&sb, "  • %s — %s (%d уч.)\n",
				fmtEventTime(r.StartAt, loc), fmtEventTime(r.EndAt, loc), r.Participants)
		}
	}
	return sb.String()
}

// fmtEventTime formats an event/registration timestamp in loc, or returns "—"
// for a zero (missing) time.
func fmtEventTime(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "—"
	}
	if loc != nil {
		t = t.In(loc)
	}
	return t.Format("02.01.2006 15:04")
}

// htmlEscaper escapes the characters significant in Telegram's HTML parse mode.
// strings.Replacer runs in a single left-to-right pass, so "&" -> "&amp;" is not
// re-escaped. (go-telegram/bot has no EscapeHTML helper — only EscapeMarkdown.)
var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// escapeHTML escapes externally-sourced text before interpolation into an
// HTML-parse-mode message.
func escapeHTML(s string) string { return htmlEscaper.Replace(s) }

// parsePiscineFromCallback extracts the piscine type from callback data.
func parsePiscineFromCallback(data, prefix string) string {
	if !strings.HasPrefix(data, prefix) {
		return ""
	}
	return strings.TrimPrefix(data, prefix)
}

// nextMonday returns the date of the next Monday from the given time, at
// midnight in the input's location. Pass a time already converted to the
// desired location (e.g. time.Now().In(loc)) to avoid timezone drift.
func nextMonday(t time.Time) time.Time {
	daysUntilMonday := (8 - int(t.Weekday())) % 7
	if daysUntilMonday == 0 {
		daysUntilMonday = 7
	}
	monday := t.AddDate(0, 0, daysUntilMonday)
	return time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, t.Location())
}
