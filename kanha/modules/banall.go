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
	"context"
	"fmt"
	"sync"
	"time"

	td "github.com/Kanha/Meow"

	"KanhaMusic/kanha/logger"
	"KanhaMusic/kanha/utils"
)

const (
	banallPermissionError = "ᴇɪᴛʜᴇʀ ɪ ᴅᴏɴ'ᴛ ʜᴀᴠᴇ ᴛʜᴇ ʀɪɢʜᴛ ᴛᴏ ʀᴇsᴛʀɪᴄᴛ ᴜsᴇʀs ᴏʀ ʏᴏᴜ ᴀʀᴇ ɴᴏᴛ ɪɴ sᴜᴅᴏ ᴜsᴇʀs \n ᴏᴡɴᴇʀ ᴋᴏ ᴘᴀᴘᴀ ʙᴏʟ ᴋᴇ sᴜᴅᴏ ʟᴇ ʟᴇ || @Kaito_3_2||"
	banallSuccessTemplate = "<b>‣ ᴇᴋ ᴏʀ ᴍᴀʀ ɢʏᴀ ᴍᴄ 🥺 .</b>\n\n➻ %s"
)

var (
	activeBanAllMu    sync.Mutex
	activeBanAllChats = make(map[int64]bool)
)

func init() {
	helpTexts["/banall"] = `<i>Ban all non-admin members from the group.</i>

<u>Usage:</u>
<b>/banall</b> — Mass ban eligible members

<b>🔒 Restrictions:</b>
• <b>TALK To Owner</b> @itachiplub02
• Bot must have <b>Ban Users</b> admin right.`
}

// acquireBanallLock prevents duplicate simultaneous /banall operations in the same chat.
func acquireBanallLock(chatID int64) bool {
	activeBanAllMu.Lock()
	defer activeBanAllMu.Unlock()
	if activeBanAllChats[chatID] {
		return false
	}
	activeBanAllChats[chatID] = true
	return true
}

// releaseBanallLock releases the active /banall lock for a chat.
func releaseBanallLock(chatID int64) {
	activeBanAllMu.Lock()
	defer activeBanAllMu.Unlock()
	delete(activeBanAllChats, chatID)
}

// isBanallRunning reports whether a /banall operation is actively running in the chat.
func isBanallRunning(chatID int64) bool {
	activeBanAllMu.Lock()
	defer activeBanAllMu.Unlock()
	return activeBanAllChats[chatID]
}

// canMemberRestrict evaluates whether a chat member status permits restricting/banning members.
func canMemberRestrict(status td.ChatMemberStatus) bool {
	if status == nil {
		return false
	}
	switch s := status.(type) {
	case *td.ChatMemberStatusCreator:
		return true
	case *td.ChatMemberStatusAdministrator:
		if s.Rights == nil {
			return true // In basic groups, administrators have all rights.
		}
		return s.Rights.CanRestrictMembers
	default:
		return false
	}
}

// checkBotCanRestrict queries Telegram to verify if the bot has restriction permissions in chatID.
func checkBotCanRestrict(c *td.Client, chatID int64) bool {
	var botID int64
	if c.Me != nil {
		botID = c.Me.Id
	}
	if botID == 0 {
		me, err := c.GetMe()
		if err == nil && me != nil {
			botID = me.Id
		}
	}
	if botID == 0 {
		return false
	}

	member, err := c.GetChatMember(chatID, &td.MessageSenderUser{UserId: botID})
	if err != nil || member == nil {
		return false
	}
	return canMemberRestrict(member.Status)
}

// isMemberEligibleForBan checks if a given chat member can be banned.
func isMemberEligibleForBan(botID int64, ownerID int64, isOwnerOrSudoFunc func(int64) bool, isAdminFunc func(int64) bool, member td.ChatMember) (int64, bool, string) {
	var targetUserID int64
	switch u := member.MemberId.(type) {
	case *td.MessageSenderUser:
		targetUserID = u.UserId
	default:
		return 0, false, "non_user"
	}

	if targetUserID == 0 {
		return 0, false, "zero_id"
	}

	if botID != 0 && targetUserID == botID {
		return targetUserID, false, "bot"
	}

	if ownerID != 0 && targetUserID == ownerID {
		return targetUserID, false, "chat_owner"
	}

	if isOwnerOrSudoFunc != nil && isOwnerOrSudoFunc(targetUserID) {
		return targetUserID, false, "sudo_or_owner"
	}

	switch member.Status.(type) {
	case *td.ChatMemberStatusCreator:
		return targetUserID, false, "creator"
	case *td.ChatMemberStatusAdministrator:
		return targetUserID, false, "admin"
	case *td.ChatMemberStatusBanned:
		return targetUserID, false, "already_banned"
	case *td.ChatMemberStatusLeft:
		return targetUserID, false, "left"
	}

	if isAdminFunc != nil && isAdminFunc(targetUserID) {
		return targetUserID, false, "admin_cache"
	}

	return targetUserID, true, "eligible"
}

