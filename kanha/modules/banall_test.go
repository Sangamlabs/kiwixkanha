/*
 * ● Kiwi Music (KanhaMusic fork)
 * ○ Unit Tests for /banall command (Legacy migration)
 */

package modules

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	td "github.com/Kanha/Meow"

	"KanhaMusic/config"
)

func TestBanallPermissionStrings(t *testing.T) {
	expectedErr := "ᴇɪᴛʜᴇʀ ɪ ᴅᴏɴ'ᴛ ʜᴀᴠᴇ ᴛʜᴇ ʀɪɢʜᴛ ᴛᴏ ʀᴇsᴛʀɪᴄᴛ ᴜsᴇʀs ᴏʀ ʏᴏᴜ ᴀʀᴇ ɴᴏᴛ ɪɴ sᴜᴅᴏ ᴜsᴇʀs \n ᴏᴡɴᴇʀ ᴋᴏ ᴘᴀᴘᴀ ʙᴏʟ ᴋᴇ sᴜᴅᴏ ʟᴇ ʟᴇ || @Kaito_3_2||"
	if banallPermissionError != expectedErr {
		t.Fatalf("banallPermissionError mismatch.\nGot:  %q\nWant: %q", banallPermissionError, expectedErr)
	}

	expectedTpl := "<b>‣ ᴇᴋ ᴏʀ ᴍᴀʀ ɢʏᴀ ᴍᴄ 🥺 .</b>\n\n➻ %s"
	if banallSuccessTemplate != expectedTpl {
		t.Fatalf("banallSuccessTemplate mismatch.\nGot:  %q\nWant: %q", banallSuccessTemplate, expectedTpl)
	}
}

func TestAuthorization_OwnerAndSudo(t *testing.T) {
	origOwnerID := config.OwnerID
	defer func() { config.OwnerID = origOwnerID }()

	const testOwnerID int64 = 777888999
	const testNonSudoID int64 = 111222333

	config.OwnerID = testOwnerID

	// 1. Owner must always be authorized
	if !isOwnerOrSudo(testOwnerID) {
		t.Errorf("expected owner (%d) to be authorized, got false", testOwnerID)
	}

	// 2. Random user without DB sudo must NOT be authorized
	if isOwnerOrSudo(testNonSudoID) {
		t.Errorf("expected random user (%d) to NOT be authorized, got true", testNonSudoID)
	}
}

func TestCanMemberRestrict(t *testing.T) {
	cases := []struct {
		name     string
		status   td.ChatMemberStatus
		expected bool
	}{
		{
			name:     "nil status",
			status:   nil,
			expected: false,
		},
		{
			name:     "creator",
			status:   &td.ChatMemberStatusCreator{},
			expected: true,
		},
		{
			name: "admin with restrict rights",
			status: &td.ChatMemberStatusAdministrator{
				Rights: &td.ChatAdministratorRights{CanRestrictMembers: true},
			},
			expected: true,
		},
		{
			name: "admin without restrict rights",
			status: &td.ChatMemberStatusAdministrator{
				Rights: &td.ChatAdministratorRights{CanRestrictMembers: false},
			},
			expected: false,
		},
		{
			name: "admin with nil rights (basic group)",
			status: &td.ChatMemberStatusAdministrator{
				Rights: nil,
			},
			expected: true,
		},
		{
			name:     "regular member",
			status:   &td.ChatMemberStatusMember{},
			expected: false,
		},
		{
			name:     "restricted member",
			status:   &td.ChatMemberStatusRestricted{},
			expected: false,
		},
		{
			name:     "banned member",
			status:   &td.ChatMemberStatusBanned{},
			expected: false,
		},
		{
			name:     "left member",
			status:   &td.ChatMemberStatusLeft{},
			expected: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := canMemberRestrict(tc.status)
			if got != tc.expected {
				t.Errorf("canMemberRestrict() for %s = %v, want %v", tc.name, got, tc.expected)
			}
		})
	}
}

