/*
 * ● KanhaMusic
 * ○ Unit Tests for Guest Mode (Telegram Bot API 10.0)
 */

package modules

import (
	"context"
	"encoding/json"
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
	defer func() {
		os.Setenv("EMOJI_MUSIC_NOTES", origNotes)
		os.Setenv("GUEST_EMOJI_BOLT", origBolt)
	}()

	os.Setenv("EMOJI_MUSIC_NOTES", "123456789")
	os.Setenv("GUEST_EMOJI_BOLT", "987654321")

	emojis := loadGuestEmojis()
	if emojis.MusicNotes != "123456789" {
		t.Errorf("expected MusicNotes '123456789', got %q", emojis.MusicNotes)
	}
	if emojis.Bolt != "987654321" {
		t.Errorf("expected Bolt '987654321', got %q", emojis.Bolt)
	}
}

func TestFormatEmoji(t *testing.T) {
	// Fancy with custom emoji id
	out := formatEmoji("112233", "🎶", true)
	expected := `<tg-emoji emoji-id="112233">🎶</tg-emoji>`
	if out != expected {
		t.Errorf("formatEmoji fancy failed. Got %q, want %q", out, expected)
	}

	// Plain fallback when fancy is false
	out = formatEmoji("112233", "🎶", false)
	if out != "🎶" {
		t.Errorf("formatEmoji plain failed. Got %q, want %q", out, "🎶")
	}

	// Plain fallback when eid is empty
	out = formatEmoji("", "🎶", true)
	if out != "🎶" {
		t.Errorf("formatEmoji empty eid failed. Got %q, want %q", out, "🎶")
	}
}