// BanallStats tracks the execution statistics.
type BanallStats struct {
	Scanned int
	Banned  int
	Skipped int
	Failed  int
}

// BanallExecutor coordinates member iteration, filtering, banning, and notifications.
type BanallExecutor struct {
	ChatID         int64
	BotID          int64
	OwnerID        int64
	FetchMembers   func(ctx context.Context) ([]td.ChatMember, error)
	IsAdmin        func(userID int64) bool
	IsOwnerOrSudo  func(userID int64) bool
	BanMember      func(ctx context.Context, userID int64) error
	SendReply      func(ctx context.Context, text string) error
	GetUserMention func(userID int64) string
	Sleep          func(ctx context.Context, d time.Duration)
}

// Execute performs the banall operation over all discovered members.
func (e *BanallExecutor) Execute(ctx context.Context) (*BanallStats, error) {
	stats := &BanallStats{}
	members, err := e.FetchMembers(ctx)
	if err != nil {
		return stats, err
	}

	seen := make(map[int64]bool)
	for _, member := range members {
		if ctx.Err() != nil {
			break
		}

		targetUserID, eligible, _ := isMemberEligibleForBan(e.BotID, e.OwnerID, e.IsOwnerOrSudo, e.IsAdmin, member)
		if targetUserID == 0 || seen[targetUserID] {
			continue
		}
		seen[targetUserID] = true
		stats.Scanned++

		if !eligible {
			stats.Skipped++
			continue
		}

		if err := e.BanMember(ctx, targetUserID); err != nil {
			logger.Warnf("banall: failed to ban member %d in chat %d: %v", targetUserID, e.ChatID, err)
			stats.Failed++
			// Continue on failure, matching legacy 'except Exception: pass'
			continue
		}

		stats.Banned++
		mention := e.GetUserMention(targetUserID)
		replyText := fmt.Sprintf(banallSuccessTemplate, mention)
		if err := e.SendReply(ctx, replyText); err != nil {
			logger.Warnf("banall: failed to send reply for member %d: %v", targetUserID, err)
		}

		if e.Sleep != nil {
			e.Sleep(ctx, 300*time.Millisecond)
		}
	}

	return stats, nil
}

// fetchChatMembers retrieves all members of a chat using supergroup pagination, basic group info, or search fallback.
func fetchChatMembers(ctx context.Context, c *td.Client, chatID int64) ([]td.ChatMember, error) {
	chat, err := c.GetChat(chatID)
	if err != nil {
		logger.Warnf("banall: GetChat failed for %d: %v, attempting search fallback", chatID, err)
		return fetchSearchMembers(ctx, c, chatID)
	}

	switch ct := chat.Type.(type) {
	case *td.ChatTypeSupergroup:
		return fetchSupergroupMembers(ctx, c, ct.SupergroupId, chatID)
	case *td.ChatTypeBasicGroup:
		return fetchBasicGroupMembers(ctx, c, ct.BasicGroupId)
	default:
		return fetchSearchMembers(ctx, c, chatID)
	}
}

// fetchSupergroupMembers paginates through supergroup members.
func fetchSupergroupMembers(ctx context.Context, c *td.Client, supergroupID int64, chatID int64) ([]td.ChatMember, error) {
	var all []td.ChatMember
	var offset int32 = 0
	const limit int32 = 200

	for {
		if ctx.Err() != nil {
			break
		}
		res, err := c.GetSupergroupMembers(limit, offset, supergroupID, nil)
		if err != nil {
			logger.Warnf("banall: GetSupergroupMembers offset %d failed for supergroup %d: %v", offset, supergroupID, err)
			break
		}
		if res == nil || len(res.Members) == 0 {
			break
		}
		all = append(all, res.Members...)
		offset += int32(len(res.Members))
		if int(offset) >= int(res.TotalCount) || len(res.Members) < int(limit) {
			break
		}
	}

	if len(all) == 0 {
		return fetchSearchMembers(ctx, c, chatID)
	}
	return all, nil
}

// fetchBasicGroupMembers retrieves all members of a basic group.
func fetchBasicGroupMembers(ctx context.Context, c *td.Client, basicGroupID int64) ([]td.ChatMember, error) {
	info, err := c.GetBasicGroupFullInfo(basicGroupID)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, nil
	}
	return info.Members, nil
}

