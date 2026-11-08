/*
 * ● KanhaMusic
 * ○ Unit Tests for Guest Mode (Telegram Bot API 10.0)
 */

package modules

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"KanhaMusic/config"
)

func TestGuestEmojisLoading(t *testing.T) {
	origNotes := os.Getenv("EMOJI_MUSIC_NOTES")
	origBolt := os.Getenv("GUEST_EMOJI_BOLT")
	origLeaf := os.Getenv("EMOJI_LEAF")
	defer func() {
		os.Setenv("EMOJI_MUSIC_NOTES", origNotes)
		os.Setenv("GUEST_EMOJI_BOLT", origBolt)
		os.Setenv("EMOJI_LEAF", origLeaf)
	}()

	os.Setenv("EMOJI_MUSIC_NOTES", "123456789")
	os.Setenv("GUEST_EMOJI_BOLT", "987654321")
	os.Setenv("EMOJI_LEAF", "554433221")

	emojis := loadGuestEmojis()
	if emojis.MusicNotes != "123456789" {
		t.Errorf("expected MusicNotes '123456789', got %q", emojis.MusicNotes)
	}
	if emojis.Bolt != "987654321" {
		t.Errorf("expected Bolt '987654321', got %q", emojis.Bolt)
	}
	if emojis.Leaf != "554433221" {
		t.Errorf("expected Leaf '554433221', got %q", emojis.Leaf)
	}
}

func TestFormatEmoji(t *testing.T) {
	out := formatEmoji("112233", "🎶", true)
	expected := `<tg-emoji emoji-id="112233">🎶</tg-emoji>`
	if out != expected {
		t.Errorf("formatEmoji fancy failed. Got %q, want %q", out, expected)
	}

	out = formatEmoji("112233", "🎶", false)
	if out != "🎶" {
		t.Errorf("formatEmoji plain failed. Got %q, want %q", out, "🎶")
	}

	out = formatEmoji("", "🎶", true)
	if out != "🎶" {
		t.Errorf("formatEmoji empty eid failed. Got %q, want %q", out, "🎶")
	}
}

func TestStripCustomEmoji(t *testing.T) {
	raw := `<b>Bot</b> <tg-emoji emoji-id="12345">🍂</tg-emoji> Hello <tg-emoji emoji-id="67890">🎥</tg-emoji>`
	stripped := stripCustomEmoji(raw)
	expected := `<b>Bot</b> 🍂 Hello 🎥`
	if stripped != expected {
		t.Errorf("stripCustomEmoji failed.\nGot:  %q\nWant: %q", stripped, expected)
	}

	plain := "Just plain text 123"
	if stripCustomEmoji(plain) != plain {
		t.Errorf("stripCustomEmoji modified text without emojis")
	}
}

func TestFormatSupportURL(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"@MySupportGroup", "https://t.me/MySupportGroup"},
		{"MySupportGroup", "https://t.me/MySupportGroup"},
		{"https://t.me/MySupportGroup", "https://t.me/MySupportGroup"},
		{"http://example.com/support", "http://example.com/support"},
		{"", ""},
		{"   ", ""},
	}

	for _, c := range cases {
		out := formatSupportURL(c.input)
		if out != c.expected {
			t.Errorf("formatSupportURL(%q) = %q, want %q", c.input, out, c.expected)
		}
	}
}

