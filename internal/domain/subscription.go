package domain

import "time"

// Subscription is one bot user's announcement preferences: whether they receive
// announcements at all, and which piscines they care about.
//
// This is deliberately per-USER rather than per-chat: the existing CHAT_IDS
// broadcast targets group chats with no notion of a piscine, so it cannot answer
// "who wants news about Piscine RUST".
type Subscription struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username,omitempty"`

	// Enabled is the on/off switch the user flips with /subscribe. Disabling keeps
	// the piscine selection, so turning announcements back on restores it.
	Enabled bool `json:"enabled"`

	// Piscines are the piscines the user follows. Empty means "no announcements
	// reach this user", not "all of them" — an empty selection is what a fresh
	// record looks like.
	Piscines []PiscineType `json:"piscines,omitempty"`

	UpdatedAt time.Time `json:"updated_at"`
}

// HasPiscine reports whether the user follows the given piscine.
func (s Subscription) HasPiscine(p PiscineType) bool {
	for _, x := range s.Piscines {
		if x == p {
			return true
		}
	}
	return false
}

// Receives reports whether an announcement about piscine should reach this user.
// An empty piscine means an announcement addressed to everyone, which any
// enabled subscriber receives regardless of their selection.
func (s Subscription) Receives(piscine PiscineType) bool {
	if !s.Enabled {
		return false
	}
	if piscine == "" {
		return len(s.Piscines) > 0
	}
	return s.HasPiscine(piscine)
}

// SubscriptionStore persists announcement subscriptions. Implementations must be
// safe for concurrent use.
type SubscriptionStore interface {
	// Get returns the stored subscription for a user and whether one exists.
	Get(userID int64) (Subscription, bool)
	// Save inserts or overwrites the subscription and durably persists the store.
	Save(sub Subscription) error
	// List returns every stored subscription, ordered by UserID.
	List() ([]Subscription, error)
}
