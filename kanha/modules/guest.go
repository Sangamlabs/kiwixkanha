/*
 * ● KanhaMusic
 * ○ A high-performance engine for streaming music in Telegram voicechats.
 *
 * Copyright (C) 2026 Kanha
 *
 * This program is free software: you can redistribute it and/or modify it under the
 * terms of the GNU General Public License as published by the Free Software Foundation,
 * either version 3 of the License, or (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT ANY
 * WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR A
 * PARTICULAR PURPOSE. See the GNU General Public License for more details.
 *
 * Repository: https://github.com/Oyekanhaa/KanhaMusic
 */

package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"KanhaMusic/config"
	"KanhaMusic/kanha/logger"
)

var (
	guestLog         = logger.GetLogger("Guest")
	customEmojiRegex = regexp.MustCompile(`(?s)<tg-emoji[^>]*>(.*?)</tg-emoji>`)
	guestRunning     atomic.Bool
	guestCancel      context.CancelFunc
	guestMu          sync.Mutex
)

// BotAPIError represents an error returned by the Telegram Bot API.
type BotAPIError struct {
	StatusCode  int
	ErrorCode   int
	Description string
	RetryAfter  int
}

func (e *BotAPIError) Error() string {
	if e.ErrorCode != 0 {
		return fmt.Sprintf("telegram bot api error %d: %s", e.ErrorCode, e.Description)
	}
	return fmt.Sprintf("telegram http error %d: %s", e.StatusCode, e.Description)
}

type botAPIResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	ErrorCode   int    `json:"error_code,omitempty"`
	Description string `json:"description,omitempty"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after,omitempty"`
	} `json:"parameters,omitempty"`
}

type botAPIUser struct {
	ID                   int64  `json:"id"`
	IsBot                bool   `json:"is_bot"`
	FirstName            string `json:"first_name"`
	Username             string `json:"username"`
	SupportsGuestQueries bool   `json:"supports_guest_queries"`
}

type botAPIWebhookInfo struct {
	URL string `json:"url"`
}

type botAPIGuestMessage struct {
	GuestQueryID string `json:"guest_query_id"`
	ID           string `json:"id,omitempty"`
	ChatType     string `json:"chat_type,omitempty"`
	Text         string `json:"text,omitempty"`
}

type botAPIUpdate struct {
	UpdateID     int64               `json:"update_id"`
	GuestMessage *botAPIGuestMessage `json:"guest_message,omitempty"`
	GuestQueryID string              `json:"guest_query_id,omitempty"`
}

