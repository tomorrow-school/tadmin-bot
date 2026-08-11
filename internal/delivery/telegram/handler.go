package telegram

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"admin-bot/internal/domain"
	"admin-bot/internal/infra/sheets"
	tgAdapter "admin-bot/internal/infra/telegram"
	"admin-bot/internal/usecase"
)

// Handler processes Telegram commands and callback queries.
type Handler struct {
	raidUC           *usecase.RaidUseCase
	updatesUC        *usecase.UpdatesUseCase
	accessUC         *usecase.AccessUseCase
	announceUC       *usecase.AnnounceUseCase // nil if the subscription store failed to load
	adapter          *tgAdapter.Adapter
	sheets           *sheets.Client // nil if Google Sheets is not configured
	sheetIDs         map[domain.PiscineType]map[int]string
	sheetURLs        map[domain.PiscineType]map[int]string
	sheetSlots       map[string][]string // spreadsheet ID → SHEET_* names pointing at it
	universalSheetID string              // shared fallback table for RUST / "other" pools
	authorized       map[int64]bool      // allowlist of group chat IDs permitted to issue commands
	superAdminID     int64               // the single user who approves/rejects access requests
	loc              *time.Location      // configured timezone, used for date arithmetic
	editSessions     *editSessionStore   // in-memory /edit_tables dialog state, keyed by chat
	announceSessions *announceSessionStore
	logger           *slog.Logger
}

const (
	msgSheetsNotConfigured         = "⚠️ Google Sheets не настроен. Добавьте GOOGLE_CREDENTIALS_FILE в .env"
	msgSheetNotConfigured          = "⚠️ Таблица для этой недели не настроена. Добавьте SHEET_*_WEEK* в .env"
	msgUniversalSheetNotConfigured = "⚠️ Универсальная таблица не настроена. Добавьте SHEET_UNIVERSAL в .env"
)

// NewHandler wires the dependencies needed for command and callback handling.
//
// authorizedChatIDs is the allowlist of chats permitted to issue commands and
// press inline buttons. loc is the configured timezone used for the defense
// date (nextMonday) so the date does not drift between the container clock
// (UTC) and the cron timezone.
func NewHandler(
	raidUC *usecase.RaidUseCase,
	updatesUC *usecase.UpdatesUseCase,
	accessUC *usecase.AccessUseCase,
	announceUC *usecase.AnnounceUseCase,
	adapter *tgAdapter.Adapter,
	sheetsClient *sheets.Client,
	sheetIDs map[domain.PiscineType]map[int]string,
	sheetURLs map[domain.PiscineType]map[int]string,
	sheetSlots map[string][]string,
	universalSheetID string,
	authorizedChatIDs []int64,
	superAdminID int64,
	loc *time.Location,
	logger *slog.Logger,
) *Handler {
	allow := make(map[int64]bool, len(authorizedChatIDs))
	for _, id := range authorizedChatIDs {
		allow[id] = true
	}
	if loc == nil {
		loc = time.UTC
	}
	return &Handler{
		raidUC:           raidUC,
		updatesUC:        updatesUC,
		accessUC:         accessUC,
		announceUC:       announceUC,
		adapter:          adapter,
		sheets:           sheetsClient,
		sheetIDs:         sheetIDs,
		sheetURLs:        sheetURLs,
		sheetSlots:       sheetSlots,
		universalSheetID: universalSheetID,
		authorized:       allow,
		superAdminID:     superAdminID,
		loc:              loc,
		editSessions:     newEditSessionStore(),
		announceSessions: newAnnounceSessionStore(),
		logger:           logger,
	}
}

// isAuthorized reports whether a command from userID in chatID may run.
//
// The rule is: super-admin always; otherwise the user must be approved AND the
// chat must be either their own private chat (where Telegram guarantees
// chatID == userID) or a group in the allowlist. An approved user therefore
// works in DMs with no per-chat configuration, while group access still
// requires the group to be explicitly allowlisted — fail-closed by design.
func (h *Handler) isAuthorized(chatID, userID int64) bool {
	if userID == h.superAdminID {
		return true
	}
	if !h.isApprovedUser(userID) {
		return false
	}
	if chatID == userID {
		return true // the approved user's own private chat
	}
	return h.authorized[chatID]
}

// isApprovedUser reports whether the user has an approved access request.
func (h *Handler) isApprovedUser(userID int64) bool {
	return h.accessUC != nil && h.accessUC.IsApproved(userID)
}

// guard authorizes an incoming command message. It returns the chat ID and
// true when the caller may proceed. When it returns false it has already
// handled the unauthorized case: in a private chat it runs the access-request
// flow (so the user isn't met with silence), in a group it logs quietly.
func (h *Handler) guard(ctx context.Context, update *models.Update) (int64, bool) {
	if update.Message == nil || update.Message.From == nil {
		return 0, false
	}
	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID

	if h.isAuthorized(chatID, userID) {
		return chatID, true
	}

	if chatID == userID {
		// Private chat: auto-register / report status instead of ignoring.
		h.handleAccessEntry(ctx, update.Message)
	} else {
		h.logger.Warn("unauthorized command", "chat_id", chatID, "user_id", userID)
	}
	return chatID, false
}

func (h *Handler) lookupSheetID(piscine domain.PiscineType, week int) string {
	if m, ok := h.sheetIDs[piscine]; ok {
		return m[week]
	}
	return ""
}

// isDedicatedPiscine reports whether a piscine has its own per-week sheet
// (Go/JS/AI1/AI2/AI3). Everything else — Piscine RUST and any dynamically
// discovered pool — shares the universal fallback table.
func isDedicatedPiscine(p domain.PiscineType) bool {
	switch p {
	case domain.PiscineGo, domain.PiscineJS, domain.PiscineAI_1, domain.PiscineAI_2, domain.PiscineAI_3:
		return true
	default:
		return false
	}
}

// resolveSpreadsheetID picks the spreadsheet for a (piscine, week): the
// dedicated per-week sheet for Go/JS/AI1/AI2/AI3, or the shared universal table
// for Piscine RUST and any other (non-dedicated) piscine. dedicated reports
// which branch was taken so callers can tailor their "not configured" message.
func (h *Handler) resolveSpreadsheetID(piscine domain.PiscineType, week int) (id string, dedicated bool) {
	if isDedicatedPiscine(piscine) {
		return h.lookupSheetID(piscine, week), true
	}
	return h.universalSheetID, false
}

// now returns the current time in the configured location.
func (h *Handler) now() time.Time { return time.Now().In(h.loc) }

// callbackChatID safely extracts the originating chat ID from a callback query.
// Telegram callbacks can carry a nil or inaccessible Message; callers must
// check ok before using the result.
func callbackChatID(cb *models.CallbackQuery) (int64, bool) {
	// cb.Message is a value of type models.MaybeInaccessibleMessage (not a
	// pointer), so it can't be nil-compared. Its inner .Message is *models.Message
	// and is nil when the callback's source message is inaccessible (too old, or
	// an inline-message origin) — that's the case we must guard against.
	if cb == nil || cb.Message.Message == nil {
		return 0, false
	}
	return cb.Message.Message.Chat.ID, true
}

// answer is a best-effort callback acknowledgement that logs (rather than
// ignores) failures.
func (h *Handler) answer(ctx context.Context, b *bot.Bot, id, text string) {
	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: id,
		Text:            text,
	}); err != nil {
		h.logger.Warn("answer callback failed", "err", err)
	}
}
