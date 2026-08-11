// Package substore is a JSON-file-backed implementation of
// domain.SubscriptionStore. It mirrors accessstore: the project has no database,
// so persistence is a single JSON file kept in sync with an in-memory map under
// an RWMutex, and writes are atomic (temp file + rename) so a crash mid-write
// cannot leave corrupt JSON on disk.
package substore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"admin-bot/internal/domain"
)

// Store is a concurrency-safe, file-backed SubscriptionStore.
type Store struct {
	path string

	mu   sync.RWMutex
	subs map[int64]domain.Subscription
}

// New creates a store bound to path and loads any existing data. A missing file
// means a fresh install (empty store); a corrupt one is reported rather than
// swallowed, so the caller can decide whether to start without subscriptions.
func New(path string) (*Store, error) {
	s := &Store{
		path: path,
		subs: make(map[int64]domain.Subscription),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create subscription store dir: %w", err)
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read subscription store %q: %w", s.path, err)
	}
	if len(data) == 0 {
		return nil
	}

	var subs []domain.Subscription
	if err := json.Unmarshal(data, &subs); err != nil {
		return fmt.Errorf("parse subscription store %q: %w", s.path, err)
	}

	s.subs = make(map[int64]domain.Subscription, len(subs))
	for _, sub := range subs {
		s.subs[sub.UserID] = sub
	}
	return nil
}

// Get returns the stored subscription for userID and whether it exists.
func (s *Store) Get(userID int64) (domain.Subscription, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sub, ok := s.subs[userID]
	return sub, ok
}

// Save upserts sub and rewrites the file atomically. The in-memory map is
// updated only after the durable write succeeds, so a failed write leaves memory
// and disk consistent.
func (s *Store) Save(sub domain.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := make(map[int64]domain.Subscription, len(s.subs)+1)
	for k, v := range s.subs {
		next[k] = v
	}
	next[sub.UserID] = sub

	if err := s.persist(next); err != nil {
		return err
	}
	s.subs = next
	return nil
}

// List returns every subscription ordered by UserID, so broadcasts iterate in a
// stable order.
func (s *Store) List() ([]domain.Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedSubs(s.subs), nil
}

// persist writes the snapshot to a temp file in the same directory and renames
// it over the target, so readers never observe a partial write.
func (s *Store) persist(subs map[int64]domain.Subscription) error {
	data, err := json.MarshalIndent(sortedSubs(subs), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal subscription store: %w", err)
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".subscriptions-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp subscription store: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail out before the rename succeeds.
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp subscription store: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp subscription store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp subscription store: %w", err)
	}

	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("rename subscription store into place: %w", err)
	}
	return nil
}

// sortedSubs returns the map's values sorted by UserID, for a stable,
// diff-friendly file and a deterministic broadcast order.
func sortedSubs(subs map[int64]domain.Subscription) []domain.Subscription {
	list := make([]domain.Subscription, 0, len(subs))
	for _, sub := range subs {
		list = append(list, sub)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].UserID < list[j].UserID })
	return list
}