func TestStripCustomEmoji(t *testing.T) {
	raw := `<b>Bot</b> <tg-emoji emoji-id="12345">🎶</tg-emoji> Hello <tg-emoji emoji-id="67890">⚡</tg-emoji>`
	stripped := stripCustomEmoji(raw)
	expected := `<b>Bot</b> 🎶 Hello ⚡`
	if stripped != expected {
		t.Errorf("stripCustomEmoji failed.\nGot:  %q\nWant: %q", stripped, expected)
	}

	// No custom emojis present
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
		MusicNotes: "1001",
		Bolt:       "1002",
		Rocket:     "1003",
		Globe:      "1004",
	}

	// 1. Bot name escaping
	botName := "Kiwi & <Kanha>"
	fancyCard := buildGuestCardText(botName, emojis, true)
	if !strings.Contains(fancyCard, "<b>Kiwi &amp; &lt;Kanha&gt;</b>") {
		t.Errorf("buildGuestCardText did not escape HTML in bot name: %s", fancyCard)
	}

	// 2. Contains custom emoji tags when fancy=true
	if !strings.Contains(fancyCard, `<tg-emoji emoji-id="1001">🎶</tg-emoji>`) {
		t.Errorf("fancy card missing MusicNotes emoji tag: %s", fancyCard)
	}
	if !strings.Contains(fancyCard, `<tg-emoji emoji-id="1002">⚡</tg-emoji>`) {
		t.Errorf("fancy card missing Bolt emoji tag: %s", fancyCard)
	}

	// 3. Plain card strips all custom emojis
	plainCard := buildGuestCardText(botName, emojis, false)
	if strings.Contains(plainCard, "<tg-emoji") {
		t.Errorf("plain card contains <tg-emoji> tags: %s", plainCard)
	}
	if !strings.Contains(plainCard, "🎶") || !strings.Contains(plainCard, "⚡") {
		t.Errorf("plain card missing plain unicode emojis: %s", plainCard)
	}

	// 4. Feature bullet points & call to action verification
	requiredPhrases := []string{
		"ʏᴏᴜʀ ᴅᴇᴅɪᴄᴀᴛᴇᴅ ᴍᴜsɪᴄ ʙᴏᴛ",
		"ᴄʀʏsᴛᴀʟ-ᴄʟᴇᴀʀ ᴠᴏɪᴄᴇ ᴄʜᴀᴛ sᴛʀᴇᴀᴍɪɴɢ",
		"ʙʟᴀᴢɪɴɢ-ꜰᴀsᴛ ᴘʟᴀʏʙᴀᴄᴋ ᴡɪᴛʜ ǫᴜᴇᴜᴇ",
		"sᴍᴀʀᴛ ᴀᴜᴛᴏᴘʟᴀʏ &amp; sᴜɢɢᴇsᴛɪᴏɴs (/autoplay)",
		"ʏᴏᴜᴛᴜʙᴇ, sᴘᴏᴛɪꜰʏ &amp; ᴍᴏʀᴇ",
		"/play [song]",
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
		Help:       "5002",
		MusicNotes: "5003",
		Chat:       "5004",
		Headphones: "5005",
	}

	// Test Fancy keyboard
	kbFancy := buildGuestKeyboard("TestKiwiBot", "@KiwiSupport", 99887766, emojis, true)
	if len(kbFancy.InlineKeyboard) != 3 {
		t.Fatalf("expected 3 rows in fancy keyboard, got %d", len(kbFancy.InlineKeyboard))
	}

	// Row 1: Add me to your group
	row1 := kbFancy.InlineKeyboard[0]
	if len(row1) != 1 {
		t.Fatalf("expected 1 button in row 1, got %d", len(row1))
	}
	if row1[0].Text != "ᴀᴅᴅ ᴍᴇ ᴛᴏ ʏᴏᴜʀ ɢʀᴏᴜᴘ" {
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

	// Row 2: Commands & Open Bot
	row2 := kbFancy.InlineKeyboard[1]
	if len(row2) != 2 {
		t.Fatalf("expected 2 buttons in row 2, got %d", len(row2))
	}
	if row2[0].Text != "ᴄᴏᴍᴍᴀɴᴅs" || row2[0].URL != "https://t.me/TestKiwiBot?start=help" || row2[0].IconCustomEmojiID != "5002" {
		t.Errorf("row 2 commands button mismatch: %+v", row2[0])
	}
	if row2[1].Text != "ᴏᴘᴇɴ ʙᴏᴛ" || row2[1].URL != "https://t.me/TestKiwiBot" || row2[1].IconCustomEmojiID != "5003" {
		t.Errorf("row 2 open bot button mismatch: %+v", row2[1])
	}

	// Row 3: Support & Owner
	row3 := kbFancy.InlineKeyboard[2]
	if len(row3) != 2 {
		t.Fatalf("expected 2 buttons in row 3, got %d", len(row3))
	}
	if row3[0].Text != "sᴜᴘᴘᴏʀᴛ" || row3[0].URL != "https://t.me/KiwiSupport" || row3[0].IconCustomEmojiID != "5004" {
		t.Errorf("row 3 support button mismatch: %+v", row3[0])
	}
	if row3[1].Text != "ᴏᴡɴᴇʀ" || row3[1].URL != "tg://user?id=99887766" || row3[1].IconCustomEmojiID != "5005" {
		t.Errorf("row 3 owner button mismatch: %+v", row3[1])
	}

	// Test Plain keyboard (no custom emoji IDs, style empty)
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

	// Test Partial Row 3 (no owner, no support)
	kbNoSupport := buildGuestKeyboard("TestKiwiBot", "", 99887766, emojis, false)
	if len(kbNoSupport.InlineKeyboard) != 3 || len(kbNoSupport.InlineKeyboard[2]) != 1 {
		t.Errorf("expected row 3 with only owner button, got: %+v", kbNoSupport.InlineKeyboard)
	}

	kbNoRow3 := buildGuestKeyboard("TestKiwiBot", "", 0, emojis, false)
	if len(kbNoRow3.InlineKeyboard) != 2 {
		t.Errorf("expected 2 rows when both support and owner are empty, got %d", len(kbNoRow3.InlineKeyboard))
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

func TestDefaultBotAPIClient_Requests(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(path, "/getMe"):
			json.NewEncoder(w).Encode(botAPIResponse[botAPIUser]{
				OK: true,
				Result: botAPIUser{
					ID:                   12345,
					IsBot:                true,
					FirstName:            "KiwiBot",
					Username:             "kiwibot",
					SupportsGuestQueries: true,
				},
			})
		case strings.HasSuffix(path, "/getWebhookInfo"):
			json.NewEncoder(w).Encode(botAPIResponse[botAPIWebhookInfo]{
				OK:     true,
				Result: botAPIWebhookInfo{URL: ""},
			})
		case strings.HasSuffix(path, "/getUpdates"):
			var params getUpdatesParams
			json.NewDecoder(r.Body).Decode(&params)
			if params.Offset != 100 {
				http.Error(w, "bad offset", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(botAPIResponse[[]botAPIUpdate]{
				OK: true,
				Result: []botAPIUpdate{
					{
						UpdateID: 100,
						GuestMessage: &botAPIGuestMessage{
							GuestQueryID: "query-abc-123",
						},
					},
				},
			})
		case strings.HasSuffix(path, "/answerGuestQuery"):
			var params answerGuestQueryParams
			json.NewDecoder(r.Body).Decode(&params)
			if params.GuestQueryID != "query-abc-123" {
				http.Error(w, "bad qid", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(botAPIResponse[bool]{
				OK:     true,
				Result: true,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := newDefaultBotAPIClient("fake-token", ts.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. GetMe
	me, err := client.GetMe(ctx)
	if err != nil {
		t.Fatalf("GetMe failed: %v", err)
	}
	if me.Username != "kiwibot" || !me.SupportsGuestQueries {
		t.Errorf("GetMe returned unexpected user: %+v", me)
	}

	// 2. GetWebhookInfo
	wh, err := client.GetWebhookInfo(ctx)
	if err != nil {
		t.Fatalf("GetWebhookInfo failed: %v", err)
	}
	if wh.URL != "" {
		t.Errorf("GetWebhookInfo returned non-empty URL: %s", wh.URL)
	}

	// 3. GetUpdates
	updates, err := client.GetUpdates(ctx, 100, 10, []string{"guest_message"})
	if err != nil {
		t.Fatalf("GetUpdates failed: %v", err)
	}
	if len(updates) != 1 || updates[0].GuestMessage == nil || updates[0].GuestMessage.GuestQueryID != "query-abc-123" {
		t.Errorf("GetUpdates returned unexpected updates: %+v", updates)
	}

	// 4. AnswerGuestQuery
	err = client.AnswerGuestQuery(ctx, "query-abc-123", GuestQueryResultArticle{
		Type:  "article",
		ID:    "1",
		Title: "KiwiBot",
	})
	if err != nil {
		t.Fatalf("AnswerGuestQuery failed: %v", err)
	}
}

// MockBotAPIClient implements BotAPIClient for testing GuestService logic.
type mockBotAPIClient struct {
	mu                sync.Mutex
	getMeFunc         func(ctx context.Context) (*botAPIUser, error)
	getWebhookFunc    func(ctx context.Context) (*botAPIWebhookInfo, error)
	getUpdatesFunc    func(ctx context.Context, offset int64, timeout int, allowed []string) ([]botAPIUpdate, error)
	answerGuestFunc   func(ctx context.Context, qid string, result GuestQueryResultArticle) error
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

func (m *mockBotAPIClient) AnswerGuestQuery(ctx context.Context, qid string, result GuestQueryResultArticle) error {
	m.mu.Lock()
	m.answeredResults = append(m.answeredResults, result)
	m.answerCallCounter++
	callNum := m.answerCallCounter
	m.mu.Unlock()

	if m.answerGuestFunc != nil {
		return m.answerGuestFunc(ctx, qid, result)
	}
	_ = callNum
	return nil
}

func TestGuestService_AnswerFallback(t *testing.T) {
	var callCount atomic.Int32

	mock := &mockBotAPIClient{
		answerGuestFunc: func(ctx context.Context, qid string, result GuestQueryResultArticle) error {
			currentCall := callCount.Add(1)
			if currentCall == 1 {
				// Fancy attempt rejected by Telegram (e.g. invalid emoji or not premium)
				return &BotAPIError{ErrorCode: 400, Description: "CUSTOM_EMOJI_INVALID"}
			}
			// Plain fallback succeeds
			return nil
		},
	}

	service := &GuestService{
		Client:      mock,
		SupportChat: "@Support",
		OwnerID:     12345,
		Emojis: GuestEmojis{
			MusicNotes: "999",
			Bolt:       "888",
		},
	}

	ctx := context.Background()
	err := service.Answer(ctx, "qid-test-456", "MockBot", "Mock Bot")
	if err != nil {
		t.Fatalf("expected Answer to succeed after fallback, got err: %v", err)
	}

	if callCount.Load() != 2 {
		t.Errorf("expected 2 calls (fancy then plain), got %d", callCount.Load())
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.answeredResults) != 2 {
		t.Fatalf("expected 2 answered results recorded, got %d", len(mock.answeredResults))
	}

	// First attempt must have tg-emoji tag
	if !strings.Contains(mock.answeredResults[0].InputMessageContent.MessageText, "<tg-emoji") {
		t.Errorf("first attempt was not fancy: %s", mock.answeredResults[0].InputMessageContent.MessageText)
	}

	// Second attempt must NOT have tg-emoji tag
	if strings.Contains(mock.answeredResults[1].InputMessageContent.MessageText, "<tg-emoji") {
		t.Errorf("second attempt was not plain: %s", mock.answeredResults[1].InputMessageContent.MessageText)
	}
}

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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := service.Run(ctx)
	if err != nil {
		t.Fatalf("expected Run to return nil on active webhook conflict, got: %v", err)
	}

	if getUpdatesCalled.Load() {
		t.Errorf("getUpdates should not be called when an active webhook exists")
	}
}

func TestGuestService_Run_PollingAndOffsetAdvancement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var pollCount atomic.Int32
	var answeredQIDs []string
	var ansMu sync.Mutex

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
				// Initial poll: offset is 0, return 2 updates
				return []botAPIUpdate{
					{
						UpdateID: 50,
						GuestMessage: &botAPIGuestMessage{
							GuestQueryID: "query-one",
						},
					},
					{
						UpdateID:     51,
						GuestQueryID: "query-two",
					},
				}, nil
			}
			// Second poll: offset should now be 52
			if offset != 52 {
				t.Errorf("expected offset 52 on second poll, got %d", offset)
			}
			// Cancel context to stop Run loop
			cancel()
			return nil, context.Canceled
		},
		answerGuestFunc: func(ctx context.Context, qid string, result GuestQueryResultArticle) error {
			ansMu.Lock()
			answeredQIDs = append(answeredQIDs, qid)
			ansMu.Unlock()
			return nil
		},
	}

	service := &GuestService{
		Client:      mock,
		SupportChat: "@KiwiSupport",
		OwnerID:     123456,
	}

	_ = service.Run(ctx)

	ansMu.Lock()
	defer ansMu.Unlock()
	if len(answeredQIDs) != 2 {
		t.Fatalf("expected 2 queries answered, got %d: %+v", len(answeredQIDs), answeredQIDs)
	}
	if answeredQIDs[0] != "query-one" || answeredQIDs[1] != "query-two" {
		t.Errorf("unexpected answered query IDs: %+v", answeredQIDs)
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

	// 1. With DISABLE_GUEST_MODE=true, should not start
	os.Setenv("DISABLE_GUEST_MODE", "true")
	config.Token = "fake-token"
	StartGuestMode(context.Background())
	if IsGuestModeRunning() {
		t.Errorf("expected Guest Mode not to start when DISABLE_GUEST_MODE=true")
		StopGuestMode()
	}

	// 2. With empty token, should not start
	os.Setenv("DISABLE_GUEST_MODE", "false")
	config.Token = ""
	StartGuestMode(context.Background())
	if IsGuestModeRunning() {
		t.Errorf("expected Guest Mode not to start with empty token")
		StopGuestMode()
	}

	// 3. Normal start and stop
	config.Token = "fake-token-12345"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartGuestMode(ctx)
	if !IsGuestModeRunning() {
		t.Errorf("expected Guest Mode to be running after StartGuestMode")
	}

	// Calling Start again should be a no-op (idempotent)
	StartGuestMode(ctx)
	if !IsGuestModeRunning() {
		t.Errorf("expected Guest Mode to still be running")
	}

	StopGuestMode()
	if IsGuestModeRunning() {
		t.Errorf("expected Guest Mode not to be running after StopGuestMode")
	}
}

