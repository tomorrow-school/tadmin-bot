package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"admin-bot/internal/domain"
	"admin-bot/internal/infra/sheets"
	"admin-bot/internal/usecase"
)

// Callback-data prefixes for the /edit_tables dialog.
const (
	cbEditPiscine  = "edit_piscine:"  // + piscine type ("Piscine Go") — fixed piscine chosen
	cbEditPoolList = "edit_pool_list" // exact — show dynamically discovered pools
	cbEditPool     = "edit_pool:"     // + event ID — an "other" pool chosen
	cbEditRaid     = "edit_raid:"     // + week number — a raid/week chosen
	cbEditSlot     = "edit_slot:"     // + minutes — per-slot length chosen
	cbEditBreaks   = "edit_breaks:"   // + "yes"/"no" — breaks toggle
)

// slotMinuteChoices are the per-slot lengths offered as inline buttons.
var slotMinuteChoices = []int{10, 15, 20, 30, 40}

// HandleEditTables starts (or restarts) the /edit_tables dialog: it authorizes
// the caller, resets any prior dialog for the chat, and shows the piscine
// picker.
func (h *Handler) HandleEditTables(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID, ok := h.guard(ctx, update)
	if !ok {
		return
	}

	if h.sheets == nil {
		_ = h.adapter.SendMessage(ctx, chatID, msgSheetsNotConfigured)
		return
	}

	// A fresh /edit_tables overwrites any half-finished dialog in this chat.
	h.editSessions.start(chatID)

	if err := h.adapter.SendMessageWithKeyboard(ctx, chatID,
		"✏️ <b>Настройка таблицы защиты</b>\n\nВыберите бассейн:",
		piscinePickerKeyboard(),
	); err != nil {
		h.logger.Error("send edit_tables piscine picker failed", "err", err)
	}
}

// HandleCancel aborts an active /edit_tables dialog in the chat.
func (h *Handler) HandleCancel(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID, ok := h.guard(ctx, update)
	if !ok {
		return
	}
	if h.editSessions.clear(chatID) {
		_ = h.adapter.SendMessage(ctx, chatID, "❌ Диалог настройки таблицы отменён.")
		return
	}
	_ = h.adapter.SendMessage(ctx, chatID, "ℹ️ Нет активного диалога для отмены.")
}

// HandleCallbackEditPiscine handles selection of a fixed piscine.
func (h *Handler) HandleCallbackEditPiscine(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb, chatID, ok := h.editCallbackGuard(ctx, b, update)
	if !ok {
		return
	}

	s, ok := h.editSessions.get(chatID)
	if !ok {
		h.answer(ctx, b, cb.ID, "Сессия истекла — отправьте /edit_tables")
		return
	}

	piscine := parsePiscineFromCallback(cb.Data, cbEditPiscine)
	if piscine == "" {
		h.answer(ctx, b, cb.ID, "Неизвестный бассейн")
		return
	}
	s.IsOther = false
	s.Piscine = domain.PiscineType(piscine)
	s.EventID = 0
	s.Label = piscine
	s.Step = stepRaid
	h.answer(ctx, b, cb.ID, piscine)

	raids, err := h.raidUC.ListRaidsWithWeeks(ctx, s.Piscine)
	if err != nil {
		h.logger.Error("list raids with weeks failed", "piscine", s.Piscine, "err", err)
		h.editSessions.clear(chatID)
		_ = h.adapter.SendMessage(ctx, chatID, "⚠️ Не удалось получить список рейдов. Попробуйте позже.")
		return
	}
	h.presentRaids(ctx, chatID, s.Label, raids)
}

