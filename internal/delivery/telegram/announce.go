package telegram

import (
	"context"
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

	cbAnnPiscine = "ann_piscine:" // + piscine type, or "all" — pick the audience
	cbAnnSend    = "ann_send"     // exact — confirm and send
	cbAnnCancel  = "ann_cancel"   // exact — discard the draft
)

// announceAllArg is the audience token meaning "every subscriber, whatever they
// follow".
const announceAllArg = "all"

const msgAnnounceNotConfigured = "⚠️ Рассылка анонсов не настроена (нет хранилища подписок)."

// HandleSubscribe shows the caller's announcement settings with the toggles.
// This is the "включить/отключить функцию" switch: while it is on, the user
// receives both manual /announce messages and the scheduled announcements for
// the piscines they picked.
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
	cb, chatID, ok := h.announceCallbackGuard(ctx, b, update)
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
	cb, chatID, ok := h.announceCallbackGuard(ctx, b, update)
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

// HandleAnnounce starts the /announce dialog: pick the audience, send the text,
// confirm.
func (h *Handler) HandleAnnounce(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID, ok := h.guard(ctx, update)
	if !ok {
		return
	}
	if h.announceUC == nil {
		_ = h.adapter.SendMessage(ctx, chatID, msgAnnounceNotConfigured)
		return
	}

	// A fresh /announce discards any half-composed draft in this chat.
	h.announceSessions.start(chatID)

	if err := h.adapter.SendMessageWithKeyboard(ctx, chatID,
		"📣 <b>Анонс</b>\n\nКому отправить?", announceAudienceKeyboard(),
	); err != nil {
		h.logger.Error("send announce audience picker failed", "err", err)
	}
}

// announceAudienceKeyboard offers one button per piscine plus "все подписчики".
func announceAudienceKeyboard() *models.InlineKeyboardMarkup {
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
	rows = append(rows, []models.InlineKeyboardButton{{
		Text:         "Все подписчики",
		CallbackData: cbAnnPiscine + announceAllArg,
	}})

	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// HandleCallbackAnnouncePiscine records the audience and asks for the text.
func (h *Handler) HandleCallbackAnnouncePiscine(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb, chatID, ok := h.announceCallbackGuard(ctx, b, update)
	if !ok {
		return
	}
	s, ok := h.announceSessions.get(chatID)
	if !ok {
		h.answer(ctx, b, cb.ID, "Сессия истекла — отправьте /announce")
		return
	}

	arg := strings.TrimPrefix(cb.Data, cbAnnPiscine)
	if arg == announceAllArg {
		s.Piscine = ""
		s.Label = "все подписчики"
	} else {
		piscine := parsePiscineFromCallback(cb.Data, cbAnnPiscine)
		if piscine == "" {
			h.answer(ctx, b, cb.ID, "Неизвестный бассейн")
			return
		}
		s.Piscine = domain.PiscineType(piscine)
		s.Label = piscine
	}
	s.Step = stepAnnounceText
	h.answer(ctx, b, cb.ID, s.Label)

	// ForceReply so the answer reaches the bot even in a group with privacy mode.
	if err := h.askText(ctx, chatID, fmt.Sprintf(
		"Аудитория: <b>%s</b>\n\nОтправьте текст анонса одним сообщением.\n/cancel — отмена.",
		escapeHTML(s.Label),
	)); err != nil {
		h.logger.Error("send announce text prompt failed", "err", err)
	}
}

// HandleAnnounceText accepts the announcement body and shows the confirmation
// prompt. The text is HTML-escaped: announcements are plain text, and escaping
// guarantees a message with "<" or "&" in it is delivered instead of rejected by
// Telegram's HTML parser mid-broadcast.
func (h *Handler) HandleAnnounceText(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.From == nil {
		return
	}
	chatID := update.Message.Chat.ID
	if !h.isAuthorized(chatID, update.Message.From.ID) {
		return
	}
	s, ok := h.announceSessions.get(chatID)
	if !ok || s.Step != stepAnnounceText {
		return
	}

	text := strings.TrimSpace(update.Message.Text)
	if text == "" {
		_ = h.askText(ctx, chatID, "⚠️ Текст анонса пустой. Отправьте текст ещё раз.")
		return
	}

	recipients, err := h.announceUC.Recipients(s.Piscine)
	if err != nil {
		h.logger.Error("count announcement recipients failed", "err", err)
		h.announceSessions.clear(chatID)
		_ = h.adapter.SendMessage(ctx, chatID, "⚠️ Не удалось получить список подписчиков. Попробуйте позже.")
		return
	}

	s.Text = escapeHTML(text)
	s.Recipients = len(recipients)
	s.Step = stepAnnounceConfirm

	if len(recipients) == 0 {
		h.announceSessions.clear(chatID)
		_ = h.adapter.SendMessage(ctx, chatID, fmt.Sprintf(
			"ℹ️ На «%s» пока никто не подписан — отправлять некому.\nПодписка включается командой /subscribe.",
			escapeHTML(s.Label)))
		return
	}

	preview := fmt.Sprintf(
		"📣 <b>Проверьте анонс</b>\n\nАудитория: <b>%s</b>\nПолучателей: <b>%d</b>\n\n———\n%s\n———",
		escapeHTML(s.Label), s.Recipients, s.Text,
	)
	keyboard := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: "📨 Отправить", CallbackData: cbAnnSend},
			{Text: "❌ Отмена", CallbackData: cbAnnCancel},
		}},
	}
	if err := h.adapter.SendMessageWithKeyboard(ctx, chatID, preview, keyboard); err != nil {
		h.logger.Error("send announce confirmation failed", "err", err)
	}
}

