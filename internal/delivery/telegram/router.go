package telegram

import (
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// RegisterHandlers registers all command and callback handlers.
func RegisterHandlers(b *bot.Bot, h *Handler) {
	// Commands
	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, h.HandleStart)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypeExact, h.HandleHelp)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/raidgo", bot.MatchTypeExact, h.HandleRaidGo)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/raidjs", bot.MatchTypeExact, h.HandleRaidJS)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/raidai1", bot.MatchTypeExact, h.HandleRaidAI1)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/raidai2", bot.MatchTypeExact, h.HandleRaidAI2)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/raidai3", bot.MatchTypeExact, h.HandleRaidAI3)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/raidrust", bot.MatchTypeExact, h.HandleRaidRust)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/week", bot.MatchTypeExact, h.HandleWeek)
	// /create_tables takes an optional piscine argument ("/create_tables go"), so
	// it matches by prefix. No other command starts with this string, so nothing
	// is shadowed.
	b.RegisterHandler(bot.HandlerTypeMessageText, "/create_tables", bot.MatchTypePrefix, h.HandleTables)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/edit_tables", bot.MatchTypeExact, h.HandleEditTables)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/cancel", bot.MatchTypeExact, h.HandleCancel)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/get_astana_updates", bot.MatchTypeExact, h.HandleAstanaUpdates)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/get_region_updates", bot.MatchTypeExact, h.HandleRegionUpdates)
	// /get_event carries an argument (the event ID), so it must match by prefix.
	// Registered after the exact commands above; findHandler returns the first
	// match, and "/get_event" is not a prefix of any exact command, so none are
	// shadowed.
	b.RegisterHandler(bot.HandlerTypeMessageText, "/get_event", bot.MatchTypePrefix, h.HandleGetEvent)

	// Callback queries (inline keyboard buttons).
	// Using MatchTypePrefix because callback data includes piscine type:
	// "defense_create:Piscine Go", "defense_edit:Piscine JS", etc.
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "defense_create:", bot.MatchTypePrefix, h.HandleCallbackCreateTable)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "defense_edit:", bot.MatchTypePrefix, h.HandleCallbackEditParams)

	// /edit_tables dialog callbacks. "edit_pool_list" is registered before the
	// "edit_pool:" prefix so the exact list button is never shadowed (they don't
	// overlap — no trailing colon — but keep the ordering explicit).
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, cbEditPiscine, bot.MatchTypePrefix, h.HandleCallbackEditPiscine)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, cbEditPoolList, bot.MatchTypeExact, h.HandleCallbackEditPoolList)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, cbEditPool, bot.MatchTypePrefix, h.HandleCallbackEditPool)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, cbEditRaid, bot.MatchTypePrefix, h.HandleCallbackEditRaid)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, cbEditSlot, bot.MatchTypePrefix, h.HandleCallbackEditSlot)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, cbEditBreaks, bot.MatchTypePrefix, h.HandleCallbackEditBreaks)

	// Access-request decisions (super-admin only; enforced in the handlers).
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "access_approve:", bot.MatchTypePrefix, h.HandleCallbackAccessApprove)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "access_reject:", bot.MatchTypePrefix, h.HandleCallbackAccessReject)

	// Free-text catch-all for the /edit_tables dialog's two text steps (column
	// count, time range). The match function fires ONLY when the chat has a
	// session actively awaiting text and the message is not a command, so
	// ordinary chatter and other commands fall through untouched. Registered
	// last: findHandler returns the first matching handler, so exact commands
	// and callback handlers above always win.
	b.RegisterHandlerMatchFunc(func(update *models.Update) bool {
		if update.Message == nil {
			return false
		}
		text := update.Message.Text
		if text == "" || strings.HasPrefix(text, "/") {
			return false
		}
		return h.editSessions.awaitingText(update.Message.Chat.ID)
	}, h.HandleEditText)
}