// HandleCallbackEditPoolList shows the currently discovered "other" pools.
func (h *Handler) HandleCallbackEditPoolList(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb, chatID, ok := h.editCallbackGuard(ctx, b, update)
	if !ok {
		return
	}
	if _, ok := h.editSessions.get(chatID); !ok {
		h.answer(ctx, b, cb.ID, "Сессия истекла — отправьте /edit_tables")
		return
	}
	h.answer(ctx, b, cb.ID, "Загружаю бассейны…")

	piscines, err := h.raidUC.GetCurrentPiscines(ctx)
	if err != nil || len(piscines) == 0 {
		if err != nil {
			h.logger.Error("get current piscines failed", "err", err)
		}
		_ = h.adapter.SendMessage(ctx, chatID, "⚠️ Не удалось получить список текущих бассейнов.")
		return
	}

	var rows [][]models.InlineKeyboardButton
	for _, p := range piscines {
		label := p.Label()
		if label == "" {
			label = p.Path
		}
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         fmt.Sprintf("%s (id %d)", label, p.ID),
			CallbackData: fmt.Sprintf("%s%d", cbEditPool, p.ID),
		}})
	}

	if err := h.adapter.SendMessageWithKeyboard(ctx, chatID,
		"Выберите бассейн:", &models.InlineKeyboardMarkup{InlineKeyboard: rows},
	); err != nil {
		h.logger.Error("send pool list failed", "err", err)
	}
}

// HandleCallbackEditPool handles selection of a discovered "other" pool.
func (h *Handler) HandleCallbackEditPool(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb, chatID, ok := h.editCallbackGuard(ctx, b, update)
	if !ok {
		return
	}
	s, ok := h.editSessions.get(chatID)
	if !ok {
		h.answer(ctx, b, cb.ID, "Сессия истекла — отправьте /edit_tables")
		return
	}

	idStr := strings.TrimPrefix(cb.Data, cbEditPool)
	eventID, err := strconv.Atoi(idStr)
	if err != nil {
		h.answer(ctx, b, cb.ID, "Неверный ID бассейна")
		return
	}

	// Resolve a friendly label from the current discovery list (best-effort).
	label := fmt.Sprintf("Бассейн id %d", eventID)
	if piscines, err := h.raidUC.GetCurrentPiscines(ctx); err == nil {
		for _, p := range piscines {
			if p.ID == eventID {
				if l := p.Label(); l != "" {
					label = l
				}
				break
			}
		}
	}

	s.IsOther = true
	s.Piscine = domain.PiscineRUST // unused for routing; "other" always maps to universal
	s.EventID = eventID
	s.Label = label
	s.Step = stepRaid
	h.answer(ctx, b, cb.ID, label)

	raids, err := h.raidUC.ListRaidsForEvent(ctx, eventID)
	if err != nil {
		h.logger.Error("list raids for event failed", "event_id", eventID, "err", err)
		h.editSessions.clear(chatID)
		_ = h.adapter.SendMessage(ctx, chatID, "⚠️ Не удалось получить список рейдов. Попробуйте позже.")
		return
	}
	h.presentRaids(ctx, chatID, label, raids)
}

// presentRaids renders one button per raid ("<name> — Неделя N"). It stops the
// dialog with a notice when the piscine currently has no raids.
func (h *Handler) presentRaids(ctx context.Context, chatID int64, label string, raids []domain.RaidInfo) {
	if len(raids) == 0 {
		h.editSessions.clear(chatID)
		_ = h.adapter.SendMessage(ctx, chatID, fmt.Sprintf("ℹ️ У бассейна «%s» сейчас нет рейдов.", escapeHTML(label)))
		return
	}

	var rows [][]models.InlineKeyboardButton
	for _, r := range raids {
		name := r.RaidName
		if name == "" {
			name = "рейд"
		}
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         fmt.Sprintf("%s — Неделя %d", name, r.WeekNumber),
			CallbackData: fmt.Sprintf("%s%d", cbEditRaid, r.WeekNumber),
		}})
	}

	if err := h.adapter.SendMessageWithKeyboard(ctx, chatID,
		"Выберите рейд:", &models.InlineKeyboardMarkup{InlineKeyboard: rows},
	); err != nil {
		h.logger.Error("send raid picker failed", "err", err)
	}
}

