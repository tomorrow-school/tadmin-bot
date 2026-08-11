package substore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"admin-bot/internal/domain"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "nested", "subscriptions.json")
}

// TestSaveAndReload verifies a subscription survives a restart: the file is the
// only state, so what Save writes must be exactly what a new Store reads.
func TestSaveAndReload(t *testing.T) {
	path := tempPath(t)
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	want := domain.Subscription{
		UserID:    777,
		Username:  "nargiz",
		Enabled:   true,
		Piscines:  []domain.PiscineType{domain.PiscineGo, domain.PiscineRUST},
		UpdatedAt: time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
	}
	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := New(path)
	if err != nil {
		t.Fatalf("New (reload): %v", err)
	}
	got, ok := reloaded.Get(777)
	if !ok {
		t.Fatal("subscription missing after reload")
	}
	if got.UserID != want.UserID || got.Username != want.Username || got.Enabled != want.Enabled {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if len(got.Piscines) != 2 || !got.HasPiscine(domain.PiscineGo) || !got.HasPiscine(domain.PiscineRUST) {
		t.Errorf("Piscines = %v, want Go and RUST", got.Piscines)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, want.UpdatedAt)
	}
}

// TestSaveOverwrites verifies Save is an upsert keyed by user ID, so toggling a
// setting does not accumulate duplicate records.
func TestSaveOverwrites(t *testing.T) {
	store, err := New(tempPath(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := store.Save(domain.Subscription{UserID: 1, Enabled: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Save(domain.Subscription{UserID: 1, Enabled: false}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d records, want 1", len(list))
	}
	if list[0].Enabled {
		t.Error("the later value should win")
	}
}

// TestListOrderedByUserID pins the stable order the broadcast relies on.
func TestListOrderedByUserID(t *testing.T) {
	store, err := New(tempPath(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, id := range []int64{300, 100, 200} {
		if err := store.Save(domain.Subscription{UserID: id, Enabled: true}); err != nil {
			t.Fatalf("Save(%d): %v", id, err)
		}
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []int64{100, 200, 300}
	if len(list) != len(want) {
		t.Fatalf("List returned %d records, want %d", len(list), len(want))
	}
	for i, id := range want {
		if list[i].UserID != id {
			t.Errorf("list[%d] = %d, want %d", i, list[i].UserID, id)
		}
	}
}

// TestMissingFileIsEmptyStore verifies a fresh install starts clean (and creates
// its directory) instead of failing.
func TestMissingFileIsEmptyStore(t *testing.T) {
	path := tempPath(t)
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected an empty store, got %v", list)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("store directory was not created: %v", err)
	}
}

// TestCorruptFileIsReported verifies bad JSON surfaces as an error rather than
// silently dropping everyone's subscriptions.
func TestCorruptFileIsReported(t *testing.T) {
	path := tempPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := New(path); err == nil {
		t.Fatal("expected an error for a corrupt store file")
	}
}

// TestNoTempFilesLeftBehind verifies the atomic write cleans up after itself, so
// the data directory does not fill with .tmp files.
func TestNoTempFilesLeftBehind(t *testing.T) {
	path := tempPath(t)
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := int64(1); i <= 5; i++ {
		if err := store.Save(domain.Subscription{UserID: i, Enabled: true}); err != nil {
			t.Fatalf("Save(%d): %v", i, err)
		}
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory contains %v, want only %s", names, filepath.Base(path))
	}
}
