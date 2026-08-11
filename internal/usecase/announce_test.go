package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"admin-bot/internal/domain"
)

// memSubStore is an in-memory domain.SubscriptionStore for the tests.
type memSubStore struct {
	mu      sync.Mutex
	subs    map[int64]domain.Subscription
	saveErr error
	listErr error
}

func newMemSubStore(subs ...domain.Subscription) *memSubStore {
	m := &memSubStore{subs: make(map[int64]domain.Subscription, len(subs))}
	for _, s := range subs {
		m.subs[s.UserID] = s
	}
	return m
}

func (m *memSubStore) Get(userID int64) (domain.Subscription, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[userID]
	return s, ok
}

func (m *memSubStore) Save(sub domain.Subscription) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[sub.UserID] = sub
	return nil
}

func (m *memSubStore) List() ([]domain.Subscription, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Deterministic order: by user ID, like the file store.
	out := make([]domain.Subscription, 0, len(m.subs))
	for _, id := range []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10} {
		if s, ok := m.subs[id]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// recordingSender captures the fan-out and can fail for specific users.
type recordingSender struct {
	mu       sync.Mutex
	sent     []int64
	texts    []string
	failFor  map[int64]bool
	sendErr  error
	callback func()
}

func (r *recordingSender) SendMessage(ctx context.Context, chatID int64, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.callback != nil {
		r.callback()
	}
	if r.failFor[chatID] {
		return errors.New("blocked by user")
	}
	if r.sendErr != nil {
		return r.sendErr
	}
	r.sent = append(r.sent, chatID)
	r.texts = append(r.texts, text)
	return nil
}

// newTestAnnounceUC builds a use case with the throttle disabled so the tests do
// not spend real time pacing sends.
func newTestAnnounceUC(store domain.SubscriptionStore, sender domain.BotSender) *AnnounceUseCase {
	uc := NewAnnounceUseCase(store, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	uc.sendInterval = 0
	uc.now = func() time.Time { return time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC) }
	return uc
}

// TestGetUnknownUser verifies an unseen user reads as "announcements off,
// nothing selected" rather than an error.
func TestGetUnknownUser(t *testing.T) {
	uc := newTestAnnounceUC(newMemSubStore(), &recordingSender{})
	got := uc.Get(42)
	if got.UserID != 42 || got.Enabled || len(got.Piscines) != 0 {
		t.Errorf("Get(42) = %+v, want a disabled, empty subscription", got)
	}
}

// TestTogglePiscine covers the selection rules, including the two conveniences:
// the first pick turns announcements on, and removing the last one turns them off.
func TestTogglePiscine(t *testing.T) {
	store := newMemSubStore()
	uc := newTestAnnounceUC(store, &recordingSender{})

	// First pick enables the feature.
	sub, err := uc.TogglePiscine(1, "nargiz", domain.PiscineGo)
	if err != nil {
		t.Fatalf("TogglePiscine: %v", err)
	}
	if !sub.Enabled {
		t.Error("first selected piscine should enable announcements")
	}
	if !sub.HasPiscine(domain.PiscineGo) {
		t.Errorf("Piscines = %v, want Go", sub.Piscines)
	}
	if sub.Username != "nargiz" {
		t.Errorf("Username = %q, want the current handle", sub.Username)
	}

	// A second pick keeps both, in AllPiscines order.
	sub, err = uc.TogglePiscine(1, "nargiz", domain.PiscineRUST)
	if err != nil {
		t.Fatalf("TogglePiscine: %v", err)
	}
	if len(sub.Piscines) != 2 || sub.Piscines[0] != domain.PiscineGo || sub.Piscines[1] != domain.PiscineRUST {
		t.Errorf("Piscines = %v, want [Go, RUST] in AllPiscines order", sub.Piscines)
	}

	// Removing one keeps the rest and stays enabled.
	sub, err = uc.TogglePiscine(1, "nargiz", domain.PiscineGo)
	if err != nil {
		t.Fatalf("TogglePiscine: %v", err)
	}
	if sub.HasPiscine(domain.PiscineGo) {
		t.Error("Go should have been removed")
	}
	if !sub.Enabled {
		t.Error("announcements should stay on while a piscine remains")
	}

	// Removing the last one disables the feature — nothing left to receive.
	sub, err = uc.TogglePiscine(1, "nargiz", domain.PiscineRUST)
	if err != nil {
		t.Fatalf("TogglePiscine: %v", err)
	}
	if len(sub.Piscines) != 0 {
		t.Errorf("Piscines = %v, want empty", sub.Piscines)
	}
	if sub.Enabled {
		t.Error("announcements should be off once no piscine is selected")
	}
}

// TestToggleEnabledKeepsSelection verifies the on/off switch is a pause: the
// piscine selection survives it.
func TestToggleEnabledKeepsSelection(t *testing.T) {
	store := newMemSubStore(domain.Subscription{
		UserID: 1, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineJS},
	})
	uc := newTestAnnounceUC(store, &recordingSender{})

	off, err := uc.ToggleEnabled(1, "")
	if err != nil {
		t.Fatalf("ToggleEnabled: %v", err)
	}
	if off.Enabled {
		t.Error("expected announcements to be off")
	}
	if !off.HasPiscine(domain.PiscineJS) {
		t.Errorf("selection lost on disable: %v", off.Piscines)
	}

	on, err := uc.ToggleEnabled(1, "")
	if err != nil {
		t.Fatalf("ToggleEnabled: %v", err)
	}
	if !on.Enabled || !on.HasPiscine(domain.PiscineJS) {
		t.Errorf("re-enabling should restore the selection, got %+v", on)
	}
}

