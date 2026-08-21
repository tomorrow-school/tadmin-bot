package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"admin-bot/internal/domain"
	"admin-bot/internal/usecase"
)

// Callback-data prefixes for the announcement flows.
const (
	cbSubToggle  = "sub_toggle"   // exact — turn announcements on/off
	cbSubPiscine = "sub_piscine:" // + piscine type — follow/unfollow one piscine

	cbAnnPiscine = "ann_piscine:" // + piscine type — pick the pool
	cbAnnKind    = "ann_kind:"    // + "<announcement ID>:<piscine type>"
)

const msgAnnounceNotConfigured = "⚠️ Подписки на анонсы не настроены (нет хранилища подписок)."

// HandleSubscribe shows the caller's announcement settings with the toggles.
// This is the "включить/отключить функцию" switch: while it is on, the user
// receives the scheduled (cron) announcements for the piscines they picked.
// /announce is a separate thing — it only answers with a ready text.
func (h *Handler) HandleSubscribe(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID, ok := h.guard(ctx, update)
	if !ok {
		return
	}
	if h.announceUC == nil {
		_ = h.adapter.SendMessage(ctx, chatID, msgAnnounceNotConfigured)
		return
	}

	sub := h.announceUC.Get(update.Message.From.ID)
	if err := h.adapter.SendMessageWithKeyboard(ctx, chatID, subscribeText(sub), subscribeKeyboard(sub)); err != nil {
		h.logger.Error("send subscribe screen failed", "err", err)
	}
}

// subscribeText renders the current state of a subscription.
func subscribeText(sub domain.Subscription) string {
	var sb strings.Builder
	sb.WriteString("🔔 <b>Анонсы</b>\n\n")
	if sub.Enabled && len(sub.Piscines) > 0 {
		sb.WriteString("Статус: <b>включены</b>\n")
	} else {
		sb.WriteString("Статус: <b>выключены</b>\n")
	}

	if len(sub.Piscines) == 0 {
		sb.WriteString("Бассейны: не выбраны\n\nОтметьте бассейны, о которых хотите получать анонсы.")
		return sb.String()
	}

	names := make([]string, 0, len(sub.Piscines))
	for _, p := range sub.Piscines {
		names = append(names, escapeHTML(string(p)))
	}
	fmt.Fprintf(&sb, "Бассейны: %s\n\nНажмите на бассейн, чтобы подписаться или отписаться.", strings.Join(names, ", "))
	return sb.String()
}

// subscribeKeyboard renders one toggle per piscine (checked when followed) plus
// the master on/off switch.
func subscribeKeyboard(sub domain.Subscription) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton

	for i, p := range domain.AllPiscines() {
		mark := "☐"
		if sub.HasPiscine(p) {
			mark = "✅"
		}
		row = append(row, models.InlineKeyboardButton{
			Text:         fmt.Sprintf("%s %s", mark, piscineShortLabel(p)),
			CallbackData: cbSubPiscine + string(p),
		})
		if (i+1)%3 == 0 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}

	toggle := "🔔 Включить анонсы"
	if sub.Enabled {
		toggle = "🔕 Отключить анонсы"
	}
	rows = append(rows, []models.InlineKeyboardButton{{Text: toggle, CallbackData: cbSubToggle}})

	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// piscineShortLabel trims the "Piscine " prefix for button captions, which have
// little room.
func piscineShortLabel(p domain.PiscineType) string {
	return strings.TrimPrefix(string(p), "Piscine ")
}

// HandleCallbackSubToggle flips the master switch and refreshes the screen.
func (h *Handler) HandleCallbackSubToggle(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb, chatID, ok := h.subscribeCallbackGuard(ctx, b, update)
	if !ok {
		return
	}

	sub, err := h.announceUC.ToggleEnabled(cb.From.ID, cb.From.Username)
	if err != nil {
		h.logger.Error("toggle subscription failed", "user_id", cb.From.ID, "err", err)
		h.answer(ctx, b, cb.ID, "Не удалось сохранить")
		return
	}

	ack := "Анонсы выключены"
	if sub.Enabled {
		ack = "Анонсы включены"
	}
	h.answer(ctx, b, cb.ID, ack)
	h.refreshSubscribeScreen(ctx, cb, chatID, sub)
}

// HandleCallbackSubPiscine follows/unfollows one piscine and refreshes the
// screen.
func (h *Handler) HandleCallbackSubPiscine(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb, chatID, ok := h.subscribeCallbackGuard(ctx, b, update)
	if !ok {
		return
	}

	piscine := parsePiscineFromCallback(cb.Data, cbSubPiscine)
	if piscine == "" {
		h.answer(ctx, b, cb.ID, "Неизвестный бассейн")
		return
	}

	sub, err := h.announceUC.TogglePiscine(cb.From.ID, cb.From.Username, domain.PiscineType(piscine))
	if err != nil {
		h.logger.Error("toggle piscine subscription failed", "user_id", cb.From.ID, "err", err)
		h.answer(ctx, b, cb.ID, "Не удалось сохранить")
		return
	}

	ack := "Отписка: " + piscine
	if sub.HasPiscine(domain.PiscineType(piscine)) {
		ack = "Подписка: " + piscine
	}
	h.answer(ctx, b, cb.ID, ack)
	h.refreshSubscribeScreen(ctx, cb, chatID, sub)
}

