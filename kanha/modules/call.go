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
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"KanhaMusic/kanha/logger"

	"KanhaMusic/kanha/core"
	state "KanhaMusic/kanha/core/models"
	"KanhaMusic/kanha/locales"
	"KanhaMusic/kanha/platforms"
	"KanhaMusic/kanha/utils"
	"KanhaMusic/ntgcalls"
)

var (
	autoplayHistory sync.Map
	failedTracks    sync.Map // videoID -> time.Time
	chatWorkers     sync.Map // chatID -> *chatWorker
)

const failedTracksTTL = 10 * time.Minute

type chatWorker struct {
	busy atomic.Bool
}

func getChatWorker(chatID int64) *chatWorker {
	val, ok := chatWorkers.Load(chatID)
	if !ok {
		w := &chatWorker{}
		actual, _ := chatWorkers.LoadOrStore(chatID, w)
		return actual.(*chatWorker)
	}
	return val.(*chatWorker)
}

func markTrackFailed(videoID string) {
	if videoID == "" {
		return
	}
	failedTracks.Store(videoID, time.Now())
}

func isTrackFailedRecently(videoID string) bool {
	if videoID == "" {
		return false
	}
	val, ok := failedTracks.Load(videoID)
	if !ok {
		return false
	}
	failedAt := val.(time.Time)
	if time.Since(failedAt) > failedTracksTTL {
		failedTracks.Delete(videoID)
		return false
	}
	return true
}

func getChatHistoryMap(chatID int64) *sync.Map {
	val, ok := autoplayHistory.Load(chatID)
	if !ok {
		m := &sync.Map{}
		autoplayHistory.Store(chatID, m)
		return m
	}
	return val.(*sync.Map)
}

func apMarkPlayed(chatID int64, videoID string) {
	if videoID == "" {
		return
	}
	m := getChatHistoryMap(chatID)
	m.Store(videoID, time.Now())
}

func apIsPlayed(chatID int64, videoID string) bool {
	if videoID == "" {
		return false
	}
	m := getChatHistoryMap(chatID)
	_, exists := m.Load(videoID)
	return exists
}

func apClearChat(chatID int64) {
	autoplayHistory.Delete(chatID)
}

func callDiscardedHandler(chatID int64) {
	r, ok := core.RoomFor(chatID)
	if !ok {
		return
	}

	logger.Debugf("[callDiscardedHandler] Group call discarded in chat %d", chatID)

	scheduleOldPlayingMessage(r)
	cid := r.ChatID
	apClearChat(chatID)
	core.DropRoom(chatID)

	if _, err := core.Bot.SendTextMessage(cid, F(cid, "call_discarded"), nil); err != nil {
		logger.Error(err)
	}
}

