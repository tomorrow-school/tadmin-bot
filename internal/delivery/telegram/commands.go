package telegram

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
		"/create_tables {бассейн} — таблица защит одного бассейна (go, js, ai1, ai2, ai3, rust)\n" +
		"/create_tables — таблицы защит всех бассейнов с рейдом (3 колонки × 30 мин, до 17:30)\n" +
		"/edit_tables — создать/обновить таблицу защиты с ручными параметрами\n" +
		"/announce — получить текст анонса: выбрать бассейн (у Go — ещё и тип анонса)\n" +
		"/subscribe — включить/отключить анонсы и выбрать бассейны\n" +
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
		switch {
		case weekInfo.ActiveRaid != nil && weekInfo.ActiveRaid.RaidName != "":
			raidName = weekInfo.ActiveRaid.RaidName
			// Mark a raid that is still in registration, so "Рейд: X" is not read
			// as "X is running now".
			if weekInfo.RaidStatus == domain.RaidStatusUpcoming {
				raidName += " (скоро старт)"
			}
		case weekInfo.RecentRaid != nil && weekInfo.RecentRaid.RaidName != "":
			// Nothing is running, but the last raid is still being defended —
			// that is what the admins are working on right now.
			raidName = weekInfo.RecentRaid.RaidName + " (завершён, идёт защита)"
		}

		fmt.Fprintf(&sb, "📌 <b>%s</b> (id %d): Неделя %d | Рейд: %s\n",
			escapeHTML(label), p.ID, weekInfo.WeekNumber, escapeHTML(raidName))
	}

	if err := h.adapter.SendMessage(ctx, chatID, sb.String()); err != nil {
		h.logger.Error("send week info failed", "err", err)
	}
}

// HandleTables handles "/create_tables [бассейн]".
//
// The layout is entirely automatic: 3 columns of 30-minute slots, no breaks,
// ending at 17:30, with as many rows as the team count needs (20 teams → 7
// rows). Only the raid has to be resolved — the running one, or the one that
// just ended, since defenses happen after a raid finishes.
//
// With no argument it still covers every piscine with such a raid, but it no
// longer writes blindly: targets are resolved first and any two that would land
// in the SAME spreadsheet are refused, because the second write would wipe the
// first one's defense table. With an argument ("/create_tables go") exactly one
// piscine is updated, which is the safe way to work when several pools share a
// document.
func (h *Handler) HandleTables(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID, ok := h.guard(ctx, update)
	if !ok {
		return
	}

	if h.sheets == nil {
		_ = h.adapter.SendMessage(ctx, chatID, msgSheetsNotConfigured)
		return
	}

	piscines, argOK := parseTablesArg(update.Message.Text)
	if !argOK {
		_ = h.adapter.SendMessage(ctx, chatID, tablesUsage())
		return
	}

	targets, lines, skips := h.planTableUpdates(ctx, piscines)
	targets, conflicts := rejectConflictingTargets(targets)
	lines = append(lines, conflicts...)
	lines = append(lines, skipLines(skips, len(piscines) == 1)...)

	updatedCount := 0
	for _, t := range targets {
		res, err := h.updateTableForActiveRaid(ctx, t.spreadsheetID, t.raid, t.defenseDate)
		if err != nil {
			h.logger.Error("update defense table failed", "piscine", t.piscine, "raid", t.raid.RaidName, "err", err)
			lines = append(lines, fmt.Sprintf("❌ Ошибка при обновлении таблицы (%s)", t.piscine))
			continue
		}

		updatedCount++
		line := fmt.Sprintf("✅ Таблица обновлена (%s — %s, защита %s): %s",
			escapeHTML(string(t.piscine)), escapeHTML(t.raid.RaidName),
			t.defenseDate.Format("02.01"), res.URL)
		if t.ended {
			// The raid is over, so students may already have signed up in this
			// document — and the update rewrites it from scratch.
			line += "\n⚠️ Рейд уже завершён: таблица перестроена заново, прежние записи в ней очищены."
		}
		if res.FormatFailed {
			line += "\n" + msgFormattingFailed
		}
		if warn := h.sharedDocumentWarning(t.spreadsheetID); warn != "" {
			line += "\n" + warn
		}
		lines = append(lines, line)
	}

	resp := "ℹ️ Сейчас нет рейдов, для которых нужна таблица защит."
	if len(lines) > 0 {
		resp = strings.Join(lines, "\n")
	}

	if err := h.adapter.SendMessage(ctx, chatID, resp); err != nil {
		h.logger.Error("send create_tables result failed", "err", err)
	}

	h.logger.Info("create_tables finished", "requested", len(piscines), "updated", updatedCount, "total_lines", len(lines))
}

