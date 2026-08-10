package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"admin-bot/internal/domain"
	"admin-bot/internal/infra/sheets"
	"admin-bot/internal/usecase"
)

func (h *Handler) HandleHelp(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID, ok := h.guard(ctx, update)
	if !ok {
		return
	}

	text := "📋 <b>Команды:</b>\n\n" +
		"/help — показать это сообщение\n" +
		"/raidgo — информация о рейде Piscine Go\n" +
		"/raidjs — информация о рейде Piscine JS\n" +
		"/raidai1 — информация о рейде Piscine AI 1\n" +
		"/raidai2 — информация о рейде Piscine AI 2\n" +
		"/raidai3 — информация о рейде Piscine AI 3\n" +
		"/raidrust — информация о рейде Piscine RUST\n" +
		"/week — текущая неделя для всех Piscine\n" +
		"/create_tables — обновить Google Sheets таблицы защит для всех активных рейдов\n" +
		"/edit_tables — создать/обновить таблицу защиты с ручными параметрами\n" +
		"/get_region_updates — статистика обновлений по всем регионам\n" +
		"/get_astana_updates — статистика обновлений Astana\n" +
		"/get_event {id} — информация об ивенте (участники, регистрация, даты)\n"

	if err := h.adapter.SendMessage(ctx, chatID, text); err != nil {
		h.logger.Error("send help failed", "err", err)
	}
}

func (h *Handler) HandleRaidGo(ctx context.Context, b *bot.Bot, update *models.Update) {
	h.handleRaidInfo(ctx, update, domain.PiscineGo)
}

func (h *Handler) HandleRaidJS(ctx context.Context, b *bot.Bot, update *models.Update) {
	h.handleRaidInfo(ctx, update, domain.PiscineJS)
}

func (h *Handler) HandleRaidAI1(ctx context.Context, b *bot.Bot, update *models.Update) {
	h.handleRaidInfo(ctx, update, domain.PiscineAI_1)
}

func (h *Handler) HandleRaidAI2(ctx context.Context, b *bot.Bot, update *models.Update) {
	h.handleRaidInfo(ctx, update, domain.PiscineAI_2)
}

func (h *Handler) HandleRaidAI3(ctx context.Context, b *bot.Bot, update *models.Update) {
	h.handleRaidInfo(ctx, update, domain.PiscineAI_3)
}

func (h *Handler) HandleRaidRust(ctx context.Context, b *bot.Bot, update *models.Update) {
	h.handleRaidInfo(ctx, update, domain.PiscineRUST)
}

func (h *Handler) HandleWeek(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID, ok := h.guard(ctx, update)
	if !ok {
		return
	}

	piscines, err := h.raidUC.GetCurrentPiscines(ctx)
	if err != nil || len(piscines) == 0 {
		// Do NOT echo err.Error() into the chat: an upstream error can carry
		// sensitive fragments, and chat messages persist on Telegram's servers.
		// Log the detail server-side, show a generic line here.
		if err != nil {
			h.logger.Error("get current piscines failed", "err", err)
		} else {
			h.logger.Warn("get current piscines returned empty")
		}
		if sendErr := h.adapter.SendMessage(ctx, chatID, "❌ Не удалось получить список текущих бассейнов"); sendErr != nil {
			h.logger.Error("send week info failed", "err", sendErr)
		}
		return
	}

	var sb strings.Builder
	for _, p := range piscines {
		label := p.Label()
		if label == "" {
			label = p.Path
		}

		weekInfo, err := h.raidUC.DetectCurrentWeekForEvent(ctx, p)
		if err != nil {
			h.logger.Error("detect week for event failed", "path", p.Path, "eventID", p.ID, "err", err)
			fmt.Fprintf(&sb, "📌 <b>%s</b> (id %d): не удалось получить данные\n", escapeHTML(label), p.ID)
			continue
		}

		raidName := "—"
		if weekInfo.ActiveRaid != nil && weekInfo.ActiveRaid.RaidName != "" {
			raidName = weekInfo.ActiveRaid.RaidName
		}

		fmt.Fprintf(&sb, "📌 <b>%s</b> (id %d): Неделя %d | Рейд: %s\n",
			escapeHTML(label), p.ID, weekInfo.WeekNumber, escapeHTML(raidName))
	}

	if err := h.adapter.SendMessage(ctx, chatID, sb.String()); err != nil {
		h.logger.Error("send week info failed", "err", err)
	}
}

