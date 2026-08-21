package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"admin-bot/internal/domain"
)

// defaultSendInterval paces a broadcast. The Telegram Bot API allows roughly 30
// messages per second overall (and about one per second into a single chat);
// 50 ms between sends keeps us at ~20/s, comfortably under the limit, so a large
// announcement does not start collecting 429 Too Many Requests.
const defaultSendInterval = 50 * time.Millisecond

// AnnounceUseCase owns announcement subscriptions and the fan-out to them.
type AnnounceUseCase struct {
	store  domain.SubscriptionStore
	sender domain.BotSender
	logger *slog.Logger

	sendInterval time.Duration
	now          func() time.Time // injectable clock, defaults to time.Now
}

// NewAnnounceUseCase wires the use case to its store and sender.
func NewAnnounceUseCase(store domain.SubscriptionStore, sender domain.BotSender, logger *slog.Logger) *AnnounceUseCase {
	return &AnnounceUseCase{
		store:        store,
		sender:       sender,
		logger:       logger,
		sendInterval: defaultSendInterval,
		now:          time.Now,
	}
}

// Get returns the user's subscription, or a zero-valued one (announcements off,
// nothing selected) when they have never used /subscribe.
func (uc *AnnounceUseCase) Get(userID int64) domain.Subscription {
	if sub, ok := uc.store.Get(userID); ok {
		return sub
	}
	return domain.Subscription{UserID: userID}
}

// SetEnabled turns announcements on or off for a user, keeping their piscine
// selection so toggling back restores it.
func (uc *AnnounceUseCase) SetEnabled(userID int64, username string, enabled bool) (domain.Subscription, error) {
	sub := uc.Get(userID)
	sub.Enabled = enabled
	return uc.save(sub, username)
}

// ToggleEnabled flips the on/off switch and returns the new state.
func (uc *AnnounceUseCase) ToggleEnabled(userID int64, username string) (domain.Subscription, error) {
	sub := uc.Get(userID)
	return uc.SetEnabled(userID, username, !sub.Enabled)
}

// TogglePiscine adds or removes one piscine from the user's selection.
//
// Two conveniences, so the two switches never contradict each other in a way the
// user did not intend: picking a piscine for the first time turns announcements
// ON (otherwise the choice would silently do nothing), and removing the last
// piscine turns them OFF (there is nothing left to receive).
func (uc *AnnounceUseCase) TogglePiscine(userID int64, username string, piscine domain.PiscineType) (domain.Subscription, error) {
	sub := uc.Get(userID)
	wasEmpty := len(sub.Piscines) == 0

	if sub.HasPiscine(piscine) {
		kept := make([]domain.PiscineType, 0, len(sub.Piscines))
		for _, p := range sub.Piscines {
			if p != piscine {
				kept = append(kept, p)
			}
		}
		sub.Piscines = kept
		if len(sub.Piscines) == 0 {
			sub.Enabled = false
		}
	} else {
		sub.Piscines = append(sub.Piscines, piscine)
		if wasEmpty {
			sub.Enabled = true
		}
	}

	sortPiscines(sub.Piscines)
	return uc.save(sub, username)
}

// save stamps the record and persists it, refreshing the cached username so the
// admin report shows the current handle.
func (uc *AnnounceUseCase) save(sub domain.Subscription, username string) (domain.Subscription, error) {
	if username != "" {
		sub.Username = username
	}
	sub.UpdatedAt = uc.now()
	if err := uc.store.Save(sub); err != nil {
		return domain.Subscription{}, err
	}
	return sub, nil
}

// Recipients returns the subscriptions that should receive an announcement about
// piscine. An empty piscine addresses every enabled subscriber.
func (uc *AnnounceUseCase) Recipients(piscine domain.PiscineType) ([]domain.Subscription, error) {
	all, err := uc.store.List()
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	var out []domain.Subscription
	for _, sub := range all {
		if sub.Receives(piscine) {
			out = append(out, sub)
		}
	}
	return out, nil
}

// BroadcastReport summarizes one fan-out.
type BroadcastReport struct {
	Piscine domain.PiscineType
	// Recipients is how many subscriptions matched the filter.
	Recipients int
	// Sent is how many messages went out successfully.
	Sent int
	// FailedUsers are the user IDs whose delivery failed — most often someone who
	// never opened a chat with the bot, or who blocked it.
	FailedUsers []int64
}

// Failed is the number of failed deliveries.
func (r BroadcastReport) Failed() int { return len(r.FailedUsers) }

// Broadcast sends text to every subscriber of piscine, pacing the sends to stay
// within Telegram's rate limit. A single failed delivery does not abort the run:
// it is logged, counted, and reported, so one blocked user cannot silence the
// announcement for everyone else. A cancelled context does stop the run, and the
// partial report is returned alongside the error.
func (uc *AnnounceUseCase) Broadcast(ctx context.Context, piscine domain.PiscineType, text string) (BroadcastReport, error) {
	recipients, err := uc.Recipients(piscine)
	if err != nil {
		return BroadcastReport{Piscine: piscine}, err
	}

	report := BroadcastReport{Piscine: piscine, Recipients: len(recipients)}
	for i, sub := range recipients {
		// Pace between sends, not before the first one.
		if i > 0 {
			if err := sleepCtx(ctx, uc.sendInterval); err != nil {
				return report, err
			}
		}

		if err := uc.sender.SendMessage(ctx, sub.UserID, text); err != nil {
			uc.logger.Error("announcement send failed",
				"piscine", piscine, "user_id", sub.UserID, "err", err)
			report.FailedUsers = append(report.FailedUsers, sub.UserID)
			continue
		}
		report.Sent++
		uc.logger.Info("announcement sent", "piscine", piscine, "user_id", sub.UserID)
	}

	uc.logger.Info("announcement broadcast finished", "piscine", piscine,
		"recipients", report.Recipients, "sent", report.Sent, "failed", report.Failed())
	return report, nil
}

// sleepCtx waits for d, or returns early with the context's error if it is
// cancelled first (shutdown, or the cron job's timeout).
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// sortPiscines orders a selection the same way AllPiscines does, so the
// /subscribe keyboard and the stored file stay in a predictable order.
func sortPiscines(piscines []domain.PiscineType) {
	order := make(map[domain.PiscineType]int, len(domain.AllPiscines()))
	for i, p := range domain.AllPiscines() {
		order[p] = i
	}
	sort.Slice(piscines, func(i, j int) bool {
		oi, oki := order[piscines[i]]
		oj, okj := order[piscines[j]]
		if oki != okj {
			return oki // known piscines first
		}
		if oki && okj {
			return oi < oj
		}
		return piscines[i] < piscines[j]
	})
}