func streamEndHandler(
	chatID int64,
	streamType ntgcalls.StreamType,
	_ ntgcalls.StreamDevice,
) {
	if streamType == ntgcalls.VideoStream {
		logger.Debug("[onStreamEndHandler] Video stream ended, returning")
		return
	}

	worker := getChatWorker(chatID)
	if !worker.busy.CompareAndSwap(false, true) {
		logger.Infof("[Autoplay] Duplicate trigger ignored")
		return
	}
	defer worker.busy.Store(false)

	logger.Infof("[Autoplay] Worker started")

	r, ok := core.RoomFor(chatID)
	if !ok || r.Destroyed() {
		return
	}

	if r.IsManualTransitionInProgress() {
		logger.Infof("[Autoplay] Manual transition in progress for chat %d, ignoring stream-end callback", chatID)
		return
	}

	if natural, reason := r.IsNaturalStreamEnd(r.ActiveTrackGen()); !natural {
		logger.Warnf("[Autoplay] Stream-end ignored for chat %d: %s", chatID, reason)
		return
	}

	scheduleOldPlayingMessage(r)

	c := core.Bot
	cid := r.ChatID
	r.Parse()

	prevTrack := r.Track()

	var t *state.Track
	var wasLooping bool
	var isAutoplay bool

	t = r.NextTrack()
	if t == nil {
		t = tryAutoplay(chatID, r)
		if t == nil {
			apClearChat(chatID)
			core.DropRoom(chatID)
			if _, err := c.SendTextMessage(cid, F(cid, "stream_queue_finished"), nil); err != nil {
				logger.Error(err)
			}
			return
		}
		t.Requester = "🎵 ᴀᴜᴛᴏᴘʟᴀʏ"
		isAutoplay = true
	} else if prevTrack != nil && t == prevTrack {
		wasLooping = true
	}

	var workerGen uint64
	var apCtx context.Context
	var apCancel context.CancelFunc

	if isAutoplay {
		workerGen = r.Generation()
		var ok bool
		apCtx, apCancel, ok = r.BeginAutoplay(workerGen)
		if !ok {
			logger.Infof("[Autoplay] Autoplay aborted before starting for chat %d (stale or manual transition active)", chatID)
			return
		}
		defer r.FinishAutoplay(workerGen)
		defer apCancel()

		if !r.IsAutoplayValid(workerGen) || apCtx.Err() != nil {
			logger.Infof("[Autoplay] Autoplay cancelled before status message for chat %d", chatID)
			return
		}
	}

	logger.Infof("[Autoplay] Queue candidate selected: %s", t.Title)

	statusText := F(cid, "stream_downloading_next")
	if isAutoplay {
		statusText = F(cid, "stream_autoplay_next", locales.Arg{
			"title": utils.EscapeHTML(utils.ShortTitle(t.Title, 25)),
		})
	} else if wasLooping && r.FilePath() != "" {
		statusText = F(cid, "cb_replaying")
	}

	statusMsg, err := c.SendTextMessage(cid, statusText, nil)
	if err != nil {
		logger.Errorf("[call.go] Failed to send msg: %v", err)
	}

	var filePath string
	var dlErr error
	if wasLooping && t != nil && r.FilePath() != "" {
		filePath = r.FilePath()
	} else {
		// Parallel thumbnail preparation for the candidate
		thumbCtx := context.Background()
		if isAutoplay {
			thumbCtx = apCtx
		}
		StartNowPlayingPreparation(thumbCtx, c, chatID, t, nil)

		const maxRetries = 4
		const downloadTimeout = 60 * time.Second
		for attempt := 0; attempt < maxRetries; attempt++ {
			if isAutoplay && (!r.IsAutoplayValid(workerGen) || apCtx.Err() != nil) {
				logger.Warnf("[Autoplay] Worker stale/cancelled before download attempt %d for chat %d", attempt, chatID)
				if statusMsg != nil {
					_ = statusMsg.Delete(c, true)
				}
				return
			}

			var dlCtx context.Context
			var cancel context.CancelFunc
			if isAutoplay {
				dlCtx, cancel = context.WithTimeout(apCtx, downloadTimeout)
			} else {
				dlCtx, cancel = context.WithTimeout(context.Background(), downloadTimeout)
			}

			filePath, dlErr = platforms.Download(dlCtx, t, statusMsg)
			cancel()

			// Check after download blocking call
			if isAutoplay && (!r.IsAutoplayValid(workerGen) || apCtx.Err() != nil) {
				logger.Warnf("[Autoplay] Worker stale/cancelled after download attempt %d for chat %d", attempt, chatID)
				if filePath != "" {
					_ = os.Remove(filePath)
				}
				if statusMsg != nil {
					_ = statusMsg.Delete(c, true)
				}
				return
			}

			if dlErr == nil {
				break
			}

			// Record failure so candidate is not repeated
			markTrackFailed(t.ID)
			apMarkPlayed(chatID, t.ID)

			if isAutoplay {
				if !r.IsAutoplayValid(workerGen) || apCtx.Err() != nil {
					logger.Warnf("[Autoplay] Worker stale/cancelled during candidate switch for chat %d", chatID)
					if statusMsg != nil {
						_ = statusMsg.Delete(c, true)
					}
					return
				}
				nextT := pickAutoplayCandidate(chatID, t)
				if nextT == nil {
					break
				}
				nextT.Requester = "🎵 ᴀᴜᴛᴏᴘʟᴀʏ"
				t = nextT
				logger.Infof("[Autoplay] Queue candidate selected: %s", t.Title)
				StartNowPlayingPreparation(apCtx, c, chatID, t, nil)
			} else {
				// If manual queued track failed on all providers, try next queued track if available
				if nextQueued := r.NextTrack(); nextQueued != nil {
					logger.Warnf("[Autoplay] Queued track failed on all providers; moving to next candidate: %s", nextQueued.Title)
					t = nextQueued
					logger.Infof("[Autoplay] Queue candidate selected: %s", t.Title)
					StartNowPlayingPreparation(context.Background(), c, chatID, t, nil)
				} else {
					break
				}
			}
		}
	}

	if isAutoplay && (!r.IsAutoplayValid(workerGen) || apCtx.Err() != nil) {
		logger.Warnf("[Autoplay] Worker stale or cancelled before Play for chat %d, aborting", chatID)
		if filePath != "" {
			_ = os.Remove(filePath)
		}
		if statusMsg != nil {
			_ = statusMsg.Delete(c, true)
		}
		return
	}

	if dlErr != nil {
		logger.Errorf(
			"[onStreamEndHandler] Download failed for %s: %v",
			t.URL,
			dlErr,
		)
		utils.EOR(c, statusMsg, F(cid, "stream_download_fail", locales.Arg{
			"error": dlErr.Error(),
		}), nil)
		core.DropRoom(chatID)

		return
	}

	// Check generation immediately before r.Play()
	if isAutoplay && !r.IsAutoplayValid(workerGen) {
		logger.Warnf("[Autoplay] Worker stale immediately before Play for chat %d (workerGen=%d, curGen=%d), aborting", chatID, workerGen, r.Generation())
		if filePath != "" {
			_ = os.Remove(filePath)
		}
		if statusMsg != nil {
			_ = statusMsg.Delete(c, true)
		}
		return
	}

	logger.Infof("[Autoplay] Starting playback for %s", t.Title)
	forcePlay := !isAutoplay
	if err := r.Play(t, filePath, forcePlay); err != nil {
		logger.Errorf(
			"[onStreamEndHandler] Play failed for %s: %v",
			t.URL,
			err,
		)
		utils.EOR(c, statusMsg, F(cid, "stream_play_fail"), nil)
		core.DropRoom(chatID)

		return
	}

	if isAutoplay {
		r.SetActiveTrackGen(workerGen)
	}

	statusMsg = sendNowPlaying(c, statusMsg, cid, r, t)
	r.SetStatusMsg(statusMsg)

	if isAutoplay {
		logger.Infof("[Autoplay] Track retained; thumbnail failure does not affect playback")
		go sendAutoplayLogs(c, cid, t, statusMsg)
	}
}