// HandleCallbackAnnounceSend performs the broadcast and reports the outcome.
func (h *Handler) HandleCallbackAnnounceSend(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb, chatID, ok := h.announceCallbackGuard(ctx, b, update)
	if !ok {
		return
	}
	s, ok := h.announceSessions.get(chatID)
	if !ok || s.Step != stepAnnounceConfirm {
		h.answer(ctx, b, cb.ID, "Сессия истекла — отправьте /announce")
		return
	}

	// Acknowledge before fanning out: the broadcast is paced, so it can outlast
	// the callback-answer window.
	h.answer(ctx, b, cb.ID, "Отправляю…")
	h.announceSessions.clear(chatID)

	h.logger.Info("announcement broadcast requested",
		"user_id", cb.From.ID, "piscine", s.Piscine, "recipients", s.Recipients)

	report, err := h.announceUC.Broadcast(ctx, s.Piscine, s.Text)
	if err != nil {
		h.logger.Error("announcement broadcast failed", "piscine", s.Piscine, "err", err)
	}

	if err := h.adapter.SendMessage(ctx, chatID, announceReportText(s.Label, report, err != nil)); err != nil {
		h.logger.Error("send announce report failed", "err", err)
	}
}

// HandleCallbackAnnounceCancel discards the draft.
func (h *Handler) HandleCallbackAnnounceCancel(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb, chatID, ok := h.announceCallbackGuard(ctx, b, update)
	if !ok {
		return
	}
	h.announceSessions.clear(chatID)
	h.answer(ctx, b, cb.ID, "Отменено")
	if err := h.adapter.SendMessage(ctx, chatID, "❌ Анонс не отправлен."); err != nil {
		h.logger.Error("send announce cancel failed", "err", err)
	}
}

// announceReportText summarizes a finished broadcast for the admin: how many got
// it, how many did not, and which users failed (usually someone who never opened
// a chat with the bot, or blocked it). interrupted marks a run cut short by a
// cancelled context, so a partial report is not read as complete.
func announceReportText(label string, report usecase.BroadcastReport, interrupted bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "📣 <b>Анонс отправлен</b>\nАудитория: %s\n✅ Успешно: %d из %d",
		escapeHTML(label), report.Sent, report.Recipients)

	if failed := report.Failed(); failed > 0 {
		fmt.Fprintf(&sb, "\n⚠️ Не доставлено: %d", failed)
		ids := make([]string, 0, len(report.FailedUsers))
		for _, id := range report.FailedUsers {
			ids = append(ids, fmt.Sprintf("<code>%d</code>", id))
		}
		fmt.Fprintf(&sb, " (%s)\nВозможно, эти пользователи не открывали чат с ботом или заблокировали его.",
			strings.Join(ids, ", "))
	}
	if interrupted {
		sb.WriteString("\n⚠️ Рассылка прервана — отправлены не все сообщения.")
	}
	return sb.String()
}

// announceCallbackGuard is the shared callback bookkeeping for the announcement
// flows: it resolves the chat, rejects unauthorized callers, and refuses when the
// feature is unconfigured. ok=false means it has already answered.
func (h *Handler) announceCallbackGuard(ctx context.Context, b *bot.Bot, update *models.Update) (*models.CallbackQuery, int64, bool) {
	cb := update.CallbackQuery
	chatID, ok := callbackChatID(cb)
	if !ok {
		if cb != nil {
			h.answer(ctx, b, cb.ID, "Ошибка: сообщение недоступно")
		}
		return nil, 0, false
	}
	if !h.isAuthorized(chatID, cb.From.ID) {
		h.logger.Warn("unauthorized announce callback", "data", cb.Data, "chat_id", chatID, "user_id", cb.From.ID)
		h.answer(ctx, b, cb.ID, "Недостаточно прав")
		return nil, 0, false
	}
	if h.announceUC == nil {
		h.answer(ctx, b, cb.ID, "Рассылка не настроена")
		return nil, 0, false
	}
	return cb, chatID, true
}
