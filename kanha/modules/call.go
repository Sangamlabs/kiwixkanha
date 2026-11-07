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
		r.UpdateSessionVibeFromTrack(t)
		logger.Infof("[Autoplay] Playback started successfully and session profile updated for chat %d", chatID)
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

	room, hasRoom := core.RoomFor(chatID)
	var profile state.TrackVibeProfile
	if hasRoom && room.GetVibeProfile() != nil {
		profile = *room.GetVibeProfile()
	} else {
		profile = state.ClassifyTrack(cur.Title, cur.Artist)
		if hasRoom {
			room.SetVibeProfile(&profile)
		}
	}

	cleanTitle := cleanSongTitleForSearch(cur.Title)
	if profile.Confidence <= 0.40 || (profile.Genre == state.GenreUnknown && profile.Vibe == state.VibeUnknown) {
		logger.Infof("[Vibe] Classification confidence too low (Confidence=%.2f). Using metadata fallback.", profile.Confidence)
	}

	logger.Infof("[Autoplay] Target profile for chat %d:\nGenre=%s\nVibe=%s\nVersion=%s\nEnergy=%s\n(Seed: %q)",
		chatID, profile.Genre, profile.Vibe, profile.Version, profile.Energy, cleanTitle)

	queries := buildVibeSearchQueries(profile, cleanTitle)

	// Collect raw candidates from YouTube Mix and targeted search queries
	var rawCandidates []*state.Track
	seenIDs := make(map[string]bool)

	// 1. YouTube Mix Playlist
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
			if t.ID != "" && t.ID != cur.ID && !seenIDs[t.ID] {
				seenIDs[t.ID] = true
				rawCandidates = append(rawCandidates, t)
			}
		}
	}

	// 2. Search targeted queries
	for _, query := range queries {
		if len(rawCandidates) >= 20 {
			break
		}
		if searchTracks, sErr := platforms.SearchTracks(query, false); sErr == nil && len(searchTracks) > 0 {
			for _, t := range searchTracks {
				if t.ID != "" && t.ID != cur.ID && !seenIDs[t.ID] {
					seenIDs[t.ID] = true
					rawCandidates = append(rawCandidates, t)
				}
			}
		}
	}

	// 3. Filter and Score Candidates
	var exactMatches []*state.Track
	var moderateMatches []*state.Track
	var fallbackMatches []*state.Track

	isProfileUnknown := (profile.Genre == state.GenreUnknown || profile.Genre == state.GenreNone) &&
		(profile.Vibe == state.VibeUnknown || profile.Vibe == state.VibeNone)

	for _, t := range rawCandidates {
		// Strict exclusion of played or failed tracks
		if t.ID == "" || t.ID == cur.ID || apIsPlayed(chatID, t.ID) || (hasRoom && room.HasPlayed(t.ID, t.Title)) {
			logger.Infof("[Autoplay] Candidate rejected for chat %d: %s (ID=%s)\nReason=Already played in current session", chatID, t.Title, t.ID)
			continue
		}
		if isTrackFailedRecently(t.ID) {
			logger.Infof("[Autoplay] Candidate rejected for chat %d: %s (ID=%s)\nReason=Previously failed download in session", chatID, t.Title, t.ID)
			continue
		}

		cProfile := state.ClassifyTrack(t.Title, t.Artist)
		score, reasons := profile.ScoreCandidateDetails(cProfile, cur.Artist, t.Artist)

		// Exclude conflicting genres / styles
		if score < 0 {
			logger.Infof("[Autoplay] Candidate rejected for chat %d: %s (ID=%s)\nReason=Conflicting genre/version (Score=%d)", chatID, t.Title, t.ID, score)
			continue
		}

		reasonStr := "- " + strings.Join(reasons, "\n- ")
		logger.Infof("[Autoplay] Candidate:\n%s (ID=%s)\n[Autoplay] Match Score=%d\nReasons:\n%s",
			t.Title, t.ID, score, reasonStr)

		if isProfileUnknown {
			fallbackMatches = append(fallbackMatches, t)
		} else if score >= 70 {
			exactMatches = append(exactMatches, t)
		} else if score >= 45 {
			moderateMatches = append(moderateMatches, t)
		} else {
			fallbackMatches = append(fallbackMatches, t)
		}
	}

	// 4. Select Candidate from Priority Tiers
	var pool []*state.Track
	if len(exactMatches) > 0 {
		pool = exactMatches
		logger.Infof("[Autoplay] Picked from Exact Vibe Match pool (%d candidates) for chat %d", len(pool), chatID)
	} else if len(moderateMatches) > 0 {
		pool = moderateMatches
		logger.Infof("[Autoplay] Picked from Moderate Vibe Match pool (%d candidates) for chat %d", len(pool), chatID)
	} else if len(fallbackMatches) > 0 {
		pool = fallbackMatches
		logger.Infof("[Autoplay] Picked from Fallback pool (%d candidates) for chat %d", len(pool), chatID)
	}

	if len(pool) == 0 {
		logger.Infof("[Autoplay] All candidates exhausted for chat %d, clearing history and stopping autoplay", chatID)
		apClearChat(chatID)
		return nil
	}

	maxPool := len(pool)
	if maxPool > 4 {
		maxPool = 4
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(maxPool)))
	var chosen *state.Track
	if err != nil {
		chosen = pool[0]
	} else {
		chosen = pool[n.Int64()]
	}

	apMarkPlayed(chatID, chosen.ID)
	if hasRoom {
		room.AddPlayed(chosen)
	}
	chosen.Requester = "🎵 ᴀᴜᴛᴏᴘʟᴀʏ"
	chosen.IsAutoplay = true
	cProf := state.ClassifyTrack(chosen.Title, chosen.Artist)
	chosen.VibeProfile = &cProf

	logger.Infof("[Autoplay] Candidate accepted: %q (%s) [Genre=%s, Vibe=%s, Version=%s] for chat %d",
		chosen.Title, chosen.ID, cProf.Genre, cProf.Vibe, cProf.Version, chatID)
	return chosen
}