func TestBuildGuestCardText(t *testing.T) {
	emojis := GuestEmojis{
		Leaf:      "1001",
		Video:     "1002",
		Snowflake: "1003",
	}

	username := "KiwiMusicBot"
	botName := "Kiwi & <Kanha>"

	// 1. Bot name escaping and link structure
	fancyCard := buildGuestCardText(username, botName, emojis, true)
	if !strings.Contains(fancyCard, `<a href="https://t.me/KiwiMusicBot">˹Kiwi &amp; &lt;Kanha&gt;˼ ♪</a>`) {
		t.Errorf("buildGuestCardText did not format bot name and link correctly: %s", fancyCard)
	}

	// 2. Fancy contains custom emojis and blockquote
	if !strings.Contains(fancyCard, `<tg-emoji emoji-id="1001">🍂</tg-emoji>`) {
		t.Errorf("fancy card missing Leaf emoji tag: %s", fancyCard)
	}
	if !strings.Contains(fancyCard, `<blockquote>`) || !strings.Contains(fancyCard, `</blockquote>`) {
		t.Errorf("card missing blockquote formatting: %s", fancyCard)
	}

	// 3. Plain card strips all custom emojis
	plainCard := buildGuestCardText(username, botName, emojis, false)
	if strings.Contains(plainCard, "<tg-emoji") {
		t.Errorf("plain card contains <tg-emoji> tags: %s", plainCard)
	}
	if !strings.Contains(plainCard, "🍂") || !strings.Contains(plainCard, "🎥") || !strings.Contains(plainCard, "❄️") {
		t.Errorf("plain card missing plain unicode emojis: %s", plainCard)
	}

	// 4. Required promotional phrases from guest.py
	requiredPhrases := []string{
		"𝐘ᴏᴜʀ 𝐏ʀᴇᴍɪᴜᴍ 𝐌ᴜsɪᴄ 𝐒ᴛʀᴇᴍᴀʀ 𝐁ᴏᴛ",
		"▸ 𝐅ᴀsᴛ • 𝐋ᴀɢ 𝐅ʀᴇᴇ • 𝐍ᴏ 𝐀ᴅs",
		"▸ 𝐀ᴜᴛᴏ-𝐏ʟᴀʏ • 𝐀ᴜᴅɪᴏ • 𝐕ɪᴅᴇᴏ",
		"𝐓ᴏ 𝐘ᴏᴜʀ 𝐆ʀᴏᴜᴘ &amp; 𝐄ɴᴊᴏʏ 𝐇ɪɢʜ 𝐐ᴜᴀʟɪᴛʏ 𝐒ᴏɴɢ",
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(fancyCard, phrase) {
			t.Errorf("card missing required phrase %q", phrase)
		}
	}
}

func TestBuildGuestKeyboard(t *testing.T) {
	emojis := GuestEmojis{
		Rocket:     "5001",
		Chat:       "5004",
		Headphones: "5005",
	}

	// Fancy keyboard
	kbFancy := buildGuestKeyboard("TestKiwiBot", "@KiwiSupport", 99887766, emojis, true)
	if len(kbFancy.InlineKeyboard) < 1 {
		t.Fatalf("expected at least 1 row in keyboard")
	}

	// Row 1: Add me to your group matching guest.py
	row1 := kbFancy.InlineKeyboard[0]
	if len(row1) != 1 {
		t.Fatalf("expected 1 button in row 1, got %d", len(row1))
	}
	if row1[0].Text != "✙ ʌᴅᴅ ϻє ɪη ʏσυʀ ɢʀσυᴘ ✙" {
		t.Errorf("row 1 button text mismatch: %q", row1[0].Text)
	}
	if row1[0].URL != "https://t.me/TestKiwiBot?startgroup=true" {
		t.Errorf("row 1 button URL mismatch: %q", row1[0].URL)
	}
	if row1[0].Style != "primary" {
		t.Errorf("row 1 button style expected 'primary', got %q", row1[0].Style)
	}
	if row1[0].IconCustomEmojiID != "5001" {
		t.Errorf("row 1 button custom emoji expected '5001', got %q", row1[0].IconCustomEmojiID)
	}

	// Plain keyboard
	kbPlain := buildGuestKeyboard("TestKiwiBot", "@KiwiSupport", 99887766, emojis, false)
	for rIdx, r := range kbPlain.InlineKeyboard {
		for bIdx, btn := range r {
			if btn.Style != "" {
				t.Errorf("plain button [%d][%d] had non-empty style: %s", rIdx, bIdx, btn.Style)
			}
			if btn.IconCustomEmojiID != "" {
				t.Errorf("plain button [%d][%d] had non-empty IconCustomEmojiID: %s", rIdx, bIdx, btn.IconCustomEmojiID)
			}
		}
	}
}