// refreshSubscribeScreen edits the settings message in place, falling back to a
// fresh message when the original can no longer be edited (too old).
func (h *Handler) refreshSubscribeScreen(ctx context.Context, cb *models.CallbackQuery, chatID int64, sub domain.Subscription) {
	text, keyboard := subscribeText(sub), subscribeKeyboard(sub)
	if err := h.adapter.EditMessageWithKeyboard(ctx, chatID, cb.Message.Message.ID, text, keyboard); err != nil {
		h.logger.Warn("edit subscribe screen failed, sending a new one", "err", err)
		if err := h.adapter.SendMessageWithKeyboard(ctx, chatID, text, keyboard); err != nil {
			h.logger.Error("send subscribe screen failed", "err", err)
		}
	}
}

// HandleAnnounce starts the /announce flow: pick a piscine, then (for Piscine
// Go, which has several) pick which announcement — and the bot answers with the
// ready text. Nothing is broadcast: the admin copies the text where it belongs.
func (h *Handler) HandleAnnounce(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID, ok := h.guard(ctx, update)
	if !ok {
		return
	}

	if err := h.adapter.SendMessageWithKeyboard(ctx, chatID,
		"📣 <b>Анонс</b>\n\nПо какому бассейну?", announcePiscineKeyboard(),
	); err != nil {
		h.logger.Error("send announce piscine picker failed", "err", err)
	}
}

// announcePiscineKeyboard offers one button per piscine, three to a row.
func announcePiscineKeyboard() *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton

	for i, p := range domain.AllPiscines() {
		row = append(row, models.InlineKeyboardButton{
			Text:         piscineShortLabel(p),
			CallbackData: cbAnnPiscine + string(p),
		})
		if (i+1)%3 == 0 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// HandleCallbackAnnouncePiscine answers with the piscine's announcement. A pool
// with exactly one announcement (everything except Piscine Go) skips the menu
// and gets the text straight away.
func (h *Handler) HandleCallbackAnnouncePiscine(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb, chatID, ok := h.callbackGuard(ctx, b, update)
	if !ok {
		return
	}

	name := parsePiscineFromCallback(cb.Data, cbAnnPiscine)
	piscine := domain.PiscineType(name)
	kinds := domain.AnnouncementKindsFor(piscine)
	if name == "" || len(kinds) == 0 {
		h.answer(ctx, b, cb.ID, "Неизвестный бассейн")
		return
	}
	h.answer(ctx, b, cb.ID, name)

	if len(kinds) == 1 {
		h.sendAnnouncement(ctx, chatID, piscine, kinds[0])
		return
	}

	if err := h.adapter.SendMessageWithKeyboard(ctx, chatID,
		fmt.Sprintf("<b>%s</b> — какой анонс?", escapeHTML(name)),
		announceKindKeyboard(piscine, kinds),
	); err != nil {
		h.logger.Error("send announce kind picker failed", "err", err)
	}
}

// announceKindKeyboard lists a piscine's announcements, one per row (the
// captions are too long to pair up). The piscine travels in the callback data,
// so the flow needs no stored session — a button pressed hours later still
// works.
func announceKindKeyboard(piscine domain.PiscineType, kinds []domain.AnnouncementKind) *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, len(kinds))
	for _, k := range kinds {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         k.Label,
			CallbackData: cbAnnKind + k.ID + ":" + string(piscine),
		}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// HandleCallbackAnnounceKind answers with the chosen announcement's text.
func (h *Handler) HandleCallbackAnnounceKind(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb, chatID, ok := h.callbackGuard(ctx, b, update)
	if !ok {
		return
	}

	kind, piscine, ok := parseAnnounceKindCallback(cb.Data)
	if !ok {
		h.answer(ctx, b, cb.ID, "Неизвестный анонс")
		return
	}
	h.answer(ctx, b, cb.ID, kind.Label)
	h.sendAnnouncement(ctx, chatID, piscine, kind)
}

// parseAnnounceKindCallback splits "ann_kind:<id>:<piscine>" back into its
// parts, checking that both halves are known.
func parseAnnounceKindCallback(data string) (domain.AnnouncementKind, domain.PiscineType, bool) {
	rest, ok := strings.CutPrefix(data, cbAnnKind)
	if !ok {
		return domain.AnnouncementKind{}, "", false
	}
	id, name, found := strings.Cut(rest, ":")
	if !found {
		return domain.AnnouncementKind{}, "", false
	}
	// Look the announcement up WITHIN the piscine's own menu, so a hand-crafted
	// callback cannot pull a Go-only announcement into another pool.
	piscine := domain.PiscineType(name)
	kind, found := domain.AnnouncementKindFor(piscine, id)
	if !found {
		return domain.AnnouncementKind{}, "", false
	}
	return kind, piscine, true
}

