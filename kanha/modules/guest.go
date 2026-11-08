/*
 * ● KanhaMusic
 * ○ High-performance Telegram Bot API 10.0 Guest Mode service.
 *
 * Copyright (C) 2026 Kanha
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
	"math/rand"
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

// SentGuestMessage describes the result of a successful answerGuestQuery call.
type SentGuestMessage struct {
	InlineMessageID string `json:"inline_message_id,omitempty"`
}

type botAPIResponseRaw struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after,omitempty"`
	} `json:"parameters,omitempty"`
}

type botAPIUser struct {
	ID                   int64  `json:"id"`
	IsBot                bool   `json:"is_bot"`
	FirstName            string `json:"first_name"`
	LastName             string `json:"last_name,omitempty"`
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

type LinkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

type InputTextMessageContent struct {
	MessageText        string              `json:"message_text"`
	ParseMode          string              `json:"parse_mode,omitempty"`
	LinkPreviewOptions *LinkPreviewOptions `json:"link_preview_options,omitempty"`
}

type GuestQueryResultArticle struct {
	Type                string                  `json:"type"`
	ID                  string                  `json:"id"`
	Title               string                  `json:"title"`
	Description         string                  `json:"description,omitempty"`
	ThumbnailURL        string                  `json:"thumbnail_url,omitempty"`
	ThumbURL            string                  `json:"thumb_url,omitempty"`
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
	Leaf       string
	Video      string
	Snowflake  string
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
		Leaf:       getEnvFirst("EMOJI_LEAF", "GUEST_EMOJI_LEAF"),
		Video:      getEnvFirst("EMOJI_VIDEO", "GUEST_EMOJI_VIDEO"),
		Snowflake:  getEnvFirst("EMOJI_SNOWFLAKE", "GUEST_EMOJI_SNOWFLAKE"),
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

// buildGuestCardText implements the promotional card layout from guest.py.
func buildGuestCardText(username, name string, emojis GuestEmojis, fancy bool) string {
	n := html.EscapeString(name)
	leaf := formatEmoji(emojis.Leaf, "🍂", fancy)
	video := formatEmoji(emojis.Video, "🎥", fancy)
	snow := formatEmoji(emojis.Snowflake, "❄️", fancy)

	text := fmt.Sprintf(
		"❖ <a href=\"https://t.me/%s\">˹%s˼ ♪</a> — <b>𝐘ᴏᴜʀ 𝐏ʀᴇᴍɪᴜᴍ 𝐌ᴜsɪᴄ 𝐒ᴛʀᴇᴍᴀʀ 𝐁ᴏᴛ %s</b>\n\n"+
			"<blockquote><b>▸ 𝐅ᴀsᴛ • 𝐋ᴀɢ 𝐅ʀᴇᴇ • 𝐍ᴏ 𝐀ᴅs %s</b>\n"+
			"<b>▸ 𝐀ᴜᴛᴏ-𝐏ʟᴀʏ • 𝐀ᴜᴅɪᴏ • 𝐕ɪᴅᴇᴏ %s</b></blockquote>\n\n"+
			"<b>◼️ 𝐀ᴅᴅ</b> <a href=\"https://t.me/%s\">˹%s˼ ♪</a> <b>𝐓ᴏ 𝐘ᴏᴜʀ 𝐆ʀᴏᴜᴘ &amp; 𝐄ɴᴊᴏʏ 𝐇ɪɢʜ 𝐐ᴜᴀʟɪᴛʏ 𝐒ᴏɴɢ %s</b>",
		username, n, leaf, leaf, video, username, n, snow,
	)

	if !fancy {
		text = stripCustomEmoji(text)
	}
	return text
}

// buildGuestKeyboard creates the inline keyboard matching guest.py.
func buildGuestKeyboard(username string, supportChat string, ownerID int64, emojis GuestEmojis, fancy bool) *InlineKeyboardMarkup {
	var rows [][]InlineKeyboardButton

	// Row 1: Add me to your group (from guest.py)
	btnRow1 := InlineKeyboardButton{
		Text: "✙ ʌᴅᴅ ϻє ɪη ʏσυʀ ɢʀσυᴘ ✙",
		URL:  fmt.Sprintf("https://t.me/%s?startgroup=true", username),
	}
	if fancy {
		btnRow1.Style = "primary"
		if emojis.Rocket != "" {
			btnRow1.IconCustomEmojiID = emojis.Rocket
		}
	}
	rows = append(rows, []InlineKeyboardButton{btnRow1})

	// Row 2: Support & Owner if configured
	var row2 []InlineKeyboardButton
	supportURL := formatSupportURL(supportChat)
	if supportURL != "" {
		btnSupport := InlineKeyboardButton{
			Text: "sᴜᴘᴘᴏʀᴛ",
			URL:  supportURL,
		}
		if fancy && emojis.Chat != "" {
			btnSupport.IconCustomEmojiID = emojis.Chat
		}
		row2 = append(row2, btnSupport)
	}
	if ownerID != 0 {
		btnOwner := InlineKeyboardButton{
			Text: "ᴏᴡɴᴇʀ",
			URL:  fmt.Sprintf("tg://user?id=%d", ownerID),
		}
		if fancy && emojis.Headphones != "" {
			btnOwner.IconCustomEmojiID = emojis.Headphones
		}
		row2 = append(row2, btnOwner)
	}
	if len(row2) > 0 {
		rows = append(rows, row2)
	}

	return &InlineKeyboardMarkup{
		InlineKeyboard: rows,
	}
}

// buildGuestResult constructs the full GuestQueryResultArticle for Bot API.
func buildGuestResult(username, name, supportChat, thumbURL string, ownerID int64, emojis GuestEmojis, fancy bool) GuestQueryResultArticle {
	if thumbURL == "" {
		thumbURL = "https://files.catbox.moe/qv2ob4.jpg"
	}
	text := buildGuestCardText(username, name, emojis, fancy)
	return GuestQueryResultArticle{
		Type:         "article",
		ID:           "1",
		Title:        fmt.Sprintf("❖ %s ♪", name),
		Description:  fmt.Sprintf("@%s • Add Me In Your Group 🎶", username),
		ThumbnailURL: thumbURL,
		ThumbURL:     thumbURL,
		InputMessageContent: InputTextMessageContent{
			MessageText: text,
			ParseMode:   "HTML",
			LinkPreviewOptions: &LinkPreviewOptions{
				IsDisabled: true,
			},
		},
		ReplyMarkup: buildGuestKeyboard(username, supportChat, ownerID, emojis, fancy),
	}
}

// BotAPIClient defines operations used to interact with Telegram Bot API HTTPS endpoints.
type BotAPIClient interface {
	GetMe(ctx context.Context) (*botAPIUser, error)
	GetWebhookInfo(ctx context.Context) (*botAPIWebhookInfo, error)
	GetUpdates(ctx context.Context, offset int64, timeout int, allowed []string) ([]botAPIUpdate, error)
	AnswerGuestQuery(ctx context.Context, qid string, result GuestQueryResultArticle) (*SentGuestMessage, error)
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
	var resp botAPIResponseRaw
	if err := c.doRequest(ctx, "getMe", nil, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		retryAfter := 0
		if resp.Parameters != nil {
			retryAfter = resp.Parameters.RetryAfter
		}
		return nil, &BotAPIError{StatusCode: 200, ErrorCode: resp.ErrorCode, Description: resp.Description, RetryAfter: retryAfter}
	}

	var user botAPIUser
	if err := json.Unmarshal(resp.Result, &user); err != nil {
		return nil, fmt.Errorf("unmarshal getMe user failed: %w", err)
	}
	return &user, nil
}

func (c *defaultBotAPIClient) GetWebhookInfo(ctx context.Context) (*botAPIWebhookInfo, error) {
	var resp botAPIResponseRaw
	if err := c.doRequest(ctx, "getWebhookInfo", nil, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		retryAfter := 0
		if resp.Parameters != nil {
			retryAfter = resp.Parameters.RetryAfter
		}
		return nil, &BotAPIError{StatusCode: 200, ErrorCode: resp.ErrorCode, Description: resp.Description, RetryAfter: retryAfter}
	}

	var info botAPIWebhookInfo
	if err := json.Unmarshal(resp.Result, &info); err != nil {
		return nil, fmt.Errorf("unmarshal getWebhookInfo failed: %w", err)
	}
	return &info, nil
}

func (c *defaultBotAPIClient) GetUpdates(ctx context.Context, offset int64, timeout int, allowed []string) ([]botAPIUpdate, error) {
	params := getUpdatesParams{
		Offset:         offset,
		Timeout:        timeout,
		AllowedUpdates: allowed,
	}
	var resp botAPIResponseRaw
	if err := c.doRequest(ctx, "getUpdates", params, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		retryAfter := 0
		if resp.Parameters != nil {
			retryAfter = resp.Parameters.RetryAfter
		}
		return nil, &BotAPIError{StatusCode: 200, ErrorCode: resp.ErrorCode, Description: resp.Description, RetryAfter: retryAfter}
	}

	var updates []botAPIUpdate
	if len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, &updates); err != nil {
			return nil, fmt.Errorf("unmarshal getUpdates failed: %w", err)
		}
	}
	return updates, nil
}

// AnswerGuestQuery decodes Telegram's SentGuestMessage or treats ok:true as success.
func (c *defaultBotAPIClient) AnswerGuestQuery(ctx context.Context, qid string, result GuestQueryResultArticle) (*SentGuestMessage, error) {
	params := answerGuestQueryParams{
		GuestQueryID: qid,
		Result:       result,
	}
	var resp botAPIResponseRaw
	if err := c.doRequest(ctx, "answerGuestQuery", params, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		retryAfter := 0
		if resp.Parameters != nil {
			retryAfter = resp.Parameters.RetryAfter
		}
		return nil, &BotAPIError{
			StatusCode:  200,
			ErrorCode:   resp.ErrorCode,
			Description: resp.Description,
			RetryAfter:  retryAfter,
		}
	}

	var sent SentGuestMessage
	if len(resp.Result) > 0 && string(resp.Result) != "true" && string(resp.Result) != "false" {
		_ = json.Unmarshal(resp.Result, &sent)
	}
	return &sent, nil
}

// dedupeCache prevents duplicate processing of the same guest query ID.
type dedupeCache struct {
	mu      sync.Mutex
	seen    map[string]time.Time
	timeout time.Duration
}

func newDedupeCache(timeout time.Duration) *dedupeCache {
	return &dedupeCache{
		seen:    make(map[string]time.Time),
		timeout: timeout,
	}
}

func (c *dedupeCache) AddOrCheck(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	// Periodic garbage collection of old IDs
	for k, t := range c.seen {
		if now.Sub(t) > c.timeout {
			delete(c.seen, k)
		}
	}

	if _, exists := c.seen[id]; exists {
		return true // already seen
	}
	c.seen[id] = now
	return false
}

// GuestService coordinates the background polling and answering of guest queries.
type GuestService struct {
	Client      BotAPIClient
	SupportChat string
	OwnerID     int64
	ThumbURL    string
	Emojis      GuestEmojis

	cachedFancy *GuestQueryResultArticle
	cachedPlain *GuestQueryResultArticle
	cachedUser  string
	cachedName  string
	cacheMu     sync.RWMutex
	dedupe      *dedupeCache
}

// NewGuestService constructs a GuestService with the default Bot API client.
func NewGuestService(token, supportChat string, ownerID int64) *GuestService {
	thumb := getEnvFirst("GUEST_THUMB_URL", "THUMB_URL")
	if thumb == "" {
		thumb = "https://files.catbox.moe/qv2ob4.jpg"
	}
	return &GuestService{
		Client:      newDefaultBotAPIClient(token, ""),
		SupportChat: supportChat,
		OwnerID:     ownerID,
		ThumbURL:    thumb,
		Emojis:      loadGuestEmojis(),
		dedupe:      newDedupeCache(5 * time.Minute),
	}
}

func (s *GuestService) cacheCards(username, name string) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	s.cachedUser = username
	s.cachedName = name

	fancy := buildGuestResult(username, name, s.SupportChat, s.ThumbURL, s.OwnerID, s.Emojis, true)
	plain := buildGuestResult(username, name, s.SupportChat, s.ThumbURL, s.OwnerID, s.Emojis, false)
	s.cachedFancy = &fancy
	s.cachedPlain = &plain
}

func (s *GuestService) getCards(username, name string) (GuestQueryResultArticle, GuestQueryResultArticle) {
	s.cacheMu.RLock()
	if s.cachedFancy != nil && s.cachedPlain != nil && s.cachedUser == username && s.cachedName == name {
		fancy := *s.cachedFancy
		plain := *s.cachedPlain
		s.cacheMu.RUnlock()
		return fancy, plain
	}
	s.cacheMu.RUnlock()

	s.cacheCards(username, name)
	s.cacheMu.RLock()
	fancy := *s.cachedFancy
	plain := *s.cachedPlain
	s.cacheMu.RUnlock()
	return fancy, plain
}

func isFormattingOrEmojiError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *BotAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if isQueryExpiredOrInvalidError(err) {
		return false
	}
	desc := strings.ToLower(apiErr.Description)
	return strings.Contains(desc, "custom_emoji") ||
		strings.Contains(desc, "emoji") ||
		strings.Contains(desc, "entity") ||
		strings.Contains(desc, "parse") ||
		strings.Contains(desc, "tag") ||
		strings.Contains(desc, "html") ||
		strings.Contains(desc, "can't use custom emoji") ||
		strings.Contains(desc, "bad request: can't parse")
}

func isQueryExpiredOrInvalidError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *BotAPIError
	if errors.As(err, &apiErr) {
		desc := strings.ToLower(apiErr.Description)
		return strings.Contains(desc, "query is too old") ||
			strings.Contains(desc, "query id is invalid") ||
			strings.Contains(desc, "timeout expired")
	}
	return false
}

// Answer processes a guest query with measurement, fallback retry, and diagnostics.
func (s *GuestService) Answer(ctx context.Context, qid, username, name string, recvTime time.Time) error {
	start := time.Now()
	if !recvTime.IsZero() && time.Since(recvTime) > 8*time.Second {
		guestLog.Warnf("[Diagnostic E] Query %s expired before processing (%v elapsed)", qid, time.Since(recvTime))
		return errors.New("query expired before processing")
	}

	fancyCard, plainCard := s.getCards(username, name)

	// Attempt 1: Premium card
	sent, err := s.Client.AnswerGuestQuery(ctx, qid, fancyCard)
	duration := time.Since(start)

	if err == nil {
		msgID := ""
		if sent != nil {
			msgID = sent.InlineMessageID
		}
		guestLog.Infof("[Diagnostic D] Answer succeeded for query %s in %v (msg_id: %s, fancy)", qid, duration, msgID)
		return nil
	}

	// If query is expired or invalid, NEVER retry
	if isQueryExpiredOrInvalidError(err) {
		guestLog.Warnf("[Diagnostic E] Query %s expired or invalid (cannot retry): %v (%v elapsed)", qid, err, duration)
		return err
	}

	// If Telegram explicitly rejects formatting/emojis, retry with plain fallback
	if isFormattingOrEmojiError(err) {
		guestLog.Warnf("[Diagnostic C] Premium card rejected by Telegram (%v), retrying plain fallback", err)
		plainStart := time.Now()
		sentPlain, plainErr := s.Client.AnswerGuestQuery(ctx, qid, plainCard)
		plainDuration := time.Since(plainStart)

		if plainErr == nil {
			msgID := ""
			if sentPlain != nil {
				msgID = sentPlain.InlineMessageID
			}
			guestLog.Infof("[Diagnostic D] Answer succeeded for query %s in %v (msg_id: %s, plain fallback)", qid, plainDuration, msgID)
			return nil
		}

		guestLog.Errorf("[Diagnostic C] Plain fallback rejected by Telegram for query %s: %v (%v elapsed)", qid, plainErr, time.Since(start))
		return plainErr
	}

	// For other errors (e.g. rate limit, network timeout), do not send a plain duplicate
	guestLog.Errorf("[Diagnostic C] Query %s failed with non-formatting error (%v), skipping plain fallback", qid, err)
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

	// 2. Fetch getMe and verify Guest Mode capability with non-spamming backoff
	var me *botAPIUser
	hasLoggedCapabilityWarning := false

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		me, err = s.Client.GetMe(ctx)
		if err != nil || me == nil || me.Username == "" {
			guestLog.Warnf("getMe failed, retrying in 5s: %v", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				continue
			}
		}

		if !me.SupportsGuestQueries {
			if !hasLoggedCapabilityWarning {
				guestLog.Warnf("[Guest] Bot @%s does not have Guest Mode enabled (supports_guest_queries: false). Enable it in @BotFather: /mybots -> [Select Bot] -> Bot Settings -> Guest Mode -> Turn On. Polling paused; re-checking in 60s.", me.Username)
				hasLoggedCapabilityWarning = true
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(60 * time.Second):
				continue
			}
		}

		// Capability verified
		break
	}

	username := me.Username
	name := me.FirstName
	if me.LastName != "" {
		name = fmt.Sprintf("%s %s", me.FirstName, me.LastName)
	}
	if name == "" {
		name = "Music Bot"
	}

	s.cacheCards(username, name)
	guestLog.Infof("Bot @%s supports Guest Mode (supports_guest_queries: true). Starting polling loop.", username)

	// Bounded worker pool for concurrent query processing
	const maxConcurrentWorkers = 16
	workerSem := make(chan struct{}, maxConcurrentWorkers)
	var workerWg sync.WaitGroup
	defer func() {
		workerWg.Wait()
	}()

	var offset int64
	backoff := 1 * time.Second
	const maxBackoff = 30 * time.Second

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

			sleepDur := backoff
			var apiErr *BotAPIError
			if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
				sleepDur = time.Duration(apiErr.RetryAfter) * time.Second
			} else {
				// Exponential backoff with jitter
				jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
				sleepDur = backoff + jitter
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}

			guestLog.Warnf("getUpdates error: %v (sleeping %v)", err, sleepDur)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleepDur):
			}
			continue
		}

		// Reset backoff on successful response
		backoff = 1 * time.Second

		if len(updates) == 0 {
			// Diagnostic A: Idle poll, no guest update received
			continue
		}

		now := time.Now()
		for _, upd := range updates {
			if upd.UpdateID >= offset {
				offset = upd.UpdateID + 1
			}

			qid := ""
			chatType := ""
			if upd.GuestMessage != nil {
				qid = upd.GuestMessage.GuestQueryID
				if qid == "" {
					qid = upd.GuestMessage.ID
				}
				chatType = upd.GuestMessage.ChatType
			}
			if qid == "" {
				qid = upd.GuestQueryID
			}

			if qid == "" {
				guestLog.Warnf("[Diagnostic B] Guest update %d received but query ID is missing (chat_type: %s)", upd.UpdateID, chatType)
				continue
			}

			if s.dedupe != nil && s.dedupe.AddOrCheck(qid) {
				guestLog.Debugf("[Diagnostic E] Query %s already processed or duplicate, skipping", qid)
				continue
			}

			guestLog.Debugf("received guest query %s (chat_type: %s)", qid, chatType)

			// Concurrently dispatch query to avoid blocking subsequent updates
			workerWg.Add(1)
			select {
			case workerSem <- struct{}{}:
				go func(queryID string, arrival time.Time) {
					defer func() {
						<-workerSem
						workerWg.Done()
					}()
					_ = s.Answer(ctx, queryID, username, name, arrival)
				}(qid, now)
			default:
				// If concurrency limit reached, run synchronously to preserve delivery
				go func(queryID string, arrival time.Time) {
					defer workerWg.Done()
					_ = s.Answer(ctx, queryID, username, name, arrival)
				}(qid, now)
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