func TestSanitizeError(t *testing.T) {
	token := "123456789:ABCdefGhIJKlmNoPQRsTUVwxyZ"
	err := errors.New("post https://api.telegram.org/bot123456789:ABCdefGhIJKlmNoPQRsTUVwxyZ/getUpdates timeout")

	sanitized := sanitizeError(err, token)
	if strings.Contains(sanitized.Error(), token) {
		t.Errorf("sanitized error still contains raw token: %s", sanitized.Error())
	}
	if !strings.Contains(sanitized.Error(), "[REDACTED_TOKEN]") {
		t.Errorf("sanitized error does not contain [REDACTED_TOKEN]: %s", sanitized.Error())
	}

	if sanitizeError(nil, token) != nil {
		t.Errorf("sanitizeError(nil) should return nil")
	}
}

// TestSentGuestMessageDecoding verifies Telegram Bot API returns SentGuestMessage object without json unmarshal failure.
func TestSentGuestMessageDecoding(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Telegram returns an object containing inline_message_id, NOT boolean
		w.Write([]byte(`{
			"ok": true,
			"result": {
				"inline_message_id": "AgAAAP123456789_xyz"
			}
		}`))
	}))
	defer ts.Close()

	client := newDefaultBotAPIClient("fake-token", ts.URL)
	sent, err := client.AnswerGuestQuery(context.Background(), "test-qid", GuestQueryResultArticle{
		Type:  "article",
		ID:    "1",
		Title: "Test",
	})
	if err != nil {
		t.Fatalf("AnswerGuestQuery failed to decode SentGuestMessage object: %v", err)
	}
	if sent == nil || sent.InlineMessageID != "AgAAAP123456789_xyz" {
		t.Fatalf("expected inline_message_id 'AgAAAP123456789_xyz', got: %+v", sent)
	}
}

// MockBotAPIClient implements BotAPIClient for testing GuestService logic.
type mockBotAPIClient struct {
	mu                sync.Mutex
	getMeFunc         func(ctx context.Context) (*botAPIUser, error)
	getWebhookFunc    func(ctx context.Context) (*botAPIWebhookInfo, error)
	getUpdatesFunc    func(ctx context.Context, offset int64, timeout int, allowed []string) ([]botAPIUpdate, error)
	answerGuestFunc   func(ctx context.Context, qid string, result GuestQueryResultArticle) (*SentGuestMessage, error)
	answeredResults   []GuestQueryResultArticle
	receivedOffsets   []int64
	answerCallCounter int
}

func (m *mockBotAPIClient) GetMe(ctx context.Context) (*botAPIUser, error) {
	if m.getMeFunc != nil {
		return m.getMeFunc(ctx)
	}
	return &botAPIUser{ID: 1, Username: "MockBot", FirstName: "Mock", SupportsGuestQueries: true}, nil
}

func (m *mockBotAPIClient) GetWebhookInfo(ctx context.Context) (*botAPIWebhookInfo, error) {
	if m.getWebhookFunc != nil {
		return m.getWebhookFunc(ctx)
	}
	return &botAPIWebhookInfo{URL: ""}, nil
}

func (m *mockBotAPIClient) GetUpdates(ctx context.Context, offset int64, timeout int, allowed []string) ([]botAPIUpdate, error) {
	m.mu.Lock()
	m.receivedOffsets = append(m.receivedOffsets, offset)
	m.mu.Unlock()

	if m.getUpdatesFunc != nil {
		return m.getUpdatesFunc(ctx, offset, timeout, allowed)
	}
	return nil, nil
}

func (m *mockBotAPIClient) AnswerGuestQuery(ctx context.Context, qid string, result GuestQueryResultArticle) (*SentGuestMessage, error) {
	m.mu.Lock()
	m.answeredResults = append(m.answeredResults, result)
	m.answerCallCounter++
	m.mu.Unlock()

	if m.answerGuestFunc != nil {
		return m.answerGuestFunc(ctx, qid, result)
	}
	return &SentGuestMessage{InlineMessageID: "msg-123"}, nil
}

