package telegram

import (
	"sync"

	"admin-bot/internal/domain"
)

// announceStep is the current stage of an /announce dialog.
type announceStep int

const (
	stepAnnouncePiscine announceStep = iota // choosing the audience (inline)
	stepAnnounceText                        // awaiting the announcement text
	stepAnnounceConfirm                     // awaiting send/cancel (inline)
)

// announceSession holds the state of one /announce dialog. Like the
// /edit_tables session it is in-memory only: a restart drops half-composed
// announcements, which is the safe direction to fail.
type announceSession struct {
	Step announceStep

	// Piscine is the audience filter; empty means every subscriber.
	Piscine domain.PiscineType
	Label   string

	// Text is the announcement body, already HTML-escaped for sending.
	Text string
	// Recipients is the audience size measured when the text was accepted, shown
	// in the confirmation prompt.
	Recipients int
}

func (s *announceSession) awaitsText() bool { return s.Step == stepAnnounceText }

// announceSessionStore is a concurrency-safe map of chat ID → active dialog.
type announceSessionStore struct {
	mu       sync.Mutex
	sessions map[int64]*announceSession
}

func newAnnounceSessionStore() *announceSessionStore {
	return &announceSessionStore{sessions: make(map[int64]*announceSession)}
}

// start creates (or overwrites) the session for a chat, so a second /announce
// restarts cleanly rather than stacking dialogs.
func (st *announceSessionStore) start(chatID int64) *announceSession {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := &announceSession{Step: stepAnnouncePiscine}
	st.sessions[chatID] = s
	return s
}

func (st *announceSessionStore) get(chatID int64) (*announceSession, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[chatID]
	return s, ok
}

// clear removes any session for a chat. Returns whether one existed.
func (st *announceSessionStore) clear(chatID int64) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	_, ok := st.sessions[chatID]
	delete(st.sessions, chatID)
	return ok
}

// awaitingText reports whether the chat has a session waiting on the
// announcement body. Used by the text catch-all match function, so ordinary
// chatter is never swallowed.
func (st *announceSessionStore) awaitingText(chatID int64) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[chatID]
	return ok && s.awaitsText()
}