// HandleCallbackEditRaid records the chosen raid/week and asks for the number of
// columns.
func (h *Handler) HandleCallbackEditRaid(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb, chatID, ok := h.editCallbackGuard(ctx, b, update)
	if !ok {
		return
	}
	s, ok := h.editSessions.get(chatID)
	if !ok {
		h.answer(ctx, b, cb.ID, "Сессия истекла — отправьте /edit_tables")
		return
	}

	week, err := strconv.Atoi(strings.TrimPrefix(cb.Data, cbEditRaid))
	if err != nil {
		h.answer(ctx, b, cb.ID, "Неверный номер недели")
		return
	}

	raids, err := h.raidsForSession(ctx, s)
	if err != nil {
		h.logger.Error("re-fetch raids failed", "err", err)
		h.editSessions.clear(chatID)
		_ = h.adapter.SendMessage(ctx, chatID, "⚠️ Не удалось получить данные о рейде. Попробуйте позже.")
		return
	}

	raid := findRaidByWeek(raids, week)
	if raid == nil {
		h.answer(ctx, b, cb.ID, "Рейд не найден")
		return
	}

	s.WeekNumber = raid.WeekNumber
	s.RaidName = raid.RaidName
	s.TeamsCount = raid.TeamsCount
	s.RaidStatus = raid.StatusAt(h.now())
	s.RaidStart = raid.StartDate
	s.Step = stepColumns
	h.answer(ctx, b, cb.ID, "Ок")

	info := fmt.Sprintf(
		"📊 Рейд: <b>%s</b>\n📅 Неделя: %d\n👥 Команд: %d\n\nСколько аудиторий (колонок)?",
		escapeHTML(raid.RaidName), raid.WeekNumber, raid.TeamsCount,
	)
	// Manual mode may prepare a table before the raid starts (unlike
	// /create_tables), but the admin should know that's what they're doing —
	// the team count is not final yet.
	if s.RaidStatus == domain.RaidStatusUpcoming {
		info = fmt.Sprintf(
			"⏳ Рейд ещё не начался (старт %s) — идёт регистрация, число команд может измениться.\n\n",
			raid.StartDate.Format("02.01 15:04"),
		) + info
	}
	// ForceReply so the answer is delivered even in a group with privacy mode on.
	if err := h.askText(ctx, chatID, info); err != nil {
		h.logger.Error("send raid info failed", "err", err)
	}
}

// askText sends a free-text prompt with a ForceReply markup. In groups with
// privacy mode enabled the bot only receives replies to its own messages, so
// forcing the answer to be a reply is what keeps the dialog moving.
func (h *Handler) askText(ctx context.Context, chatID int64, text string) error {
	return h.adapter.SendMessageWithReplyMarkup(ctx, chatID, text, &models.ForceReply{
		ForceReply:            true,
		InputFieldPlaceholder: "Введите ответ…",
	})
}

// HandleEditText handles the free-text steps of the dialog (column count, time
// range, break time). It is only reached when the chat has a session awaiting
// text (see the match function in RegisterHandlers).
func (h *Handler) HandleEditText(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.From == nil {
		return
	}
	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID
	if !h.isAuthorized(chatID, userID) {
		return
	}

	s, ok := h.editSessions.get(chatID)
	if !ok {
		return
	}

	text := strings.TrimSpace(update.Message.Text)
	switch s.Step {
	case stepColumns:
		h.handleColumnsInput(ctx, chatID, s, text)
	case stepTimeRange:
		h.handleTimeRangeInput(ctx, chatID, s, text)
	case stepBreakTime:
		h.handleBreakTimeInput(ctx, chatID, s, text)
	}
}

func (h *Handler) handleColumnsInput(ctx context.Context, chatID int64, s *editTableSession, text string) {
	cols, err := strconv.Atoi(text)
	if err != nil || cols < 1 {
		_ = h.askText(ctx, chatID, "⚠️ Введите положительное число колонок (например: 3).")
		return
	}
	s.Columns = cols
	s.Step = stepSlotMinutes
	if err := h.adapter.SendMessageWithKeyboard(ctx, chatID,
		"Сколько минут длится защита одной команды?", slotMinutesKeyboard(),
	); err != nil {
		h.logger.Error("send slot-minutes picker failed", "err", err)
	}
}