// sendAnnouncement renders one announcement and posts it as the reply.
//
// The text is HTML-escaped: announcements are plain text, and escaping
// guarantees a message containing "<" or "&" is delivered instead of being
// rejected by Telegram's HTML parser.
func (h *Handler) sendAnnouncement(ctx context.Context, chatID int64, piscine domain.PiscineType, kind domain.AnnouncementKind) {
	text, warning, err := h.renderAnnouncement(ctx, piscine, kind)
	if err != nil {
		h.logger.Warn("render announcement failed", "kind", kind.ID, "piscine", piscine, "err", err)
		if sendErr := h.adapter.SendMessage(ctx, chatID, announceRenderErrorText(kind, piscine, err)); sendErr != nil {
			h.logger.Error("send announce render error failed", "err", sendErr)
		}
		return
	}

	msg := escapeHTML(text)
	if warning != "" {
		msg += "\n\n" + warning
	}
	if err := h.adapter.SendMessage(ctx, chatID, msg); err != nil {
		h.logger.Error("send announcement failed", "kind", kind.ID, "piscine", piscine, "err", err)
	}
}

// renderAnnouncement builds one announcement's text. It resolves the piscine's
// current week first — both to fill {{RAID_NAME}} and to find the defense-table
// link — and returns a warning for the one thing the admin cannot see in the
// text itself: a missing table link.
func (h *Handler) renderAnnouncement(ctx context.Context, piscine domain.PiscineType, kind domain.AnnouncementKind) (text, warning string, err error) {
	var info *usecase.CurrentWeekInfo
	if kind.NeedsRaid || kind.NeedsSheet {
		info, err = h.raidUC.DetectCurrentWeek(ctx, piscine)
		if err != nil {
			return "", "", err
		}
	}

	extra := map[string]string{}
	if kind.NeedsSheet {
		week := 0
		if info != nil {
			week = info.WeekNumber
		}
		if raid, _ := info.DefenseRaid(); raid != nil && raid.WeekNumber > 0 {
			// The table belongs to the raid's own week, which on the final-exam
			// week is not the week the piscine has moved on to.
			week = raid.WeekNumber
		}
		extra["SHEET_URL"] = h.sheetURLFor(piscine, week)
		if extra["SHEET_URL"] == "" {
			warning = "⚠️ Ссылка на таблицу защит не настроена — подставить в текст нечего."
		}
	}

	text, err = h.raidUC.RenderAnnouncement(piscine, kind, info, extra)
	if err != nil {
		return "", "", err
	}
	return text, warning, nil
}

// announceRenderErrorText explains why an announcement could not be built, in
// terms the admin can act on.
func announceRenderErrorText(kind domain.AnnouncementKind, piscine domain.PiscineType, err error) string {
	switch {
	case errors.Is(err, domain.ErrNoRaidForAnnouncement):
		return fmt.Sprintf("⚠️ У %s сейчас нет рейда, о котором можно писать — «%s» не составить.",
			escapeHTML(string(piscine)), escapeHTML(kind.Label))
	case errors.Is(err, domain.ErrNoActivePiscine):
		return fmt.Sprintf("⚠️ %s сейчас не идёт — анонс по нему не составить.", escapeHTML(string(piscine)))
	case errors.Is(err, domain.ErrTemplateNotFound):
		return fmt.Sprintf("⚠️ Шаблон анонса «%s» не найден в messages/.", escapeHTML(kind.Label))
	default:
		return "⚠️ Не удалось составить анонс. Попробуйте позже."
	}
}

// callbackGuard is the shared callback bookkeeping: it resolves the chat and
// rejects unauthorized callers. ok=false means it has already answered.
func (h *Handler) callbackGuard(ctx context.Context, b *bot.Bot, update *models.Update) (*models.CallbackQuery, int64, bool) {
	cb := update.CallbackQuery
	chatID, ok := callbackChatID(cb)
	if !ok {
		if cb != nil {
			h.answer(ctx, b, cb.ID, "Ошибка: сообщение недоступно")
		}
		return nil, 0, false
	}
	if !h.isAuthorized(chatID, cb.From.ID) {
		h.logger.Warn("unauthorized callback", "data", cb.Data, "chat_id", chatID, "user_id", cb.From.ID)
		h.answer(ctx, b, cb.ID, "Недостаточно прав")
		return nil, 0, false
	}
	return cb, chatID, true
}

// subscribeCallbackGuard additionally refuses when the subscription store is
// unavailable, which is what the /subscribe toggles need.
func (h *Handler) subscribeCallbackGuard(ctx context.Context, b *bot.Bot, update *models.Update) (*models.CallbackQuery, int64, bool) {
	cb, chatID, ok := h.callbackGuard(ctx, b, update)
	if !ok {
		return nil, 0, false
	}
	if h.announceUC == nil {
		h.answer(ctx, b, cb.ID, "Подписки не настроены")
		return nil, 0, false
	}
	return cb, chatID, true
}