// HandleTables handles the /create_tables command.
func (h *Handler) HandleTables(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID, ok := h.guard(ctx, update)
	if !ok {
		return
	}

	if h.sheets == nil {
		_ = h.adapter.SendMessage(ctx, chatID, msgSheetsNotConfigured)
		return
	}

	// Defense is on Monday — computed in the configured timezone so the date
	// matches what admins see locally regardless of the container clock.
	defenseDate := nextMonday(h.now())

	var lines []string
	updatedCount := 0

	for _, piscine := range domain.AllPiscines() {
		weekInfo, err := h.raidUC.DetectCurrentWeek(ctx, piscine)
		if err != nil {
			h.logger.Warn("detect week failed", "piscine", piscine, "err", err)
			lines = append(lines, fmt.Sprintf("❌ Ошибка при обновлении таблицы (%s)", piscine))
			continue
		}

		if weekInfo.ActiveRaid == nil {
			h.logger.Info("skip: no active raid", "piscine", piscine)
			continue
		}

		raid := weekInfo.ActiveRaid

		spreadsheetID, dedicated := h.resolveSpreadsheetID(piscine, weekInfo.WeekNumber)
		if spreadsheetID == "" {
			h.logger.Warn("no sheet configured", "piscine", piscine, "week", weekInfo.WeekNumber, "dedicated", dedicated)
			if dedicated {
				lines = append(lines, fmt.Sprintf("⚠️ %s — таблица для недели %d не настроена", piscine, weekInfo.WeekNumber))
			} else {
				lines = append(lines, fmt.Sprintf("⚠️ %s — универсальная таблица (SHEET_UNIVERSAL) не настроена", piscine))
			}
			continue
		}

		url, err := h.updateTableForActiveRaid(ctx, spreadsheetID, raid, defenseDate)
		if err != nil {
			h.logger.Error("update defense table failed", "piscine", piscine, "raid", raid.RaidName, "err", err)
			lines = append(lines, fmt.Sprintf("❌ Ошибка при обновлении таблицы (%s)", piscine))
			continue
		}

		updatedCount++
		lines = append(lines, fmt.Sprintf("✅ Таблица обновлена (%s — %s): %s",
			escapeHTML(string(piscine)), escapeHTML(raid.RaidName), url))
	}

	resp := "ℹ️ На этой неделе нет активных рейдов — обновлять нечего."
	if len(lines) > 0 {
		resp = strings.Join(lines, "\n")
	}

	if err := h.adapter.SendMessage(ctx, chatID, resp); err != nil {
		h.logger.Error("send create_tables result failed", "err", err)
	}

	h.logger.Info("create_tables finished", "updated", updatedCount, "total_lines", len(lines))
}

func (h *Handler) HandleAstanaUpdates(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID, ok := h.guard(ctx, update)
	if !ok {
		return
	}

	var sb strings.Builder

	info, err := h.updatesUC.GetAstanaUpdates(ctx)
	if err != nil {
		h.logger.Error("get astana updates failed", "err", err)
		fmt.Fprintf(&sb, "❌ Не удалось получить данные об обновлениях Astana\n")
	} else {
		date := time.Now().In(h.loc).Format("02.01.2006")

		fmt.Fprintf(&sb, "### %s - Астана\n", date)
		fmt.Fprintf(&sb, "- %d тотал заявок\n", info.Total)
		fmt.Fprintf(&sb, "- %d тотал прошли игры\n", info.Succeeded)
		fmt.Fprintf(&sb, "- %d reg на check-in\n", info.Checkin)
		writePiscineRegistrations(&sb, info.PiscineRegistrations)
	}

	if err := h.adapter.SendMessage(ctx, chatID, sb.String()); err != nil {
		h.logger.Error("send astana updates failed", "err", err)
	}
}

func (h *Handler) HandleRegionUpdates(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID, ok := h.guard(ctx, update)
	if !ok {
		return
	}

	report, err := h.updatesUC.GetRegionUpdates(ctx)
	if err != nil {
		h.logger.Error("get region updates failed", "err", err)
		text := "❌ Не удалось получить список регионов"
		if errors.Is(err, domain.ErrNoCampuses) {
			text = "⚠️ Список регионов пуст"
		}
		if sendErr := h.adapter.SendMessage(ctx, chatID, text); sendErr != nil {
			h.logger.Error("send region updates error failed", "err", sendErr)
		}
		return
	}

	for _, regionErr := range report.Errors {
		h.logger.Error("get region stats failed", "region", regionErr.Region, "err", regionErr.Err)
	}

	if len(report.Regions) == 0 {
		text := "❌ Не удалось получить статистику ни для одного региона"
		if err := h.adapter.SendMessage(ctx, chatID, text); err != nil {
			h.logger.Error("send empty region updates failed", "err", err)
		}
		return
	}

	date := h.now().Format("02.01.2006")
	for _, info := range report.Regions {
		if err := h.adapter.SendMessage(ctx, chatID, formatRegionUpdatesMessage(info, date)); err != nil {
			h.logger.Error("send region updates failed", "region", info.Region, "err", err)
		}
	}

	if len(report.Errors) > 0 {
		failedRegions := make([]string, 0, len(report.Errors))
		for _, regionErr := range report.Errors {
			region := strings.TrimSpace(regionErr.Region)
			if region == "" {
				region = "unknown"
			}
			failedRegions = append(failedRegions, escapeHTML(region))
		}
		text := "⚠️ Не удалось получить данные по регионам: " + strings.Join(failedRegions, ", ")
		if err := h.adapter.SendMessage(ctx, chatID, text); err != nil {
			h.logger.Error("send partial region updates failed", "err", err)
		}
	}
}