// TestGuestService_PremiumSuccessNoFallback verifies that a successful premium answer does NOT retry.
func TestGuestService_PremiumSuccessNoFallback(t *testing.T) {
	var callCount atomic.Int32
	mock := &mockBotAPIClient{
		answerGuestFunc: func(ctx context.Context, qid string, result GuestQueryResultArticle) (*SentGuestMessage, error) {
			callCount.Add(1)
			return &SentGuestMessage{InlineMessageID: "msg-first-try"}, nil
		},
	}

	service := NewGuestService("fake-token", "@Support", 12345)
	service.Client = mock

	err := service.Answer(context.Background(), "qid-ok", "TestBot", "Test Bot", time.Now())
	if err != nil {
		t.Fatalf("expected Answer to succeed, got %v", err)
	}

	if callCount.Load() != 1 {
		t.Fatalf("expected exactly 1 call (no retry on success), got %d", callCount.Load())
	}
}

// TestGuestService_PremiumRejectionPlainFallback verifies fallback only occurs when custom emoji is rejected.
func TestGuestService_PremiumRejectionPlainFallback(t *testing.T) {
	var callCount atomic.Int32
	mock := &mockBotAPIClient{
		answerGuestFunc: func(ctx context.Context, qid string, result GuestQueryResultArticle) (*SentGuestMessage, error) {
			num := callCount.Add(1)
			if num == 1 {
				// Telegram rejects custom emoji
				return nil, &BotAPIError{ErrorCode: 400, Description: "Bad Request: can't use custom emoji"}
			}
			return &SentGuestMessage{InlineMessageID: "msg-fallback"}, nil
		},
	}

	service := NewGuestService("fake-token", "@Support", 12345)
	service.Client = mock

	err := service.Answer(context.Background(), "qid-fallback", "TestBot", "Test Bot", time.Now())
	if err != nil {
		t.Fatalf("expected Answer to succeed after plain fallback, got %v", err)
	}

	if callCount.Load() != 2 {
		t.Fatalf("expected 2 calls (premium then plain), got %d", callCount.Load())
	}
}

// TestGuestService_ExpiredQueryNoRetry verifies that expired queries (400 query is too old) are NEVER retried.
func TestGuestService_ExpiredQueryNoRetry(t *testing.T) {
	var callCount atomic.Int32
	mock := &mockBotAPIClient{
		answerGuestFunc: func(ctx context.Context, qid string, result GuestQueryResultArticle) (*SentGuestMessage, error) {
			callCount.Add(1)
			return nil, &BotAPIError{
				ErrorCode:   400,
				Description: "Bad Request: query is too old and response timeout expired or query ID is invalid",
			}
		},
	}

	service := NewGuestService("fake-token", "@Support", 12345)
	service.Client = mock

	err := service.Answer(context.Background(), "qid-expired", "TestBot", "Test Bot", time.Now())
	if err == nil {
		t.Fatal("expected error for expired query, got nil")
	}

	if callCount.Load() != 1 {
		t.Fatalf("expected exactly 1 call (NEVER retry expired queries), got %d", callCount.Load())
	}
}

// TestDedupeCache verifies query deduplication to prevent duplicate processing.
func TestDedupeCache(t *testing.T) {
	cache := newDedupeCache(1 * time.Minute)

	if cache.AddOrCheck("qid-100") {
		t.Errorf("expected qid-100 to be new")
	}
	if !cache.AddOrCheck("qid-100") {
		t.Errorf("expected qid-100 to be duplicate on second check")
	}
	if cache.AddOrCheck("qid-101") {
		t.Errorf("expected qid-101 to be new")
	}
}

