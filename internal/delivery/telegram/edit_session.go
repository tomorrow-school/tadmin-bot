package telegram

import (
	"sync"
	"time"

	"admin-bot/internal/domain"
)

// editStep is the current stage of an /edit_tables dialog.
type editStep int

const (
	stepPiscine     editStep = iota // choosing the piscine (inline)
	stepRaid                        // choosing the raid/week (inline)
	stepColumns                     // awaiting the number of columns (text)
	stepSlotMinutes                 // choosing per-slot minutes (inline)
	stepTimeRange                   // awaiting the "HH:MM-HH:MM" window (text)
	stepBreaks                      // choosing breaks yes/no (inline)
	stepBreakTime                   // awaiting the break time "HH:MM" (text)
)

// editTableSession holds the state accumulated across one /edit_tables dialog.
// It is intentionally in-memory only: a bot restart drops in-flight dialogs,
// which is acceptable for a short interactive flow.
type editTableSession struct {
	Step editStep

	// Piscine selection. IsOther distinguishes a dynamically discovered pool
	// (identified by EventID) from a fixed PiscineType.
	IsOther bool
	Piscine domain.PiscineType
	EventID int
	Label   string // display label for an "other" pool

	// Raid/week selection. The dates are kept so the dialog can tell the admin
	// they are preparing a table before the raid starts (allowed here, unlike the
	// automatic /create_tables path).
	WeekNumber int
	RaidName   string
	TeamsCount int
	RaidStatus domain.RaidStatus
	RaidStart  time.Time

	// Layout parameters gathered from the admin.
	Columns       int
	SlotMinutes   int
	StartHour     int
	StartMinute   int
	EndHour       int
	EndMinute     int
	IncludeBreaks bool
	BreakHour     int
	BreakMinute   int
}

// awaitsText reports whether the session is currently waiting on a free-text
// reply (column count or time range). The text catch-all handler only fires
// when this is true, so ordinary messages are never swallowed.
func (s *editTableSession) awaitsText() bool {
	return s.Step == stepColumns || s.Step == stepTimeRange || s.Step == stepBreakTime
}

// editSessionStore is a concurrency-safe map of chat ID → active dialog.
type editSessionStore struct {
	mu       sync.Mutex
	sessions map[int64]*editTableSession
}

func newEditSessionStore() *editSessionStore {
	return &editSessionStore{sessions: make(map[int64]*editTableSession)}
}

// start creates (or overwrites) the session for a chat, beginning at the
// piscine-selection step. A second /edit_tables therefore restarts cleanly
// rather than stacking dialogs.
func (st *editSessionStore) start(chatID int64) *editTableSession {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := &editTableSession{Step: stepPiscine}
	st.sessions[chatID] = s
	return s
}

// get returns the active session for a chat, or (nil, false) if none.
func (st *editSessionStore) get(chatID int64) (*editTableSession, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[chatID]
	return s, ok
}

// clear removes any session for a chat. Returns whether one existed.
func (st *editSessionStore) clear(chatID int64) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	_, ok := st.sessions[chatID]
	delete(st.sessions, chatID)
	return ok
}

// awaitingText reports whether the chat has a session waiting on a text reply.
// Used by the text catch-all match function.
func (st *editSessionStore) awaitingText(chatID int64) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[chatID]
	return ok && s.awaitsText()
}