// HandleGetEvent handles "/get_event {id}" — it fetches and reports the event
// window, registration window(s) and participant count for a single event ID.
func (h *Handler) HandleGetEvent(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID, ok := h.guard(ctx, update)
	if !ok {
		return
	}

	id, ok := parseEventID(update.Message.Text)
	if !ok {
		_ = h.adapter.SendMessage(ctx, chatID,
			"ℹ️ Использование: <code>/get_event {id}</code>\nНапример: <code>/get_event 12345</code>")
		return
	}

	info, err := h.updatesUC.GetEventInfo(ctx, id)
	if err != nil {
		// Never echo err.Error() into chat (it can carry sensitive fragments);
		// log server-side and show a generic message.
		h.logger.Error("get event info failed", "id", id, "err", err)
		_ = h.adapter.SendMessage(ctx, chatID, "❌ Не удалось получить информацию об ивенте")
		return
	}
	if info == nil {
		_ = h.adapter.SendMessage(ctx, chatID, fmt.Sprintf("⚠️ Ивент с ID %d не найден", id))
		return
	}

	if err := h.adapter.SendMessage(ctx, chatID, formatEventInfoMessage(*info, h.loc)); err != nil {
		h.logger.Error("send event info failed", "err", err)
	}
}

// parseEventID extracts a positive integer event ID from a "/get_event {id}"
// message. It tolerates a "@botname" suffix on the command and extra spaces,
// and returns false when no valid ID is present.
func parseEventID(text string) (int, bool) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return 0, false
	}
	id, err := strconv.Atoi(fields[1])
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (h *Handler) handleRaidInfo(ctx context.Context, update *models.Update, piscine domain.PiscineType) {
	chatID, ok := h.guard(ctx, update)
	if !ok {
		return
	}

	weekInfo, err := h.raidUC.DetectCurrentWeek(ctx, piscine)
	if err != nil {
		h.logger.Error("detect week failed", "piscine", piscine, "err", err)
		_ = h.adapter.SendMessage(ctx, chatID, "⚠️ Не удалось определить текущую неделю. Попробуйте позже.")
		return
	}

	if weekInfo.ActiveRaid == nil {
		text := fmt.Sprintf("📌 <b>%s</b> — Неделя %d (Final Exam)\nАктивных рейдов нет.",
			escapeHTML(string(piscine)), weekInfo.WeekNumber)
		_ = h.adapter.SendMessage(ctx, chatID, text)
		return
	}

	raid := weekInfo.ActiveRaid
	text := fmt.Sprintf(
		"📌 <b>%s</b> — Неделя %d\n"+
			"⚔️ Рейд: <b>%s</b>\n"+
			"👥 Команд: %d\n"+
			"📅 %s — %s",
		escapeHTML(string(piscine)),
		weekInfo.WeekNumber,
		escapeHTML(raid.RaidName),
		raid.TeamsCount,
		raid.StartDate.Format("02.01 15:04"),
		raid.EndDate.Format("02.01 15:04"),
	)

	if err := h.adapter.SendMessage(ctx, chatID, text); err != nil {
		h.logger.Error("send raid info failed", "err", err)
	}
}

func (h *Handler) updateTableForActiveRaid(ctx context.Context, spreadsheetID string, raid *domain.RaidInfo, defenseDate time.Time) (string, error) {
	schedule := usecase.CalculateDefenseSchedule(usecase.AutoScheduleParams(raid.TeamsCount))
	return h.sheets.UpdateDefenseTable(ctx, spreadsheetID, sheets.DefenseTableParams{
		RaidName:    raid.RaidName,
		DefenseDate: defenseDate,
		Schedule:    schedule,
	})
}