func buildVibeSearchQueries(profile state.TrackVibeProfile, cleanTitle string) []string {
	var queries []string

	switch profile.Genre {
	case state.GenreBollywood:
		switch profile.Vibe {
		case state.VibeRomantic:
			queries = append(queries, cleanTitle+" romantic", "bollywood romantic songs", "hindi love songs")
		case state.VibeSad, state.VibeEmotional:
			queries = append(queries, cleanTitle+" sad hindi", "sad bollywood songs", "emotional hindi songs")
		case state.VibeParty, state.VibeEnergetic:
			queries = append(queries, cleanTitle+" dance mix", "bollywood party songs", "hindi dance hits")
		default:
			queries = append(queries, cleanTitle+" bollywood", "bollywood hits", "popular hindi songs")
		}
	case state.GenrePunjabi:
		switch profile.Vibe {
		case state.VibeParty, state.VibeEnergetic:
			queries = append(queries, cleanTitle+" punjabi bhangra", "punjabi party hits")
		case state.VibeSad:
			queries = append(queries, cleanTitle+" sad punjabi", "punjabi sad songs")
		default:
			queries = append(queries, cleanTitle+" punjabi", "latest punjabi hits")
		}
	case state.GenrePhonk:
		switch profile.Version {
		case state.VersionSlowed:
			queries = append(queries, cleanTitle+" slowed phonk", "slowed phonk drift", "phonk slowed")
		case state.VersionSlowedReverb:
			queries = append(queries, cleanTitle+" slowed reverb phonk", "slowed and reverb phonk", "slowed phonk")
		case state.VersionSpedUp, state.VersionNightcore:
			queries = append(queries, cleanTitle+" sped up phonk", "sped up phonk", "phonk drift")
		default:
			queries = append(queries, cleanTitle+" phonk", "drift phonk", "phonk bass")
		}
	case state.GenreHipHop, state.GenreRap:
		queries = append(queries, cleanTitle+" rap", "hip hop rap hits", "rap songs")
	case state.GenreRock:
		queries = append(queries, cleanTitle+" rock", "rock hits", "alternative rock")
	case state.GenreEDM, state.GenreElectronic:
		queries = append(queries, cleanTitle+" edm", "dance edm house", "electronic club")
	case state.GenreLofi:
		queries = append(queries, cleanTitle+" lofi", "lofi chill beats", "lofi hip hop relax")
	case state.GenreIndie:
		queries = append(queries, cleanTitle+" indie", "indie alternative songs", "indie pop")
	case state.GenrePop:
		queries = append(queries, cleanTitle+" pop hits", "pop songs similar")
	}

	// Add vibe-specific queries if genre wasn't specific
	if len(queries) == 0 {
		switch profile.Vibe {
		case state.VibeRomantic:
			queries = append(queries, cleanTitle+" love song", "romantic love songs", "slow romantic acoustic")
		case state.VibeSad:
			queries = append(queries, cleanTitle+" sad songs", "sad songs chill", "melancholy acoustic")
		case state.VibeChill, state.VibeRelaxing:
			queries = append(queries, cleanTitle+" chill", "chill mellow beats", "relaxing chill songs")
		case state.VibeParty, state.VibeEnergetic:
			queries = append(queries, cleanTitle+" party mix", "party dance songs")
		case state.VibeDark, state.VibeAggressive:
			queries = append(queries, cleanTitle+" dark", "dark alternative", "dark underground")
		case state.VibeDevotional:
			queries = append(queries, cleanTitle+" bhajan", "devotional songs")
		case state.VibeLofi:
			queries = append(queries, cleanTitle+" lofi", "lofi chill beats")
		}
	}

	// Add version modifiers if specialized
	switch profile.Version {
	case state.VersionSlowedReverb:
		queries = append(queries, cleanTitle+" slowed and reverb", "slowed reverb songs")
	case state.VersionSlowed:
		queries = append(queries, cleanTitle+" slowed", "slowed songs")
	case state.VersionSpedUp:
		queries = append(queries, cleanTitle+" sped up")
	case state.VersionAcoustic:
		queries = append(queries, cleanTitle+" acoustic")
	}

	queries = append(queries, cleanTitle+" similar songs", cleanTitle)
	return queries
}

func cleanSongTitleForSearch(title string) string {
	title = strings.Split(title, "|")[0]
	title = strings.Split(title, "(")[0]
	title = strings.Split(title, "[")[0]
	return strings.TrimSpace(title)
}
