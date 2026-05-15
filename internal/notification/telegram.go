package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/rs/zerolog"
)

type Priority string

const (
	PriorityCritical Priority = "P1"
	PriorityHigh     Priority = "P2"
	PriorityNormal   Priority = "P3"
	PriorityLow      Priority = "P4"
)

type Config struct {
	Enabled       bool
	BotToken      string
	DefaultChatID string
	HTTPClient    *http.Client
	Logger        zerolog.Logger
}

type TelegramNotifier struct {
	enabled       bool
	botToken      string
	defaultChatID string
	client        *http.Client
	logger        zerolog.Logger
}

func NewTelegramNotifier(cfg Config) *TelegramNotifier {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	return &TelegramNotifier{
		enabled:       cfg.Enabled,
		botToken:      cfg.BotToken,
		defaultChatID: cfg.DefaultChatID,
		client:        cfg.HTTPClient,
		logger:        cfg.Logger.With().Str("component", "notification.telegram").Logger(),
	}
}

func (n *TelegramNotifier) Enabled() bool {
	return n.enabled
}

func (n *TelegramNotifier) ConfigureWebAppMenu(ctx context.Context, webAppURL string, buttonText string) error {
	if !n.enabled {
		return nil
	}
	if n.botToken == "" {
		return errors.New("telegram bot token missing")
	}
	if webAppURL == "" {
		return errors.New("telegram web app url missing")
	}
	if buttonText == "" {
		buttonText = "Open Sol Whisperer"
	}

	payload := map[string]any{
		"menu_button": map[string]any{
			"type": "web_app",
			"text": buttonText,
			"web_app": map[string]any{
				"url": webAppURL,
			},
		},
	}
	if err := n.postBotAPI(ctx, "setChatMenuButton", payload); err != nil {
		return fmt.Errorf("configure telegram web app menu: %w", err)
	}
	return nil
}

func (n *TelegramNotifier) Send(ctx context.Context, chatID string, message string, p Priority) error {
	if !n.enabled {
		return nil
	}
	if n.botToken == "" {
		return errors.New("telegram bot token missing")
	}
	if chatID == "" {
		chatID = n.defaultChatID
	}
	if chatID == "" {
		return errors.New("telegram chat id missing")
	}

	payload := map[string]any{
		"chat_id":                  parseChatID(chatID),
		"text":                     decorateMessage(message, p),
		"parse_mode":               "Markdown",
		"disable_web_page_preview": true,
	}

	if err := n.postBotAPI(ctx, "sendMessage", payload); err != nil {
		return fmt.Errorf("send telegram message: %w", err)
	}
	return nil
}

func (n *TelegramNotifier) postBotAPI(ctx context.Context, method string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", n.botToken, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("build telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("call telegram api: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		return fmt.Errorf("telegram api returned status %d", res.StatusCode)
	}
	return nil
}

func parseChatID(chatID string) any {
	asInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return chatID
	}
	return asInt
}

func decorateMessage(msg string, p Priority) string {
	prefix := "[P3]"
	switch p {
	case PriorityCritical:
		prefix = "[P1 CRITICAL]"
	case PriorityHigh:
		prefix = "[P2 HIGH]"
	case PriorityNormal:
		prefix = "[P3]"
	case PriorityLow:
		prefix = "[P4]"
	}
	return prefix + " " + msg
}
