package telegram

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"admin-bot/internal/domain"
	"admin-bot/internal/usecase"
)

// HandleCallbackCreateTable handles the "Обновить таблицу" button press.
func (h *Handler) HandleCallbackCreateTable(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	chatID, ok := callbackChatID(cb)
	if !ok {
		if cb != nil {
			h.answer(ctx, b, cb.ID, "Ошибка: сообщение недоступно")
		}
		return
	}

	if !h.isAuthorized(chatID, cb.From.ID) {
		h.logger.Warn("unauthorized callback", "data", "defense_create", "chat_id", chatID, "user_id", cb.From.ID)
		h.answer(ctx, b, cb.ID, "Недостаточно прав")
		return
	}

	piscine := parsePiscineFromCallback(cb.Data, "defense_create:")
	if piscine == "" {
		h.answer(ctx, b, cb.ID, "Ошибка: неизвестный тип Piscine")
		return
	}

	h.answer(ctx, b, cb.ID, "Обновляю таблицу…")

	if h.sheets == nil {
		_ = h.adapter.SendMessage(ctx, chatID, msgSheetsNotConfigured)
		return
	}

	weekInfo, err := h.raidUC.DetectCurrentWeek(ctx, domain.PiscineType(piscine))
	if err != nil || weekInfo.ActiveRaid == nil {
		_ = h.adapter.SendMessage(ctx, chatID, "⚠️ Не удалось получить данные о рейде.")
		return
	}

	// Same rule as /create_tables: no defense table while registration is open.
	if !weekInfo.RaidStatus.AllowsDefenseTable() {
		h.logger.Info("skip table update: raid not started", "piscine", piscine,
			"raid", weekInfo.ActiveRaid.RaidName, "status", weekInfo.RaidStatus)
		_ = h.adapter.SendMessage(ctx, chatID,
			notStartedLine(domain.PiscineType(piscine), weekInfo.ActiveRaid)+
				"\n\nЕсли таблица нужна заранее — используйте /edit_tables.")
		return
	}

	spreadsheetID, dedicated := h.resolveSpreadsheetID(domain.PiscineType(piscine), weekInfo.WeekNumber)
	if spreadsheetID == "" {
		h.logger.Warn("no sheet configured", "piscine", piscine, "week", weekInfo.WeekNumber, "dedicated", dedicated)
		msg := msgSheetNotConfigured
		if !dedicated {
			msg = msgUniversalSheetNotConfigured
		}
		_ = h.adapter.SendMessage(ctx, chatID, msg)
		return
	}

	raid := weekInfo.ActiveRaid
	url, err := h.updateTableForActiveRaid(ctx, spreadsheetID, raid, nextMonday(h.now()))
	if err != nil {
		h.logger.Error("update defense table failed", "err", err)
		_ = h.adapter.SendMessage(ctx, chatID, "⚠️ Не удалось обновить таблицу. Попробуйте позже.")
		return
	}

	text := fmt.Sprintf("✅ Таблица защит обновлена!\n📊 %s\n🔗 %s", escapeHTML(raid.RaidName), url)
	if err := h.adapter.SendMessage(ctx, chatID, text); err != nil {
		h.logger.Error("send table url failed", "err", err)
	}
}

// HandleCallbackEditParams handles the "Изменить параметры" button press.
func (h *Handler) HandleCallbackEditParams(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	chatID, ok := callbackChatID(cb)
	if !ok {
		if cb != nil {
			h.answer(ctx, b, cb.ID, "Ошибка: сообщение недоступно")
		}
		return
	}

	if !h.isAuthorized(chatID, cb.From.ID) {
		h.logger.Warn("unauthorized callback", "data", "defense_edit", "chat_id", chatID, "user_id", cb.From.ID)
		h.answer(ctx, b, cb.ID, "Недостаточно прав")
		return
	}

	h.answer(ctx, b, cb.ID, "Изменение параметров")

	if err := h.adapter.SendMessage(ctx, chatID, "✏️ Для настройки параметров таблицы отправьте команду /edit_tables"); err != nil {
		h.logger.Error("send edit params response failed", "err", err)
	}
}

// --- Defense Reminder (called by scheduler) ---

// SendDefenseReminderWithKeyboard sends the defense reminder with inline buttons.
func (h *Handler) SendDefenseReminderWithKeyboard(ctx context.Context, chatID int64, piscine domain.PiscineType, text string, schedule *usecase.DefenseSchedule) {
	keyboard := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{Text: "📊 Обновить таблицу", CallbackData: "defense_create:" + string(piscine)},
				{Text: "✏️ Изменить параметры", CallbackData: "defense_edit:" + string(piscine)},
			},
		},
	}

	if err := h.adapter.SendMessageWithKeyboard(ctx, chatID, text, keyboard); err != nil {
		h.logger.Error("send defense reminder with keyboard failed", "chat_id", chatID, "err", err)
	}
}