// HandleCallbackEditSlot records the per-slot length and asks for the time
// window.
func (h *Handler) HandleCallbackEditSlot(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb, chatID, ok := h.editCallbackGuard(ctx, b, update)
	if !ok {
		return
	}
	s, ok := h.editSessions.get(chatID)
	if !ok {
		h.answer(ctx, b, cb.ID, "Сессия истекла — отправьте /edit_tables")
		return
	}

	mins, err := strconv.Atoi(strings.TrimPrefix(cb.Data, cbEditSlot))
	if err != nil || mins < 1 {
		h.answer(ctx, b, cb.ID, "Неверная длительность")
		return
	}
	s.SlotMinutes = mins
	s.Step = stepTimeRange
	h.answer(ctx, b, cb.ID, fmt.Sprintf("%d мин", mins))

	if err := h.askText(ctx, chatID, "С какого и до какого времени? (например: 11:00-17:00)"); err != nil {
		h.logger.Error("send time-range prompt failed", "err", err)
	}
}

func (h *Handler) handleTimeRangeInput(ctx context.Context, chatID int64, s *editTableSession, text string) {
	sh, sm, eh, em, ok := parseTimeRange(text)
	if !ok {
		_ = h.askText(ctx, chatID, "⚠️ Неверный формат. Введите диапазон как ЧЧ:ММ-ЧЧ:ММ (например: 11:00-17:00).")
		return
	}
	s.StartHour, s.StartMinute = sh, sm
	s.EndHour, s.EndMinute = eh, em
	s.Step = stepBreaks

	keyboard := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: "Перерывы: Да", CallbackData: cbEditBreaks + "yes"},
			{Text: "Перерывы: Нет", CallbackData: cbEditBreaks + "no"},
		}},
	}
	if err := h.adapter.SendMessageWithKeyboard(ctx, chatID, "Добавить перерывы?", keyboard); err != nil {
		h.logger.Error("send breaks picker failed", "err", err)
	}
}

// HandleCallbackEditBreaks branches the dialog: "no" finishes immediately, "yes"
// asks for the break time before finishing.
func (h *Handler) HandleCallbackEditBreaks(ctx context.Context, b *bot.Bot, update *models.Update) {
	cb, chatID, ok := h.editCallbackGuard(ctx, b, update)
	if !ok {
		return
	}
	s, ok := h.editSessions.get(chatID)
	if !ok {
		h.answer(ctx, b, cb.ID, "Сессия истекла — отправьте /edit_tables")
		return
	}

	if strings.TrimPrefix(cb.Data, cbEditBreaks) == "yes" {
		s.IncludeBreaks = true
		s.Step = stepBreakTime
		h.answer(ctx, b, cb.ID, "Перерывы: да")
		if err := h.askText(ctx, chatID, "Во сколько сделать перерыв? (например: 14:00)"); err != nil {
			h.logger.Error("send break-time prompt failed", "err", err)
		}
		return
	}

	s.IncludeBreaks = false
	h.answer(ctx, b, cb.ID, "Обновляю таблицу…")
	h.finishEditTable(ctx, chatID, s)
}

func (h *Handler) handleBreakTimeInput(ctx context.Context, chatID int64, s *editTableSession, text string) {
	bh, bm, ok := parseHM(text)
	if !ok {
		_ = h.askText(ctx, chatID, "⚠️ Неверный формат. Введите время как ЧЧ:ММ (например: 14:00).")
		return
	}
	// The break must fall inside the window.
	start := s.StartHour*60 + s.StartMinute
	end := s.EndHour*60 + s.EndMinute
	if bt := bh*60 + bm; bt <= start || bt >= end {
		_ = h.askText(ctx, chatID, fmt.Sprintf(
			"⚠️ Время перерыва должно быть внутри окна %02d:%02d-%02d:%02d. Введите ещё раз:",
			s.StartHour, s.StartMinute, s.EndHour, s.EndMinute))
		return
	}
	s.BreakHour, s.BreakMinute = bh, bm
	h.finishEditTable(ctx, chatID, s)
}