func TestIsMemberEligibleForBan(t *testing.T) {
	const (
		botID   int64 = 1000
		ownerID int64 = 2000
		sudoID  int64 = 3000
		adminID int64 = 4000
		userID1 int64 = 5001
		userID2 int64 = 5002
	)

	isOwnerOrSudoFn := func(id int64) bool {
		return id == sudoID || id == ownerID
	}

	isAdminFn := func(id int64) bool {
		return id == adminID
	}

	cases := []struct {
		name         string
		member       td.ChatMember
		wantID       int64
		wantEligible bool
		wantReason   string
	}{
		{
			name: "bot itself",
			member: td.ChatMember{
				MemberId: &td.MessageSenderUser{UserId: botID},
				Status:   &td.ChatMemberStatusAdministrator{Rights: &td.ChatAdministratorRights{CanRestrictMembers: true}},
			},
			wantID:       botID,
			wantEligible: false,
			wantReason:   "bot",
		},
		{
			name: "chat owner by id",
			member: td.ChatMember{
				MemberId: &td.MessageSenderUser{UserId: ownerID},
				Status:   &td.ChatMemberStatusCreator{},
			},
			wantID:       ownerID,
			wantEligible: false,
			wantReason:   "chat_owner",
		},
		{
			name: "group creator status",
			member: td.ChatMember{
				MemberId: &td.MessageSenderUser{UserId: 2999},
				Status:   &td.ChatMemberStatusCreator{},
			},
			wantID:       2999,
			wantEligible: false,
			wantReason:   "creator",
		},
		{
			name: "sudo user",
			member: td.ChatMember{
				MemberId: &td.MessageSenderUser{UserId: sudoID},
				Status:   &td.ChatMemberStatusMember{},
			},
			wantID:       sudoID,
			wantEligible: false,
			wantReason:   "sudo_or_owner",
		},
		{
			name: "admin by status",
			member: td.ChatMember{
				MemberId: &td.MessageSenderUser{UserId: 4999},
				Status:   &td.ChatMemberStatusAdministrator{},
			},
			wantID:       4999,
			wantEligible: false,
			wantReason:   "admin",
		},
		{
			name: "admin by admin list/cache",
			member: td.ChatMember{
				MemberId: &td.MessageSenderUser{UserId: adminID},
				Status:   &td.ChatMemberStatusMember{},
			},
			wantID:       adminID,
			wantEligible: false,
			wantReason:   "admin_cache",
		},
		{
			name: "already banned member",
			member: td.ChatMember{
				MemberId: &td.MessageSenderUser{UserId: 6001},
				Status:   &td.ChatMemberStatusBanned{},
			},
			wantID:       6001,
			wantEligible: false,
			wantReason:   "already_banned",
		},
		{
			name: "left member",
			member: td.ChatMember{
				MemberId: &td.MessageSenderUser{UserId: 6002},
				Status:   &td.ChatMemberStatusLeft{},
			},
			wantID:       6002,
			wantEligible: false,
			wantReason:   "left",
		},
		{
			name: "non-user sender (e.g. channel)",
			member: td.ChatMember{
				MemberId: &td.MessageSenderChat{ChatId: -100123456789},
				Status:   &td.ChatMemberStatusMember{},
			},
			wantID:       0,
			wantEligible: false,
			wantReason:   "non_user",
		},
		{
			name: "eligible regular member 1",
			member: td.ChatMember{
				MemberId: &td.MessageSenderUser{UserId: userID1},
				Status:   &td.ChatMemberStatusMember{},
			},
			wantID:       userID1,
			wantEligible: true,
			wantReason:   "eligible",
		},
		{
			name: "eligible restricted member 2",
			member: td.ChatMember{
				MemberId: &td.MessageSenderUser{UserId: userID2},
				Status:   &td.ChatMemberStatusRestricted{},
			},
			wantID:       userID2,
			wantEligible: true,
			wantReason:   "eligible",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, eligible, reason := isMemberEligibleForBan(botID, ownerID, isOwnerOrSudoFn, isAdminFn, tc.member)
			if id != tc.wantID {
				t.Errorf("isMemberEligibleForBan() id = %d, want %d", id, tc.wantID)
			}
			if eligible != tc.wantEligible {
				t.Errorf("isMemberEligibleForBan() eligible = %v, want %v", eligible, tc.wantEligible)
			}
			if reason != tc.wantReason {
				t.Errorf("isMemberEligibleForBan() reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}

func TestConcurrencyLock(t *testing.T) {
	const chat1 int64 = -100111111
	const chat2 int64 = -100222222

	// Ensure clean state before test
	releaseBanallLock(chat1)
	releaseBanallLock(chat2)

	// 1. Acquire chat1 lock
	if !acquireBanallLock(chat1) {
		t.Fatalf("failed to acquire initial lock for chat1")
	}

	// 2. Duplicate lock on chat1 must fail
	if acquireBanallLock(chat1) {
		t.Errorf("expected duplicate acquireBanallLock for chat1 to fail, got true")
	}

	if !isBanallRunning(chat1) {
		t.Errorf("expected isBanallRunning(chat1) to be true")
	}

	// 3. Different chat (chat2) must succeed independently
	if !acquireBanallLock(chat2) {
		t.Errorf("expected acquireBanallLock for chat2 to succeed while chat1 locked, got false")
	}

	// 4. Release chat1
	releaseBanallLock(chat1)
	if isBanallRunning(chat1) {
		t.Errorf("expected isBanallRunning(chat1) to be false after release")
	}

	// 5. Re-acquire chat1 after release must succeed
	if !acquireBanallLock(chat1) {
		t.Errorf("expected acquireBanallLock for chat1 to succeed after release, got false")
	}

	// Cleanup
	releaseBanallLock(chat1)
	releaseBanallLock(chat2)
}

func TestBanallExecutor_ExecuteSuccess(t *testing.T) {
	const (
		botID   int64 = 100
		ownerID int64 = 200
		sudoID  int64 = 300
		adminID int64 = 400
	)

	members := []td.ChatMember{
		{MemberId: &td.MessageSenderUser{UserId: botID}, Status: &td.ChatMemberStatusAdministrator{}},
		{MemberId: &td.MessageSenderUser{UserId: ownerID}, Status: &td.ChatMemberStatusCreator{}},
		{MemberId: &td.MessageSenderUser{UserId: sudoID}, Status: &td.ChatMemberStatusMember{}},
		{MemberId: &td.MessageSenderUser{UserId: adminID}, Status: &td.ChatMemberStatusAdministrator{}},
		{MemberId: &td.MessageSenderUser{UserId: 501}, Status: &td.ChatMemberStatusMember{}},
		{MemberId: &td.MessageSenderUser{UserId: 502}, Status: &td.ChatMemberStatusMember{}},
		{MemberId: &td.MessageSenderUser{UserId: 503}, Status: &td.ChatMemberStatusMember{}},
		// Duplicate of 501 in member list (should only be scanned & banned once)
		{MemberId: &td.MessageSenderUser{UserId: 501}, Status: &td.ChatMemberStatusMember{}},
	}

	var bannedUsers []int64
	var banMu sync.Mutex

	var sentReplies []string
	var replyMu sync.Mutex

	executor := &BanallExecutor{
		ChatID:  -10012345,
		BotID:   botID,
		OwnerID: ownerID,
		FetchMembers: func(ctx context.Context) ([]td.ChatMember, error) {
			return members, nil
		},
		IsAdmin: func(userID int64) bool {
			return userID == adminID
		},
		IsOwnerOrSudo: func(userID int64) bool {
			return userID == sudoID || userID == ownerID
		},
		BanMember: func(ctx context.Context, userID int64) error {
			banMu.Lock()
			bannedUsers = append(bannedUsers, userID)
			banMu.Unlock()
			return nil
		},
		SendReply: func(ctx context.Context, text string) error {
			replyMu.Lock()
			sentReplies = append(sentReplies, text)
			replyMu.Unlock()
			return nil
		},
		GetUserMention: func(userID int64) string {
			return fmt.Sprintf("<a href=\"tg://user?id=%d\">User%d</a>", userID, userID)
		},
		Sleep: func(ctx context.Context, d time.Duration) {
			// Fast test: no-op
		},
	}

	stats, err := executor.Execute(context.Background())
	if err != nil {
		t.Fatalf("executor.Execute failed: %v", err)
	}

	// Total unique members: 7 (bot, owner, sudo, admin, 501, 502, 503)
	if stats.Scanned != 7 {
		t.Errorf("stats.Scanned = %d, want 7", stats.Scanned)
	}
	// Skipped: bot, owner, sudo, admin = 4
	if stats.Skipped != 4 {
		t.Errorf("stats.Skipped = %d, want 4", stats.Skipped)
	}
	// Banned: 501, 502, 503 = 3
	if stats.Banned != 3 {
		t.Errorf("stats.Banned = %d, want 3", stats.Banned)
	}
	if stats.Failed != 0 {
		t.Errorf("stats.Failed = %d, want 0", stats.Failed)
	}

	// Verify banned users list
	expectedBans := []int64{501, 502, 503}
	if len(bannedUsers) != len(expectedBans) {
		t.Fatalf("bannedUsers length = %d, want %d", len(bannedUsers), len(expectedBans))
	}
	for i, id := range expectedBans {
		if bannedUsers[i] != id {
			t.Errorf("bannedUsers[%d] = %d, want %d", i, bannedUsers[i], id)
		}
	}

	// Verify reply text format for each banned user
	if len(sentReplies) != 3 {
		t.Fatalf("sentReplies length = %d, want 3", len(sentReplies))
	}
	for i, id := range expectedBans {
		expectedMention := fmt.Sprintf("<a href=\"tg://user?id=%d\">User%d</a>", id, id)
		expectedReply := fmt.Sprintf("<b>‣ ᴇᴋ ᴏʀ ᴍᴀʀ ɢʏᴀ ᴍᴄ 🥺 .</b>\n\n➻ %s", expectedMention)
		if sentReplies[i] != expectedReply {
			t.Errorf("sentReplies[%d] mismatch.\nGot:  %q\nWant: %q", i, sentReplies[i], expectedReply)
		}
	}
}

func TestBanallExecutor_ContinuesOnError(t *testing.T) {
	members := []td.ChatMember{
		{MemberId: &td.MessageSenderUser{UserId: 601}, Status: &td.ChatMemberStatusMember{}},
		{MemberId: &td.MessageSenderUser{UserId: 602}, Status: &td.ChatMemberStatusMember{}}, // will fail
		{MemberId: &td.MessageSenderUser{UserId: 603}, Status: &td.ChatMemberStatusMember{}},
	}

	var banned []int64
	var banMu sync.Mutex

	executor := &BanallExecutor{
		ChatID:  -10099999,
		BotID:   1,
		OwnerID: 2,
		FetchMembers: func(ctx context.Context) ([]td.ChatMember, error) {
			return members, nil
		},
		BanMember: func(ctx context.Context, userID int64) error {
			if userID == 602 {
				return errors.New("simulated telegram permission error on user 602")
			}
			banMu.Lock()
			banned = append(banned, userID)
			banMu.Unlock()
			return nil
		},
		SendReply: func(ctx context.Context, text string) error {
			return nil
		},
		GetUserMention: func(userID int64) string {
			return fmt.Sprintf("User%d", userID)
		},
	}

	stats, err := executor.Execute(context.Background())
	if err != nil {
		t.Fatalf("executor.Execute returned unexpected error: %v", err)
	}

	if stats.Scanned != 3 {
		t.Errorf("stats.Scanned = %d, want 3", stats.Scanned)
	}
	if stats.Banned != 2 {
		t.Errorf("stats.Banned = %d, want 2", stats.Banned)
	}
	if stats.Failed != 1 {
		t.Errorf("stats.Failed = %d, want 1", stats.Failed)
	}

	// 601 and 603 must still be banned despite 602 failing
	expected := []int64{601, 603}
	if len(banned) != len(expected) {
		t.Fatalf("banned length = %d, want %d", len(banned), len(expected))
	}
	for i, id := range expected {
		if banned[i] != id {
			t.Errorf("banned[%d] = %d, want %d", i, banned[i], id)
		}
	}
}

func TestBanallExecutor_ContextCancellation(t *testing.T) {
	members := []td.ChatMember{
		{MemberId: &td.MessageSenderUser{UserId: 701}, Status: &td.ChatMemberStatusMember{}},
		{MemberId: &td.MessageSenderUser{UserId: 702}, Status: &td.ChatMemberStatusMember{}},
		{MemberId: &td.MessageSenderUser{UserId: 703}, Status: &td.ChatMemberStatusMember{}},
	}

	ctx, cancel := context.WithCancel(context.Background())

	var banCount int32
	executor := &BanallExecutor{
		ChatID:  -10088888,
		BotID:   1,
		OwnerID: 2,
		FetchMembers: func(ctx context.Context) ([]td.ChatMember, error) {
			return members, nil
		},
		BanMember: func(ctx context.Context, userID int64) error {
			c := atomic.AddInt32(&banCount, 1)
			if c == 1 {
				cancel() // cancel after first ban
			}
			return nil
		},
		SendReply: func(ctx context.Context, text string) error {
			return nil
		},
		GetUserMention: func(userID int64) string {
			return fmt.Sprintf("User%d", userID)
		},
	}

	stats, err := executor.Execute(ctx)
	if err != nil {
		t.Fatalf("unexpected error on context cancel: %v", err)
	}

	// Loop should stop early due to cancellation
	if stats.Banned > 2 {
		t.Errorf("expected executor to stop early, banned %d", stats.Banned)
	}
}

