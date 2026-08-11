package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"admin-bot/internal/domain"
	"admin-bot/internal/usecase"
)

// jobTimeout bounds a single scheduled fan-out so a slow upstream cannot pin a
// job indefinitely (the HTTP client also has its own per-request timeout).
const jobTimeout = 5 * time.Minute

// CronScheduler sends announcements at specific days/times using cron expressions.
type CronScheduler struct {
	cron   *cron.Cron
	raidUC *usecase.RaidUseCase
	// announceUC delivers the same scheduled announcements to individual
	// subscribers of the piscine, on top of the group chats. nil disables that.
	announceUC *usecase.AnnounceUseCase
	sender     domain.BotSender
	chatIDs    []int64
	sheetURLs  map[domain.PiscineType]map[int]string
	logger     *slog.Logger

	// baseCtx is canceled by Stop(); each job derives a timeout context from it
	// so in-flight work unwinds on shutdown.
	baseCtx context.Context
	cancel  context.CancelFunc

	DefenseCallback func(ctx context.Context, chatID int64, piscine domain.PiscineType, text string, schedule *usecase.DefenseSchedule)
}

func NewCronScheduler(
	raidUC *usecase.RaidUseCase,
	announceUC *usecase.AnnounceUseCase,
	sender domain.BotSender,
	chatIDs []int64,
	timezone string,
	sheetURLs map[domain.PiscineType]map[int]string,
	logger *slog.Logger,
) *CronScheduler {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		logger.Error("invalid timezone, using UTC", "timezone", timezone, "err", err)
		loc = time.UTC
	}

	baseCtx, cancel := context.WithCancel(context.Background())

	s := &CronScheduler{
		cron:       cron.New(cron.WithLocation(loc)),
		raidUC:     raidUC,
		announceUC: announceUC,
		sender:     sender,
		chatIDs:    chatIDs,
		sheetURLs:  sheetURLs,
		logger:     logger,
		baseCtx:    baseCtx,
		cancel:     cancel,
	}
	s.registerJobs()
	return s
}

// jobContext derives a per-run, timeout-bounded context from the scheduler's
// cancelable parent.
func (s *CronScheduler) jobContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.baseCtx, jobTimeout)
}

func (s *CronScheduler) registerJobs() {
	s.mustAdd("0 10 * * 1", func() { s.broadcastMessage(domain.MsgFAQ, nil) })
	s.mustAdd("30 14 * * 4", func() { s.broadcastMessage(domain.MsgExamAnnouncement, nil) })
	s.mustAdd("0 14 * * 4", func() { s.broadcastMessage(domain.MsgFinalExam, nil) })
	s.mustAdd("0 10 * * 5", func() { s.broadcastMessage(domain.MsgHackathon, nil) })
	s.mustAdd("0 15 * * 0", func() { s.broadcastDefenseReminder() })
}

func (s *CronScheduler) mustAdd(spec string, cmd func()) {
	if _, err := s.cron.AddFunc(spec, cmd); err != nil {
		s.logger.Error("failed to add cron job", "spec", spec, "err", err)
	}
}

func (s *CronScheduler) Start() {
	s.cron.Start()
	s.logger.Info("cron scheduler started", "jobs", len(s.cron.Entries()))
}

// Stop gracefully stops the cron scheduler and cancels any in-flight jobs.
func (s *CronScheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
	s.cancel()
	s.logger.Info("cron scheduler stopped")
}

func (s *CronScheduler) broadcastMessage(msgType domain.MessageType, extra map[string]string) {
	ctx, cancel := s.jobContext()
	defer cancel()

	for _, piscine := range domain.AllPiscines() {
		text, err := s.raidUC.BuildMessage(ctx, piscine, msgType, extra)
		if err != nil {
			s.logger.Debug("skip message", "piscine", piscine, "type", msgType, "reason", err)
			continue
		}
		s.sendToAll(ctx, piscine, msgType, text)
		s.sendToSubscribers(ctx, piscine, msgType, text)
	}
}

// sendToSubscribers delivers a scheduled announcement to the users who follow
// this piscine via /subscribe, in addition to the configured group chats. Errors
// are logged, never propagated: a failed personal delivery must not affect the
// rest of the job.
func (s *CronScheduler) sendToSubscribers(ctx context.Context, piscine domain.PiscineType, msgType domain.MessageType, text string) {
	if s.announceUC == nil {
		return
	}
	report, err := s.announceUC.Broadcast(ctx, piscine, text)
	if err != nil {
		s.logger.Error("scheduled announcement to subscribers failed",
			"piscine", piscine, "type", msgType, "sent", report.Sent, "err", err)
		return
	}
	if report.Recipients > 0 {
		s.logger.Info("scheduled announcement delivered to subscribers",
			"piscine", piscine, "type", msgType,
			"recipients", report.Recipients, "sent", report.Sent, "failed", report.Failed())
	}
}

func (s *CronScheduler) sendToAll(ctx context.Context, piscine domain.PiscineType, msgType domain.MessageType, text string) {
	for _, chatID := range s.chatIDs {
		if err := s.sender.SendMessage(ctx, chatID, text); err != nil {
			s.logger.Error("send failed", "piscine", piscine, "type", msgType, "chat_id", chatID, "err", err)
			continue
		}
		s.logger.Info("message sent", "piscine", piscine, "type", msgType, "chat_id", chatID)
	}
}

func (s *CronScheduler) sheetURLFor(piscine domain.PiscineType, week int) string {
	if m, ok := s.sheetURLs[piscine]; ok {
		return m[week]
	}
	return ""
}

func (s *CronScheduler) broadcastDefenseReminder() {
	ctx, cancel := s.jobContext()
	defer cancel()

	for _, piscine := range domain.AllPiscines() {
		text, schedule, err := s.raidUC.BuildDefenseReminder(ctx, piscine)
		if err != nil {
			s.logger.Debug("skip defense reminder", "piscine", piscine, "reason", err)
			continue
		}

		sheetURL := ""
		if weekInfo, err := s.raidUC.DetectCurrentWeek(ctx, piscine); err == nil && weekInfo != nil {
			sheetURL = s.sheetURLFor(piscine, weekInfo.WeekNumber)
			if sheetURL == "" {
				s.logger.Warn("no sheet URL configured for week", "piscine", piscine, "week", weekInfo.WeekNumber)
			}
		}

		studentText, studentErr := s.raidUC.BuildMessage(ctx, piscine, domain.MsgStudentMessage,
			map[string]string{"SHEET_URL": sheetURL})
		if studentErr != nil {
			s.logger.Debug("skip student message", "piscine", piscine, "reason", studentErr)
		}

		for _, chatID := range s.chatIDs {
			if s.DefenseCallback != nil {
				s.DefenseCallback(ctx, chatID, piscine, text, schedule)
			} else if err := s.sender.SendMessage(ctx, chatID, text); err != nil {
				s.logger.Error("send defense reminder failed", "chat_id", chatID, "err", err)
			}

			if studentErr == nil {
				if err := s.sender.SendMessage(ctx, chatID, studentText); err != nil {
					s.logger.Error("send student message failed", "piscine", piscine, "chat_id", chatID, "err", err)
				}
			}
		}
	}
}