// tableTarget is one resolved "(piscine, week) → spreadsheet" mapping that
// /create_tables is about to write.
type tableTarget struct {
	piscine       domain.PiscineType
	week          int
	raid          *domain.RaidInfo
	spreadsheetID string
	defenseDate   time.Time
	ended         bool // the raid has finished; the table is being rebuilt after the fact
}

// skipNote records a piscine /create_tables did not write, and why. Notes are
// summarized rather than printed one per line when the admin asked for every
// piscine at once — six "nothing to do" lines would bury the real results.
type skipNote struct {
	piscine domain.PiscineType
	reason  string
}

// skipLines renders the skip notes. verbose (a single-piscine request) spells
// each one out; otherwise they collapse into one summary line.
func skipLines(skips []skipNote, verbose bool) []string {
	if len(skips) == 0 {
		return nil
	}
	if verbose {
		out := make([]string, 0, len(skips))
		for _, s := range skips {
			out = append(out, fmt.Sprintf("ℹ️ %s — %s", escapeHTML(string(s.piscine)), s.reason))
		}
		return out
	}
	parts := make([]string, 0, len(skips))
	for _, s := range skips {
		parts = append(parts, fmt.Sprintf("%s (%s)", escapeHTML(piscineArgFor(s.piscine)), s.reason))
	}
	return []string{"ℹ️ Пропущены: " + strings.Join(parts, ", ")}
}

// planTableUpdates resolves each requested piscine to a write target, returning
// the writable targets plus the reply lines explaining every piscine that was
// skipped (no raid, registration still open, no sheet configured, upstream
// error). Nothing is written here — resolving first is what makes the
// same-document check below possible.
func (h *Handler) planTableUpdates(ctx context.Context, piscines []domain.PiscineType) ([]tableTarget, []string, []skipNote) {
	var targets []tableTarget
	var lines []string
	var skips []skipNote

	for _, piscine := range piscines {
		weekInfo, err := h.raidUC.DetectCurrentWeek(ctx, piscine)
		if err != nil {
			// A piscine that simply is not running is a normal state, not a
			// failure — reporting both as "❌ Ошибка при обновлении таблицы" is
			// what made this command impossible to diagnose.
			if errors.Is(err, domain.ErrNoActivePiscine) {
				h.logger.Info("skip: piscine not running", "piscine", piscine)
				skips = append(skips, skipNote{piscine, "бассейн сейчас не идёт"})
				continue
			}
			h.logger.Warn("detect week failed", "piscine", piscine, "err", err)
			lines = append(lines, fmt.Sprintf("❌ %s — не удалось получить данные о рейде.", escapeHTML(string(piscine))))
			continue
		}

		// The raid to schedule defenses for: the running one, or the one that
		// just ended — defenses happen after a raid finishes, so the table is
		// still wanted for the rest of that week.
		raid, status := weekInfo.DefenseRaid()
		if raid == nil {
			// Registration window: ActiveRaid holds the NEXT raid, which has no
			// teams yet, so there is nothing to schedule. Say so instead of
			// silently writing a table for a raid that hasn't started.
			if weekInfo.ActiveRaid != nil && weekInfo.RaidStatus == domain.RaidStatusUpcoming {
				h.logger.Info("skip: raid not started", "piscine", piscine,
					"raid", weekInfo.ActiveRaid.RaidName, "status", weekInfo.RaidStatus)
				lines = append(lines, notStartedLine(piscine, weekInfo.ActiveRaid))
				continue
			}
			h.logger.Info("skip: no raid to defend", "piscine", piscine, "week", weekInfo.WeekNumber)
			skips = append(skips, skipNote{piscine, "нет рейда для защиты"})
			continue
		}

		// A raid with no teams would produce a header and nothing else — and the
		// write wipes whatever the document held. Refuse instead.
		if raid.TeamsCount <= 0 {
			h.logger.Info("skip: raid has no teams", "piscine", piscine, "raid", raid.RaidName)
			lines = append(lines, fmt.Sprintf(
				"⚠️ %s — у рейда «%s» пока нет команд, таблицу защит не из чего строить. Пустую сетку можно сделать через /edit_tables.",
				escapeHTML(string(piscine)), escapeHTML(raid.RaidName)))
			continue
		}

		// A finished raid belongs to its own week, not to the (later) week the
		// piscine has moved on to — its defense table lives in that week's sheet.
		week := weekInfo.WeekNumber
		if status == domain.RaidStatusEnded && raid.WeekNumber > 0 {
			week = raid.WeekNumber
		}

		spreadsheetID, dedicated := h.resolveSpreadsheetID(piscine, week)
		if spreadsheetID == "" {
			h.logger.Warn("no sheet configured", "piscine", piscine, "week", week, "dedicated", dedicated)
			if dedicated {
				lines = append(lines, fmt.Sprintf("⚠️ %s — таблица для недели %d не настроена", piscine, week))
			} else {
				lines = append(lines, fmt.Sprintf("⚠️ %s — универсальная таблица (SHEET_UNIVERSAL) не настроена", piscine))
			}
			continue
		}

		targets = append(targets, tableTarget{
			piscine:       piscine,
			week:          week,
			raid:          raid,
			spreadsheetID: spreadsheetID,
			defenseDate:   defenseDateFor(raid, h.now()),
			ended:         status == domain.RaidStatusEnded,
		})
	}

	return targets, lines, skips
}