// fetchSearchMembers falls back to searching members in the chat.
func fetchSearchMembers(ctx context.Context, c *td.Client, chatID int64) ([]td.ChatMember, error) {
	res, err := c.SearchChatMembers(chatID, 200, "", nil)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return res.Members, nil
}

// banallHandler handles the /banall command.
func banallHandler(c *td.Client, m *td.Message) error {
	// 1. Authorization: Only OWNER / SUDOERS
	if !isOwnerOrSudo(m.SenderID()) {
		_, err := m.ReplyText(c, banallPermissionError, nil)
		return err
	}

	// 2. Chat Type: Groups only
	if m.IsPrivate() {
		_, err := m.ReplyText(c, "<b>❌ Error:</b> This command can only be used in groups.", &td.SendTextMessageOpts{
			ParseMode: td.ParseModeHTML,
		})
		return err
	}

	chatID := m.ChatID()

	// 3. Concurrency Lock: Prevent duplicate runs in same chat
	if !acquireBanallLock(chatID) {
		_, err := m.ReplyText(c, "⚠️ <b>A /banall operation is already running in this chat. Please wait for it to complete.</b>", &td.SendTextMessageOpts{
			ParseMode: td.ParseModeHTML,
		})
		return err
	}
	defer releaseBanallLock(chatID)

	// 4. Permission Check: Bot must have can_restrict_members
	if !checkBotCanRestrict(c, chatID) {
		_, err := m.ReplyText(c, banallPermissionError, nil)
		return err
	}

	var botID int64
	if c.Me != nil {
		botID = c.Me.Id
	} else if me, err := c.GetMe(); err == nil && me != nil {
		botID = me.Id
	}

	// Preload chat owner & admins to ensure accurate exclusions
	ownerID, _ := utils.GetChatOwner(c, chatID)
	adminIDs, _ := utils.RefreshChatAdmin(c, chatID)
	adminMap := make(map[int64]bool, len(adminIDs))
	for _, id := range adminIDs {
		adminMap[id] = true
	}

	executor := &BanallExecutor{
		ChatID:  chatID,
		BotID:   botID,
		OwnerID: ownerID,
		FetchMembers: func(ctx context.Context) ([]td.ChatMember, error) {
			return fetchChatMembers(ctx, c, chatID)
		},
		IsAdmin: func(userID int64) bool {
			return adminMap[userID]
		},
		IsOwnerOrSudo: isOwnerOrSudo,
		BanMember: func(ctx context.Context, userID int64) error {
			err := c.BanChatMember(0, chatID, &td.MessageSenderUser{UserId: userID}, nil)
			if err != nil {
				if wait := getFloodWait(err); wait > 0 && wait <= 30 {
					select {
					case <-time.After(time.Duration(wait) * time.Second):
						err = c.BanChatMember(0, chatID, &td.MessageSenderUser{UserId: userID}, nil)
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
			return err
		},
		SendReply: func(ctx context.Context, text string) error {
			_, err := m.ReplyText(c, text, &td.SendTextMessageOpts{
				ParseMode: td.ParseModeHTML,
			})
			if err != nil {
				if wait := getFloodWait(err); wait > 0 && wait <= 10 {
					select {
					case <-time.After(time.Duration(wait) * time.Second):
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
			return err
		},
		GetUserMention: func(userID int64) string {
			u, _ := c.GetUser(userID)
			return mentionOf(u, userID)
		},
		Sleep: func(ctx context.Context, d time.Duration) {
			select {
			case <-time.After(d):
			case <-ctx.Done():
			}
		},
	}

	ctx := context.Background()
	stats, err := executor.Execute(ctx)
	if err != nil {
		logger.Errorf("banall: execution failed in chat %d: %v", chatID, err)
		_, _ = m.ReplyText(c, fmt.Sprintf("<b>❌ Banall Error:</b> Unable to fetch chat members: %v", err), &td.SendTextMessageOpts{
			ParseMode: td.ParseModeHTML,
		})
		return err
	}

	summary := fmt.Sprintf(
		"<b>🚫 Banall Completed</b>\n\n"+
			"👥 <b>Total Scanned:</b> %d\n"+
			"✅ <b>Successfully Banned:</b> %d\n"+
			"🛡️ <b>Skipped (Admins/Protected):</b> %d\n"+
			"❌ <b>Failed:</b> %d",
		stats.Scanned, stats.Banned, stats.Skipped, stats.Failed,
	)
	_, err = m.ReplyText(c, summary, &td.SendTextMessageOpts{
		ParseMode: td.ParseModeHTML,
	})
	return err
}