// finishEditTable computes the window-driven schedule, resolves the target
// spreadsheet, writes the table, and reports the result. It always writes when
// a sheet is configured; a capacity shortfall (fewer slots than teams) only adds
// a warning, per the admin's request that edge cases still produce a table.
func (h *Handler) finishEditTable(ctx context.Context, chatID int64, s *editTableSession) {
	if h.sheets == nil {
		h.editSessions.clear(chatID)
		_ = h.adapter.SendMessage(ctx, chatID, msgSheetsNotConfigured)
		return
	}

	schedule := usecase.CalculateDefenseScheduleWindow(usecase.WindowScheduleParams{
		StartHour:     s.StartHour,
		StartMinute:   s.StartMinute,
		EndHour:       s.EndHour,
		EndMinute:     s.EndMinute,
		SlotMinutes:   s.SlotMinutes,
		Columns:       s.Columns,
		IncludeBreaks: s.IncludeBreaks,
		BreakHour:     s.BreakHour,
		BreakMinute:   s.BreakMinute,
	})

	spreadsheetID, dedicated := h.resolveSheetForSession(s)
	if spreadsheetID == "" {
		h.editSessions.clear(chatID)
		msg := msgUniversalSheetNotConfigured
		if dedicated {
			msg = msgSheetNotConfigured
		}
		_ = h.adapter.SendMessage(ctx, chatID, msg)
		return
	}

	res, err := h.sheets.UpdateDefenseTable(ctx, spreadsheetID, sheets.DefenseTableParams{
		RaidName:    s.RaidName,
		DefenseDate: nextMonday(h.now()),
		Schedule:    schedule,
	})
	if err != nil {
		h.logger.Error("edit_tables update failed", "err", err)
		h.editSessions.clear(chatID)
		_ = h.adapter.SendMessage(ctx, chatID, "⚠️ Не удалось обновить таблицу. Попробуйте позже.")
		return
	}

	// The dialog is complete regardless of the warning.
	h.editSessions.clear(chatID)

	window := fmt.Sprintf("%02d:%02d-%02d:%02d", s.StartHour, s.StartMinute, s.EndHour, s.EndMinute)
	text := fmt.Sprintf("✅ Таблица обновлена: %s", res.URL)
	if res.FormatFailed {
		text += "\n" + msgFormattingFailed
	}
	if warn := h.sharedDocumentWarning(spreadsheetID); warn != "" {
		text += "\n" + warn
	}
	if s.RaidStatus == domain.RaidStatusUpcoming {
		text += fmt.Sprintf(
			"\n⏳ Рейд начнётся %s — таблица подготовлена заранее, число команд ещё может измениться.",
			s.RaidStart.Format("02.01 15:04"))
	}
	if warn := buildCapacityWarning(window, s.Columns, s.SlotMinutes, schedule.TotalSlots, s.TeamsCount); warn != "" {
		text += "\n" + warn
	}
	if err := h.adapter.SendMessage(ctx, chatID, text); err != nil {
		h.logger.Error("send edit_tables result failed", "err", err)
	}
}

// resolveSheetForSession picks the spreadsheet for a completed dialog. An
// "other" pool always maps to the universal table; a fixed piscine goes through
// the shared resolveSpreadsheetID rule.
func (h *Handler) resolveSheetForSession(s *editTableSession) (id string, dedicated bool) {
	if s.IsOther {
		return h.universalSheetID, false
	}
	return h.resolveSpreadsheetID(s.Piscine, s.WeekNumber)
}

// raidsForSession re-fetches the raid list appropriate to the session's target.
func (h *Handler) raidsForSession(ctx context.Context, s *editTableSession) ([]domain.RaidInfo, error) {
	if s.IsOther {
		return h.raidUC.ListRaidsForEvent(ctx, s.EventID)
	}
	return h.raidUC.ListRaidsWithWeeks(ctx, s.Piscine)
}

