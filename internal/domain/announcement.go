package domain

// AnnouncementKind is one ready-made announcement offered by /announce. The
// admin picks a piscine first and then one of these, instead of typing the text
// by hand every time — the wording lives in messages/<Message>.txt.
type AnnouncementKind struct {
	// ID is the stable token carried in callback data. Keep it short: Telegram
	// caps callback data at 64 bytes, and the piscine name travels with it.
	ID string

	// Label is the button caption.
	Label string

	// Message selects the template file (messages/<Message>.txt).
	Message MessageType

	// NeedsRaid marks announcements whose template interpolates the current raid
	// ({{RAID_NAME}}). They can only be built for a concrete piscine that has a
	// raid running — or one that finished recently enough to still be the
	// subject (see the defense window in usecase week detection).
	NeedsRaid bool

	// NeedsSheet marks announcements that link the defense sign-up table
	// ({{SHEET_URL}}), so the caller knows to supply it.
	NeedsSheet bool

	// AboutDefense picks WHICH raid the text is about. Registration
	// announcements name the raid students are about to start (the running or
	// upcoming one); defense announcements name the raid being defended, which
	// by then may already have finished. Getting this wrong would put the next
	// week's raid name into the sign-up message.
	AboutDefense bool

	// GoOnly limits the announcement to Piscine Go. Only that pool runs the
	// programme these texts describe (the FAQ, the offline exam rules, the
	// hackathon); the other pools announce nothing but the defense sign-up.
	GoOnly bool
}

// announcementKinds is the catalogue behind the /announce picker, in the order
// the buttons are shown.
var announcementKinds = []AnnouncementKind{
	{
		ID:           "defense",
		Label:        "🎤 Запись на защиту",
		Message:      MsgStudentMessage,
		NeedsRaid:    true,
		NeedsSheet:   true,
		AboutDefense: true,
	},
	{
		ID:      "faq",
		Label:   "❓ Ответы на вопросы",
		Message: MsgFAQ,
		GoOnly:  true,
	},
	{
		ID:        "exam",
		Label:     "📢 Экзамен и рейд",
		Message:   MsgExamAnnouncement,
		NeedsRaid: true,
		GoOnly:    true,
	},
	{
		ID:      "hackathon",
		Label:   "🚀 Хакатон",
		Message: MsgHackathon,
		GoOnly:  true,
	},
	{
		ID:      "final",
		Label:   "🏁 Финальный экзамен",
		Message: MsgFinalExam,
		GoOnly:  true,
	},
}

// AnnouncementKindsFor returns the announcements offered for a piscine, in
// display order. Every pool has the defense sign-up; the rest belong to Piscine
// Go alone. An unknown piscine gets nothing, which is how the callback handlers
// reject a bad piscine token.
func AnnouncementKindsFor(piscine PiscineType) []AnnouncementKind {
	if !isKnownPiscine(piscine) {
		return nil
	}
	out := make([]AnnouncementKind, 0, len(announcementKinds))
	for _, k := range announcementKinds {
		if k.GoOnly && piscine != PiscineGo {
			continue
		}
		out = append(out, k)
	}
	return out
}

// AnnouncementKindFor looks up one of a piscine's announcements by its callback
// token. The piscine is part of the lookup on purpose: callback data is client
// data, and a hand-crafted "hackathon for Piscine RUST" must not render a
// Go-only announcement for a pool that does not have it.
func AnnouncementKindFor(piscine PiscineType, id string) (AnnouncementKind, bool) {
	for _, k := range AnnouncementKindsFor(piscine) {
		if k.ID == id {
			return k, true
		}
	}
	return AnnouncementKind{}, false
}

// isKnownPiscine reports whether the type is one the bot serves.
func isKnownPiscine(piscine PiscineType) bool {
	for _, p := range AllPiscines() {
		if p == piscine {
			return true
		}
	}
	return false
}