type InlineKeyboardButton struct {
	Text              string `json:"text"`
	URL               string `json:"url,omitempty"`
	Style             string `json:"style,omitempty"`
	IconCustomEmojiID string `json:"icon_custom_emoji_id,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type InputTextMessageContent struct {
	MessageText string `json:"message_text"`
	ParseMode   string `json:"parse_mode,omitempty"`
}

type GuestQueryResultArticle struct {
	Type                string                  `json:"type"`
	ID                  string                  `json:"id"`
	Title               string                  `json:"title"`
	InputMessageContent InputTextMessageContent `json:"input_message_content"`
	ReplyMarkup         *InlineKeyboardMarkup   `json:"reply_markup,omitempty"`
}

type answerGuestQueryParams struct {
	GuestQueryID string                  `json:"guest_query_id"`
	Result       GuestQueryResultArticle `json:"result"`
}

type getUpdatesParams struct {
	Offset         int64    `json:"offset,omitempty"`
	Timeout        int      `json:"timeout"`
	AllowedUpdates []string `json:"allowed_updates"`
}

// GuestEmojis stores custom emoji IDs used for promotional cards and buttons.
type GuestEmojis struct {
	MusicNotes string
	Bolt       string
	Rocket     string
	Globe      string
	Help       string
	Chat       string
	Headphones string
}

func loadGuestEmojis() GuestEmojis {
	return GuestEmojis{
		MusicNotes: getEnvFirst("EMOJI_MUSIC_NOTES", "EMOJI_MUSIC_NOTE", "GUEST_EMOJI_MUSIC_NOTES", "GUEST_EMOJI_MUSIC_NOTE"),
		Bolt:       getEnvFirst("EMOJI_BOLT", "GUEST_EMOJI_BOLT"),
		Rocket:     getEnvFirst("EMOJI_ROCKET", "GUEST_EMOJI_ROCKET"),
		Globe:      getEnvFirst("EMOJI_GLOBE", "GUEST_EMOJI_GLOBE"),
		Help:       getEnvFirst("EMOJI_HELP", "GUEST_EMOJI_HELP"),
		Chat:       getEnvFirst("EMOJI_CHAT", "GUEST_EMOJI_CHAT"),
		Headphones: getEnvFirst("EMOJI_HEADPHONES", "GUEST_EMOJI_HEADPHONES"),
	}
}

func getEnvFirst(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func formatEmoji(eid, plain string, fancy bool) string {
	if fancy && eid != "" {
		return fmt.Sprintf("<tg-emoji emoji-id=\"%s\">%s</tg-emoji>", eid, plain)
	}
	return plain
}

func stripCustomEmoji(text string) string {
	return customEmojiRegex.ReplaceAllString(text, "$1")
}

func formatSupportURL(group string) string {
	g := strings.TrimSpace(group)
	if g == "" {
		return ""
	}
	if !strings.HasPrefix(g, "http://") && !strings.HasPrefix(g, "https://") {
		return "https://t.me/" + strings.TrimPrefix(g, "@")
	}
	return g
}

func buildGuestCardText(name string, emojis GuestEmojis, fancy bool) string {
	n := html.EscapeString(name)

	musicNote := formatEmoji(emojis.MusicNotes, "🎶", fancy)
	bolt := formatEmoji(emojis.Bolt, "⚡", fancy)
	rocket := formatEmoji(emojis.Rocket, "🚀", fancy)
	globe := formatEmoji(emojis.Globe, "🌐", fancy)

	text := fmt.Sprintf(
		"<b>%s</b> — ʏᴏᴜʀ ᴅᴇᴅɪᴄᴀᴛᴇᴅ ᴍᴜsɪᴄ ʙᴏᴛ.\n\n"+
			"%s ᴄʀʏsᴛᴀʟ-ᴄʟᴇᴀʀ ᴠᴏɪᴄᴇ ᴄʜᴀᴛ sᴛʀᴇᴀᴍɪɴɢ\n"+
			"%s ʙʟᴀᴢɪɴɢ-ꜰᴀsᴛ ᴘʟᴀʏʙᴀᴄᴋ ᴡɪᴛʜ ǫᴜᴇᴜᴇ\n"+
			"%s sᴍᴀʀᴛ ᴀᴜᴛᴏᴘʟᴀʏ &amp; sᴜɢɢᴇsᴛɪᴏɴs (/autoplay)\n"+
			"%s ʏᴏᴜᴛᴜʙᴇ, sᴘᴏᴛɪꜰʏ &amp; ᴍᴏʀᴇ\n\n"+
			"ᴜsᴇ /play [song] ᴛᴏ ɢᴇᴛ sᴛᴀʀᴛᴇᴅ!",
		n, musicNote, bolt, rocket, globe,
	)

	if !fancy {
		text = stripCustomEmoji(text)
	}
	return text
}

func buildGuestKeyboard(username string, supportChat string, ownerID int64, emojis GuestEmojis, fancy bool) *InlineKeyboardMarkup {
	var rows [][]InlineKeyboardButton

	// Row 1: Add me to your group
	btnRow1 := InlineKeyboardButton{
		Text: "ᴀᴅᴅ ᴍᴇ ᴛᴏ ʏᴏᴜʀ ɢʀᴏᴜᴘ",
		URL:  fmt.Sprintf("https://t.me/%s?startgroup=true", username),
	}
	if fancy {
		btnRow1.Style = "primary"
		if emojis.Rocket != "" {
			btnRow1.IconCustomEmojiID = emojis.Rocket
		}
	}
	rows = append(rows, []InlineKeyboardButton{btnRow1})

	// Row 2: Commands & Open Bot
	btnCommands := InlineKeyboardButton{
		Text: "ᴄᴏᴍᴍᴀɴᴅs",
		URL:  fmt.Sprintf("https://t.me/%s?start=help", username),
	}
	btnOpen := InlineKeyboardButton{
		Text: "ᴏᴘᴇɴ ʙᴏᴛ",
		URL:  fmt.Sprintf("https://t.me/%s", username),
	}
	if fancy {
		if emojis.Help != "" {
			btnCommands.IconCustomEmojiID = emojis.Help
		}
		if emojis.MusicNotes != "" {
			btnOpen.IconCustomEmojiID = emojis.MusicNotes
		}
	}
	rows = append(rows, []InlineKeyboardButton{btnCommands, btnOpen})

	// Row 3: Support & Owner
	var row3 []InlineKeyboardButton
	supportURL := formatSupportURL(supportChat)
	if supportURL != "" {
		btnSupport := InlineKeyboardButton{
			Text: "sᴜᴘᴘᴏʀᴛ",
			URL:  supportURL,
		}
		if fancy && emojis.Chat != "" {
			btnSupport.IconCustomEmojiID = emojis.Chat
		}
		row3 = append(row3, btnSupport)
	}

	if ownerID != 0 {
		btnOwner := InlineKeyboardButton{
			Text: "ᴏᴡɴᴇʀ",
			URL:  fmt.Sprintf("tg://user?id=%d", ownerID),
		}
		if fancy && emojis.Headphones != "" {
			btnOwner.IconCustomEmojiID = emojis.Headphones
		}
		row3 = append(row3, btnOwner)
	}

	if len(row3) > 0 {
		rows = append(rows, row3)
	}

	return &InlineKeyboardMarkup{
		InlineKeyboard: rows,
	}
}

func buildGuestResult(username, name, supportChat string, ownerID int64, emojis GuestEmojis, fancy bool) GuestQueryResultArticle {
	text := buildGuestCardText(name, emojis, fancy)
	return GuestQueryResultArticle{
		Type:  "article",
		ID:    "1",
		Title: name,
		InputMessageContent: InputTextMessageContent{
			MessageText: text,
			ParseMode:   "HTML",
		},
		ReplyMarkup: buildGuestKeyboard(username, supportChat, ownerID, emojis, fancy),
	}
}

// BotAPIClient defines operations used to interact with Telegram Bot API HTTPS endpoints.
type BotAPIClient interface {
	GetMe(ctx context.Context) (*botAPIUser, error)
	GetWebhookInfo(ctx context.Context) (*botAPIWebhookInfo, error)
	GetUpdates(ctx context.Context, offset int64, timeout int, allowed []string) ([]botAPIUpdate, error)
	AnswerGuestQuery(ctx context.Context, qid string, result GuestQueryResultArticle) error
}

type defaultBotAPIClient struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

func newDefaultBotAPIClient(token, baseURL string) *defaultBotAPIClient {
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	return &defaultBotAPIClient{
		token:   token,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 45 * time.Second,
		},
	}
}

func (c *defaultBotAPIClient) doRequest(ctx context.Context, method string, reqBody any, dest any) error {
	url := fmt.Sprintf("%s/bot%s/%s", c.baseURL, c.token, method)

	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request failed: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bodyReader)
	if err != nil {
		return sanitizeError(err, c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return sanitizeError(err, c.token)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return sanitizeError(err, c.token)
	}

	if err := json.Unmarshal(respData, dest); err != nil {
		return fmt.Errorf("unmarshal response failed: %w", err)
	}

	return nil
}

func (c *defaultBotAPIClient) GetMe(ctx context.Context) (*botAPIUser, error) {
	var resp botAPIResponse[botAPIUser]
	if err := c.doRequest(ctx, "getMe", nil, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		retryAfter := 0
		if resp.Parameters != nil {
			retryAfter = resp.Parameters.RetryAfter
		}
		return nil, &BotAPIError{ErrorCode: resp.ErrorCode, Description: resp.Description, RetryAfter: retryAfter}
	}
	return &resp.Result, nil
}

func (c *defaultBotAPIClient) GetWebhookInfo(ctx context.Context) (*botAPIWebhookInfo, error) {
	var resp botAPIResponse[botAPIWebhookInfo]
	if err := c.doRequest(ctx, "getWebhookInfo", nil, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		retryAfter := 0
		if resp.Parameters != nil {
			retryAfter = resp.Parameters.RetryAfter
		}
		return nil, &BotAPIError{ErrorCode: resp.ErrorCode, Description: resp.Description, RetryAfter: retryAfter}
	}
	return &resp.Result, nil
}

func (c *defaultBotAPIClient) GetUpdates(ctx context.Context, offset int64, timeout int, allowed []string) ([]botAPIUpdate, error) {
	params := getUpdatesParams{
		Offset:         offset,
		Timeout:        timeout,
		AllowedUpdates: allowed,
	}
	var resp botAPIResponse[[]botAPIUpdate]
	if err := c.doRequest(ctx, "getUpdates", params, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		retryAfter := 0
		if resp.Parameters != nil {
			retryAfter = resp.Parameters.RetryAfter
		}
		return nil, &BotAPIError{ErrorCode: resp.ErrorCode, Description: resp.Description, RetryAfter: retryAfter}
	}
	return resp.Result, nil
}

func (c *defaultBotAPIClient) AnswerGuestQuery(ctx context.Context, qid string, result GuestQueryResultArticle) error {
	params := answerGuestQueryParams{
		GuestQueryID: qid,
		Result:       result,
	}
	var resp botAPIResponse[bool]
	if err := c.doRequest(ctx, "answerGuestQuery", params, &resp); err != nil {
		return err
	}
	if !resp.OK {
		retryAfter := 0
		if resp.Parameters != nil {
			retryAfter = resp.Parameters.RetryAfter
		}
		return &BotAPIError{ErrorCode: resp.ErrorCode, Description: resp.Description, RetryAfter: retryAfter}
	}
	return nil
}

// GuestService coordinates the background polling and answering of guest queries.
type GuestService struct {
	Client      BotAPIClient
	SupportChat string
	OwnerID     int64
	Emojis      GuestEmojis
}

// NewGuestService constructs a GuestService with the default Bot API client.
func NewGuestService(token, supportChat string, ownerID int64) *GuestService {
	return &GuestService{
		Client:      newDefaultBotAPIClient(token, ""),
		SupportChat: supportChat,
		OwnerID:     ownerID,
		Emojis:      loadGuestEmojis(),
	}
}

// Answer attempts to answer a guest query using fancy styling first, retrying plain if rejected.
func (s *GuestService) Answer(ctx context.Context, qid, username, name string) error {
	fancyResult := buildGuestResult(username, name, s.SupportChat, s.OwnerID, s.Emojis, true)
	err := s.Client.AnswerGuestQuery(ctx, qid, fancyResult)
	if err == nil {
		guestLog.Info("answered a guest query")
		return nil
	}

	guestLog.Warnf("premium card refused (%v), retrying plain", err)
	plainResult := buildGuestResult(username, name, s.SupportChat, s.OwnerID, s.Emojis, false)
	err = s.Client.AnswerGuestQuery(ctx, qid, plainResult)
	if err == nil {
		guestLog.Info("answered a guest query (plain)")
		return nil
	}

	guestLog.Errorf("answerGuestQuery failed: %v", err)
	return err
}

// Run executes the Guest Mode polling loop until ctx is cancelled.
func (s *GuestService) Run(ctx context.Context) error {
	if s.Client == nil {
		return errors.New("bot api client is nil")
	}

	// 1. Check if an active webhook exists to prevent conflicts
	webhookInfo, err := s.Client.GetWebhookInfo(ctx)
	if err != nil {
		guestLog.Warnf("failed to check webhook info: %v", err)
	} else if webhookInfo != nil && webhookInfo.URL != "" {
		guestLog.Warnf("active webhook found (%s). getUpdates polling skipped to avoid conflict.", webhookInfo.URL)
		return nil
	}

	// 2. Fetch getMe with retry/backoff
	var me *botAPIUser
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		me, err = s.Client.GetMe(ctx)
		if err == nil && me != nil && me.Username != "" {
			break
		}

		guestLog.Warnf("getMe failed, retrying in 5s: %v", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}

	username := me.Username
	name := me.FirstName
	if name == "" {
		name = "Music Bot"
	}

	if !me.SupportsGuestQueries {
		guestLog.Warnf("Bot @%s does not have Guest Mode enabled (supports_guest_queries: false). Enable it in @BotFather: /mybots -> Bot Settings -> Guest Mode.", username)
	} else {
		guestLog.Infof("Bot @%s supports Guest Mode (supports_guest_queries: true)", username)
	}

	guestLog.Infof("polling guest_message for @%s", username)

	var offset int64
	for {
		select {
		case <-ctx.Done():
			guestLog.Info("guest polling loop shutting down")
			return ctx.Err()
		default:
		}

		updates, err := s.Client.GetUpdates(ctx, offset, 30, []string{"guest_message"})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}

			sleepDur := 5 * time.Second
			var apiErr *BotAPIError
			if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
				sleepDur = time.Duration(apiErr.RetryAfter) * time.Second
			}

			guestLog.Warnf("getUpdates error: %v (sleeping %v)", err, sleepDur)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleepDur):
			}
			continue
		}

		for _, upd := range updates {
			if upd.UpdateID >= offset {
				offset = upd.UpdateID + 1
			}

			qid := ""
			if upd.GuestMessage != nil {
				qid = upd.GuestMessage.GuestQueryID
				if qid == "" {
					qid = upd.GuestMessage.ID
				}
			}
			if qid == "" {
				qid = upd.GuestQueryID
			}

			if qid != "" {
				guestLog.Debugf("received guest query %s", qid)
				_ = s.Answer(ctx, qid, username, name)
			}
		}
	}
}

func sanitizeError(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), token, "[REDACTED_TOKEN]")
	return errors.New(msg)
}

// StartGuestMode starts the background Guest Mode polling service if not already running.
func StartGuestMode(ctx context.Context) {
	guestMu.Lock()
	defer guestMu.Unlock()

	if guestRunning.Load() {
		return
	}

	if os.Getenv("DISABLE_GUEST_MODE") == "true" {
		guestLog.Info("Guest Mode disabled by DISABLE_GUEST_MODE environment variable")
		return
	}

	token := config.Token
	if token == "" {
		guestLog.Warn("Bot token is empty; Guest Mode polling will not start")
		return
	}

	guestCtx, cancel := context.WithCancel(ctx)
	guestCancel = cancel
	guestRunning.Store(true)

	service := NewGuestService(token, config.SupportChat, config.OwnerID)

	go func() {
		defer func() {
			guestRunning.Store(false)
		}()
		guestLog.Info("starting guest loop")
		if err := service.Run(guestCtx); err != nil && !errors.Is(err, context.Canceled) {
			guestLog.Errorf("guest loop exited with error: %v", err)
		}
	}()
}

// StopGuestMode terminates the Guest Mode background service gracefully.
func StopGuestMode() {
	guestMu.Lock()
	defer guestMu.Unlock()

	if guestCancel != nil {
		guestCancel()
		guestCancel = nil
	}
	guestRunning.Store(false)
}

// IsGuestModeRunning reports whether Guest Mode is currently active.
func IsGuestModeRunning() bool {
	return guestRunning.Load()
}

