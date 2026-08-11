package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Adapter wraps the go-telegram/bot library.
type Adapter struct {
	bot    *bot.Bot
	token  string // kept so we can redact it from library errors
	logger *slog.Logger
}

func NewAdapter(token string, logger *slog.Logger) (*Adapter, error) {
	a := &Adapter{token: token, logger: logger}

	// The go-telegram/bot library issues requests to
	// https://api.telegram.org/bot<TOKEN>/... — the token is in the URL path.
	// On a transport failure net/http embeds the full URL (token included) in
	// *url.Error. The library's DEFAULT errors handler would print that during
	// long-polling, leaking the token. Install our own scrubbing handler.
	b, err := bot.New(token, bot.WithErrorsHandler(func(err error) {
		logger.Error("telegram polling error", "err", a.scrub(err))
	}))
	if err != nil {
		// bot.New validates the token (getMe), so even this error can carry it.
		return nil, fmt.Errorf("create telegram bot: %w", a.scrub(err))
	}
	a.bot = b
	return a, nil
}

// scrub redacts the bot token from an error before it is logged or returned.
// The token is URL-safe (digits, letters, ':', '_', '-'), so no percent-encoding
// occurs in the path and a raw substring match is sufficient.
func (a *Adapter) scrub(err error) error {
	if err == nil || a.token == "" {
		return err
	}
	if s := err.Error(); strings.Contains(s, a.token) {
		return errors.New(strings.ReplaceAll(s, a.token, "[REDACTED_BOT_TOKEN]"))
	}
	return err
}

// Bot returns the underlying *bot.Bot for handler registration.
func (a *Adapter) Bot() *bot.Bot { return a.bot }

// SendMessage sends a plain HTML-formatted message.
func (a *Adapter) SendMessage(ctx context.Context, chatID int64, text string) error {
	_, err := a.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    chatID,
		Text:      text,
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		err = a.scrub(err)
		a.logger.Error("telegram send failed", "chat_id", chatID, "err", err)
		return fmt.Errorf("send message: %w", err)
	}
	return nil
}

// SendMessageWithKeyboard sends a message with an inline keyboard.
func (a *Adapter) SendMessageWithKeyboard(ctx context.Context, chatID int64, text string, keyboard *models.InlineKeyboardMarkup) error {
	_, err := a.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: keyboard,
	})
	if err != nil {
		err = a.scrub(err)
		a.logger.Error("telegram send with keyboard failed", "chat_id", chatID, "err", err)
		return fmt.Errorf("send message with keyboard: %w", err)
	}
	return nil
}

// SendMessageWithReplyMarkup sends a message with an arbitrary reply markup
// (inline keyboard, reply keyboard, or ForceReply). It exists so the
// /edit_tables dialog can attach a ForceReply to its free-text prompts: in a
// group with privacy mode enabled a bot never receives ordinary text, but a
// reply to its own message is always delivered — ForceReply makes the user's
// answer such a reply.
func (a *Adapter) SendMessageWithReplyMarkup(ctx context.Context, chatID int64, text string, markup models.ReplyMarkup) error {
	_, err := a.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: markup,
	})
	if err != nil {
		err = a.scrub(err)
		a.logger.Error("telegram send with reply markup failed", "chat_id", chatID, "err", err)
		return fmt.Errorf("send message with reply markup: %w", err)
	}
	return nil
}

// EditMessageText replaces the text of an existing message and drops any inline
// keyboard it carried (ReplyMarkup omitted). Used to turn the approve/reject
// prompt into a settled "approved/rejected" line once the super-admin decides.
func (a *Adapter) EditMessageText(ctx context.Context, chatID int64, messageID int, text string) error {
	_, err := a.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      text,
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		err = a.scrub(err)
		a.logger.Error("telegram edit failed", "chat_id", chatID, "message_id", messageID, "err", err)
		return fmt.Errorf("edit message: %w", err)
	}
	return nil
}

// EditMessageWithKeyboard replaces both the text and the inline keyboard of an
// existing message. The /subscribe screen uses it so toggling a piscine updates
// the message in place instead of pushing a new one into the chat.
func (a *Adapter) EditMessageWithKeyboard(ctx context.Context, chatID int64, messageID int, text string, keyboard *models.InlineKeyboardMarkup) error {
	_, err := a.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        text,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: keyboard,
	})
	if err != nil {
		err = a.scrub(err)
		a.logger.Error("telegram edit with keyboard failed", "chat_id", chatID, "message_id", messageID, "err", err)
		return fmt.Errorf("edit message with keyboard: %w", err)
	}
	return nil
}

// Start begins long-polling for updates.
func (a *Adapter) Start(ctx context.Context) {
	a.bot.Start(ctx)
}