// rejectConflictingTargets drops every target that shares a spreadsheet with
// another target in the same run and explains what happened. Writing both would
// mean the second one clearing (A1:Z1000) and rewriting the first one's rows —
// silent data loss. Refusing and naming the per-piscine commands lets the admin
// update them one at a time (into different documents, or knowingly in turn).
func rejectConflictingTargets(targets []tableTarget) (writable []tableTarget, lines []string) {
	byDoc := make(map[string][]tableTarget, len(targets))
	for _, t := range targets {
		byDoc[t.spreadsheetID] = append(byDoc[t.spreadsheetID], t)
	}

	reported := make(map[string]bool, len(byDoc))
	for _, t := range targets {
		group := byDoc[t.spreadsheetID]
		if len(group) == 1 {
			writable = append(writable, t)
			continue
		}
		if reported[t.spreadsheetID] {
			continue
		}
		reported[t.spreadsheetID] = true

		names := make([]string, 0, len(group))
		commands := make([]string, 0, len(group))
		for _, g := range group {
			names = append(names, escapeHTML(string(g.piscine)))
			commands = append(commands, "/create_tables "+piscineArgFor(g.piscine))
		}
		lines = append(lines, fmt.Sprintf(
			"⛔️ %s настроены на одну и ту же Google-таблицу — обновление затёрло бы данные друг друга, поэтому ничего не записано.\n"+
				"Обновите по одному: %s\n"+
				"Либо задайте им разные SHEET_* таблицы в .env.",
			strings.Join(names, " и "), strings.Join(commands, ", "),
		))
	}
	return writable, lines
}

// sharedDocumentWarning flags a write into a document that more than one SHEET_*
// slot points at (e.g. SHEET_GO_WEEK1 and SHEET_GO_WEEK2 holding the same URL):
// the table just written replaced whatever the other slot had there.
func (h *Handler) sharedDocumentWarning(spreadsheetID string) string {
	slots := h.sheetSlots[spreadsheetID]
	if len(slots) < 2 {
		return ""
	}
	return fmt.Sprintf(
		"⚠️ Этот документ указан сразу в %s — предыдущая таблица защит в нём перезаписана. "+
			"Чтобы данные не терялись, задайте разные ссылки в .env.",
		escapeHTML(strings.Join(slots, ", ")),
	)
}