// TestGuestService_Run_WebhookConflict verifies polling is skipped when an active webhook exists.
func TestGuestService_Run_WebhookConflict(t *testing.T) {
	var getUpdatesCalled atomic.Bool
	mock := &mockBotAPIClient{
		getWebhookFunc: func(ctx context.Context) (*botAPIWebhookInfo, error) {
			return &botAPIWebhookInfo{URL: "https://kiwibot.example.com/webhook"}, nil
		},
		getUpdatesFunc: func(ctx context.Context, offset int64, timeout int, allowed []string) ([]botAPIUpdate, error) {
			getUpdatesCalled.Store(true)
			return nil, nil
		},
	}

	service := &GuestService{
		Client: mock,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := service.Run(ctx)
	if err != nil {
		t.Fatalf("expected Run to return nil on active webhook conflict, got: %v", err)
	}

	if getUpdatesCalled.Load() {
		t.Errorf("getUpdates should not be called when an active webhook exists")
	}
}

// TestGuestService_Run_ConcurrentQueriesAndOffsetProgression verifies concurrent query handling and offset progression.
func TestGuestService_Run_ConcurrentQueriesAndOffsetProgression(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var pollCount atomic.Int32
	var answeredQIDs sync.Map

	mock := &mockBotAPIClient{
		getWebhookFunc: func(ctx context.Context) (*botAPIWebhookInfo, error) {
			return &botAPIWebhookInfo{URL: ""}, nil
		},
		getMeFunc: func(ctx context.Context) (*botAPIUser, error) {
			return &botAPIUser{
				ID:                   112233,
				Username:             "KiwiMusicBot",
				FirstName:            "Kiwi Music",
				SupportsGuestQueries: true,
			}, nil
		},
		getUpdatesFunc: func(ctx context.Context, offset int64, timeout int, allowed []string) ([]botAPIUpdate, error) {
			count := pollCount.Add(1)
			if count == 1 {
				// Deliver multiple simultaneous queries across different groups
				return []botAPIUpdate{
					{
						UpdateID: 10,
						GuestMessage: &botAPIGuestMessage{
							GuestQueryID: "grp1-query",
							ChatType:     "supergroup",
						},
					},
					{
						UpdateID: 11,
						GuestMessage: &botAPIGuestMessage{
							GuestQueryID: "grp2-query",
							ChatType:     "group",
						},
					},
					{
						UpdateID: 12, // Missing query ID (Diagnostic B test)
						GuestMessage: &botAPIGuestMessage{
							ChatType: "group",
						},
					},
				}, nil
			}
			if offset != 13 {
				t.Errorf("expected offset 13 on second poll, got %d", offset)
			}
			cancel()
			return nil, context.Canceled
		},
		answerGuestFunc: func(ctx context.Context, qid string, result GuestQueryResultArticle) (*SentGuestMessage, error) {
			answeredQIDs.Store(qid, true)
			return &SentGuestMessage{InlineMessageID: "msg-" + qid}, nil
		},
	}

	service := NewGuestService("fake-token", "@KiwiSupport", 123456)
	service.Client = mock

	_ = service.Run(ctx)

	if _, ok := answeredQIDs.Load("grp1-query"); !ok {
		t.Errorf("grp1-query was not answered")
	}
	if _, ok := answeredQIDs.Load("grp2-query"); !ok {
		t.Errorf("grp2-query was not answered")
	}
}

func TestStartStopGuestMode_Lifecycle(t *testing.T) {
	origToken := config.Token
	origDisable := os.Getenv("DISABLE_GUEST_MODE")
	defer func() {
		config.Token = origToken
		os.Setenv("DISABLE_GUEST_MODE", origDisable)
		StopGuestMode()
	}()

	os.Setenv("DISABLE_GUEST_MODE", "true")
	config.Token = "fake-token"
	StartGuestMode(context.Background())
	if IsGuestModeRunning() {
		t.Errorf("expected Guest Mode not to start when DISABLE_GUEST_MODE=true")
		StopGuestMode()
	}

	os.Setenv("DISABLE_GUEST_MODE", "false")
	config.Token = ""
	StartGuestMode(context.Background())
	if IsGuestModeRunning() {
		t.Errorf("expected Guest Mode not to start with empty token")
		StopGuestMode()
	}

	config.Token = "fake-token-12345"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartGuestMode(ctx)
	if !IsGuestModeRunning() {
		t.Errorf("expected Guest Mode to be running after StartGuestMode")
	}

	// Idempotent
	StartGuestMode(ctx)
	if !IsGuestModeRunning() {
		t.Errorf("expected Guest Mode to still be running")
	}

	StopGuestMode()
	if IsGuestModeRunning() {
		t.Errorf("expected Guest Mode not to be running after StopGuestMode")
	}
}