// TestRecipientsFilter is the core of the feature: an announcement about one
// piscine reaches only its subscribers, and only those who have it switched on.
func TestRecipientsFilter(t *testing.T) {
	store := newMemSubStore(
		domain.Subscription{UserID: 1, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineGo}},
		domain.Subscription{UserID: 2, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineGo, domain.PiscineRUST}},
		domain.Subscription{UserID: 3, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineRUST}},
		// Paused: selected Go, but announcements are off.
		domain.Subscription{UserID: 4, Enabled: false, Piscines: []domain.PiscineType{domain.PiscineGo}},
		// Enabled but nothing selected — receives nothing.
		domain.Subscription{UserID: 5, Enabled: true},
	)
	uc := newTestAnnounceUC(store, &recordingSender{})

	cases := []struct {
		name    string
		piscine domain.PiscineType
		want    []int64
	}{
		{"go_subscribers_only", domain.PiscineGo, []int64{1, 2}},
		{"rust_subscribers_only", domain.PiscineRUST, []int64{2, 3}},
		{"nobody_follows_js", domain.PiscineJS, nil},
		{"empty_piscine_means_every_subscriber", "", []int64{1, 2, 3}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := uc.Recipients(tc.piscine)
			if err != nil {
				t.Fatalf("Recipients: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d recipients %v, want %v", len(got), ids(got), tc.want)
			}
			for i, want := range tc.want {
				if got[i].UserID != want {
					t.Errorf("recipient[%d] = %d, want %d", i, got[i].UserID, want)
				}
			}
		})
	}
}

func ids(subs []domain.Subscription) []int64 {
	out := make([]int64, 0, len(subs))
	for _, s := range subs {
		out = append(out, s.UserID)
	}
	return out
}

// TestBroadcastDeliversToFilteredAudience checks the fan-out itself: the right
// users, the right text, and a report matching what happened.
func TestBroadcastDeliversToFilteredAudience(t *testing.T) {
	store := newMemSubStore(
		domain.Subscription{UserID: 1, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineGo}},
		domain.Subscription{UserID: 2, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineRUST}},
		domain.Subscription{UserID: 3, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineGo}},
	)
	sender := &recordingSender{}
	uc := newTestAnnounceUC(store, sender)

	report, err := uc.Broadcast(context.Background(), domain.PiscineGo, "Дедлайн в 17:30")
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}

	if report.Recipients != 2 || report.Sent != 2 || report.Failed() != 0 {
		t.Errorf("report = %+v, want 2 recipients / 2 sent / 0 failed", report)
	}
	if len(sender.sent) != 2 || sender.sent[0] != 1 || sender.sent[1] != 3 {
		t.Errorf("sent to %v, want [1 3] (Go subscribers only)", sender.sent)
	}
	for _, text := range sender.texts {
		if text != "Дедлайн в 17:30" {
			t.Errorf("delivered text = %q, want the announcement verbatim", text)
		}
	}
}