// piscineArgs maps the /create_tables argument to a piscine. Short aliases keep
// the command quick to type; "all" restores the update-everything behavior.
var piscineArgs = map[string]domain.PiscineType{
	"go":   domain.PiscineGo,
	"js":   domain.PiscineJS,
	"ai1":  domain.PiscineAI_1,
	"ai2":  domain.PiscineAI_2,
	"ai3":  domain.PiscineAI_3,
	"rust": domain.PiscineRUST,
}

// piscineArgFor is the reverse lookup, so error messages can name the exact
// command to run. Falls back to the piscine string for an unmapped type.
func piscineArgFor(p domain.PiscineType) string {
	for arg, piscine := range piscineArgs {
		if piscine == p {
			return arg
		}
	}
	return string(p)
}

// parseTablesArg reads the optional piscine argument of "/create_tables [arg]".
// No argument (or "all") means every piscine, as before. ok=false signals an
// unrecognized argument, so the caller can show the usage line instead of
// silently updating everything — which is exactly the accident this command
// used to make easy.
func parseTablesArg(text string) (piscines []domain.PiscineType, ok bool) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return domain.AllPiscines(), true
	}

	arg := strings.ToLower(strings.TrimPrefix(fields[1], "/"))
	if arg == "all" || arg == "все" {
		return domain.AllPiscines(), true
	}
	if piscine, found := piscineArgs[arg]; found {
		return []domain.PiscineType{piscine}, true
	}
	return nil, false
}

// tablesUsage lists the accepted arguments, sorted so the message is stable.
func tablesUsage() string {
	args := make([]string, 0, len(piscineArgs))
	for arg := range piscineArgs {
		args = append(args, arg)
	}
	sort.Strings(args)
	return "ℹ️ Использование: <code>/create_tables {бассейн}</code>\n" +
		"Бассейны: " + strings.Join(args, ", ") + "\n" +
		"<code>/create_tables</code> без аргумента (или <code>all</code>) — все бассейны с идущим рейдом."
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
		if errors.Is(err, domain.ErrNoActivePiscine) {
			h.logger.Info("raid info: piscine not running", "piscine", piscine)
			_ = h.adapter.SendMessage(ctx, chatID, fmt.Sprintf(
				"ℹ️ <b>%s</b> сейчас не идёт — на платформе нет активного бассейна.",
				escapeHTML(string(piscine))))
			return
		}
		h.logger.Error("detect week failed", "piscine", piscine, "err", err)
		_ = h.adapter.SendMessage(ctx, chatID, "⚠️ Не удалось определить текущую неделю. Попробуйте позже.")
		return
	}

	if err := h.adapter.SendMessage(ctx, chatID, raidInfoText(piscine, weekInfo, h.loc)); err != nil {
		h.logger.Error("send raid info failed", "err", err)
	}
}

// notStartedLine explains why a piscine was skipped during its registration
// window. The raid start date is included so the admin knows when to retry.
func notStartedLine(piscine domain.PiscineType, raid *domain.RaidInfo) string {
	return fmt.Sprintf(
		"⏳ %s — рейд «%s» ещё не начался (старт %s), таблицу защит создавать рано.",
		escapeHTML(string(piscine)), escapeHTML(raid.RaidName), raid.StartDate.Format("02.01 15:04"),
	)
}

func (h *Handler) updateTableForActiveRaid(ctx context.Context, spreadsheetID string, raid *domain.RaidInfo, defenseDate time.Time) (sheets.UpdateResult, error) {
	schedule := usecase.CalculateDefenseSchedule(usecase.AutoScheduleParams(raid.TeamsCount))
	return h.sheets.UpdateDefenseTable(ctx, spreadsheetID, sheets.DefenseTableParams{
		RaidName:    raid.RaidName,
		DefenseDate: defenseDate,
		Schedule:    schedule,
	})
}

// msgFormattingFailed is appended when the rows were written but the styling
// pass failed, so a visibly ugly table has an explanation in the chat.
const msgFormattingFailed = "⚠️ Данные записаны, но оформление применить не удалось — попробуйте обновить таблицу ещё раз."