// editCallbackGuard performs the shared callback bookkeeping: it extracts the
// chat, and rejects unauthorized callers. It returns ok=false when the caller
// should stop (it has already answered / logged as needed).
func (h *Handler) editCallbackGuard(ctx context.Context, b *bot.Bot, update *models.Update) (*models.CallbackQuery, int64, bool) {
	cb := update.CallbackQuery
	chatID, ok := callbackChatID(cb)
	if !ok {
		if cb != nil {
			h.answer(ctx, b, cb.ID, "Ошибка: сообщение недоступно")
		}
		return nil, 0, false
	}
	if !h.isAuthorized(chatID, cb.From.ID) {
		h.logger.Warn("unauthorized edit_tables callback", "data", cb.Data, "chat_id", chatID, "user_id", cb.From.ID)
		h.answer(ctx, b, cb.ID, "Недостаточно прав")
		return nil, 0, false
	}
	return cb, chatID, true
}

// findRaidByWeek returns the raid with the given week number, or nil.
func findRaidByWeek(raids []domain.RaidInfo, week int) *domain.RaidInfo {
	for i := range raids {
		if raids[i].WeekNumber == week {
			r := raids[i]
			return &r
		}
	}
	return nil
}

// piscinePickerKeyboard builds the first-step keyboard: one button per fixed
// piscine plus a "Другой бассейн" entry that lists discovered pools.
func piscinePickerKeyboard() *models.InlineKeyboardMarkup {
	labels := []struct {
		text    string
		piscine domain.PiscineType
	}{
		{"Go", domain.PiscineGo},
		{"JS", domain.PiscineJS},
		{"AI1", domain.PiscineAI_1},
		{"AI2", domain.PiscineAI_2},
		{"AI3", domain.PiscineAI_3},
		{"RUST", domain.PiscineRUST},
	}

	var row []models.InlineKeyboardButton
	var rows [][]models.InlineKeyboardButton
	for i, l := range labels {
		row = append(row, models.InlineKeyboardButton{
			Text:         l.text,
			CallbackData: cbEditPiscine + string(l.piscine),
		})
		// Three buttons per row.
		if (i+1)%3 == 0 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []models.InlineKeyboardButton{{
		Text:         "Другой бассейн",
		CallbackData: cbEditPoolList,
	}})

	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// slotMinutesKeyboard offers the per-slot length choices as inline buttons.
func slotMinutesKeyboard() *models.InlineKeyboardMarkup {
	var row []models.InlineKeyboardButton
	for _, m := range slotMinuteChoices {
		row = append(row, models.InlineKeyboardButton{
			Text:         fmt.Sprintf("%d мин", m),
			CallbackData: fmt.Sprintf("%s%d", cbEditSlot, m),
		})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{row}}
}

// parseTimeRange parses a "HH:MM-HH:MM" window. It returns ok=false on any
// malformed part, an out-of-range value, or a non-increasing range.
func parseTimeRange(s string) (startH, startM, endH, endM int, ok bool) {
	parts := strings.Split(strings.TrimSpace(s), "-")
	if len(parts) != 2 {
		return 0, 0, 0, 0, false
	}
	sh, sm, ok1 := parseHM(parts[0])
	eh, em, ok2 := parseHM(parts[1])
	if !ok1 || !ok2 {
		return 0, 0, 0, 0, false
	}
	if eh*60+em <= sh*60+sm {
		return 0, 0, 0, 0, false
	}
	return sh, sm, eh, em, true
}

// parseHM parses a single "HH:MM" clock value.
func parseHM(s string) (h, m int, ok bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, false
	}
	m, err = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// buildCapacityWarning returns a warning when the window (at the chosen column
// count and slot length) yields fewer defense slots than there are teams. The
// table is still created — this only flags the shortfall. It returns "" when
// capacity is sufficient or the platform reports no teams (teams == 0).
func buildCapacityWarning(window string, columns, slotMinutes, capacity, teams int) string {
	if teams <= 0 || capacity >= teams {
		return ""
	}
	return fmt.Sprintf(
		"⚠️ В окне %s при %d колонках и слоте %d мин помещается %d мест, а команд %d — мест не хватает, но таблица создана.",
		window, columns, slotMinutes, capacity, teams,
	)
}