func tryAutoplay(chatID int64, r *core.RoomState) *state.Track {
	if r == nil || !r.Autoplay() {
		logger.Debugf("[Autoplay] Autoplay is disabled for chat %d", chatID)
		return nil
	}
	return pickAutoplayCandidate(chatID, r.Track())
}

func pickAutoplayCandidate(chatID int64, cur *state.Track) *state.Track {
	if cur == nil || cur.ID == "" {
		logger.Warnf("[Autoplay] Current track is nil or empty for chat %d", chatID)
		return nil
	}

	apMarkPlayed(chatID, cur.ID)

	var candidates []*state.Track

	mixPlaylistID := cur.ID
	if !strings.HasPrefix(mixPlaylistID, "RD") {
		mixPlaylistID = "RD" + cur.ID
	}

	mix, err := platforms.GetYouTubeMixPlaylist(context.Background(), mixPlaylistID)
	if err == nil && len(mix) > 0 {
		for i, t := range mix {
			if i >= 15 {
				break
			}
			if t.ID != "" && t.ID != cur.ID && !apIsPlayed(chatID, t.ID) && !isTrackFailedRecently(t.ID) {
				candidates = append(candidates, t)
			}
		}
	}

	if len(candidates) == 0 {
		cleanTitle := cleanSongTitleForSearch(cur.Title)
		logger.Infof("[Autoplay] Radio mix empty for %s, falling back to smart search: %q", cur.Title, cleanTitle)
		query := fmt.Sprintf("%s similar songs", cleanTitle)
		if searchTracks, sErr := platforms.SearchTracks(query, false); sErr == nil && len(searchTracks) > 0 {
			for _, t := range searchTracks {
				if t.ID != "" && t.ID != cur.ID && !apIsPlayed(chatID, t.ID) && !isTrackFailedRecently(t.ID) {
					candidates = append(candidates, t)
				}
			}
		}
	}

	if len(candidates) == 0 {
		logger.Infof("[Autoplay] All candidates exhausted for chat %d, clearing history and stopping autoplay", chatID)
		apClearChat(chatID)
		return nil
	}

	maxPool := len(candidates)
	if maxPool > 4 {
		maxPool = 4
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(maxPool)))
	var chosen *state.Track
	if err != nil {
		chosen = candidates[0]
	} else {
		chosen = candidates[n.Int64()]
	}

	apMarkPlayed(chatID, chosen.ID)
	chosen.Requester = "🎵 ᴀᴜᴛᴏᴘʟᴀʏ"
	chosen.IsAutoplay = true
	logger.Infof("[Autoplay] Successfully picked Golden-Zone song: %q (%s) for chat %d", chosen.Title, chosen.ID, chatID)
	return chosen
}

func cleanSongTitleForSearch(title string) string {
	title = strings.Split(title, "|")[0]
	title = strings.Split(title, "(")[0]
	title = strings.Split(title, "[")[0]
	return strings.TrimSpace(title)
}