// TestBroadcastContinuesAfterFailure verifies one undeliverable user (blocked the
// bot, never opened the chat) does not silence the announcement for everyone
// else, and is reported.
func TestBroadcastContinuesAfterFailure(t *testing.T) {
	store := newMemSubStore(
		domain.Subscription{UserID: 1, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineGo}},
		domain.Subscription{UserID: 2, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineGo}},
		domain.Subscription{UserID: 3, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineGo}},
	)
	sender := &recordingSender{failFor: map[int64]bool{2: true}}
	uc := newTestAnnounceUC(store, sender)

	report, err := uc.Broadcast(context.Background(), domain.PiscineGo, "текст")
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}

	if report.Sent != 2 {
		t.Errorf("Sent = %d, want 2", report.Sent)
	}
	if report.Failed() != 1 || report.FailedUsers[0] != 2 {
		t.Errorf("FailedUsers = %v, want [2]", report.FailedUsers)
	}
	if len(sender.sent) != 2 || sender.sent[0] != 1 || sender.sent[1] != 3 {
		t.Errorf("sent to %v, want [1 3] — the run must continue past the failure", sender.sent)
	}
}

// TestBroadcastStopsOnCancelledContext verifies a shutdown (or the cron job's
// timeout) interrupts the paced fan-out and returns a partial report rather than
// pretending everything went out.
func TestBroadcastStopsOnCancelledContext(t *testing.T) {
	store := newMemSubStore(
		domain.Subscription{UserID: 1, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineGo}},
		domain.Subscription{UserID: 2, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineGo}},
		domain.Subscription{UserID: 3, Enabled: true, Piscines: []domain.PiscineType{domain.PiscineGo}},
	)
	ctx, cancel := context.WithCancel(context.Background())
	sender := &recordingSender{}
	// Cancel while the first message is being delivered.
	sender.callback = func() { cancel() }

	uc := newTestAnnounceUC(store, sender)
	uc.sendInterval = time.Millisecond // exercise the real pacing path

	report, err := uc.Broadcast(ctx, domain.PiscineGo, "текст")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if report.Recipients != 3 {
		t.Errorf("Recipients = %d, want 3 (the audience was resolved)", report.Recipients)
	}
	if report.Sent >= 3 {
		t.Errorf("Sent = %d, want a partial result after cancellation", report.Sent)
	}
}

// TestBroadcastNoRecipients verifies an empty audience is a successful no-op, not
// an error.
func TestBroadcastNoRecipients(t *testing.T) {
	sender := &recordingSender{}
	uc := newTestAnnounceUC(newMemSubStore(), sender)

	report, err := uc.Broadcast(context.Background(), domain.PiscineJS, "текст")
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if report.Recipients != 0 || report.Sent != 0 {
		t.Errorf("report = %+v, want an empty no-op", report)
	}
	if len(sender.sent) != 0 {
		t.Errorf("nothing should have been sent, got %v", sender.sent)
	}
}

// TestSaveErrorPropagates verifies a failed durable write is reported, so the UI
// never tells the user their choice was saved when it was not.
func TestSaveErrorPropagates(t *testing.T) {
	store := newMemSubStore()
	store.saveErr = errors.New("disk full")
	uc := newTestAnnounceUC(store, &recordingSender{})

	if _, err := uc.TogglePiscine(1, "", domain.PiscineGo); err == nil {
		t.Error("expected the store error to propagate from TogglePiscine")
	}
	if _, err := uc.ToggleEnabled(1, ""); err == nil {
		t.Error("expected the store error to propagate from ToggleEnabled")
	}
}
