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

package core

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"KanhaMusic/kanha/database"
	"KanhaMusic/kanha/logger"

	td "github.com/Kanha/Meow"

	state "KanhaMusic/kanha/core/models"
)

var (
	rooms   = make(map[int64]*RoomState)
	roomsMu sync.RWMutex

	ErrRoomDestroyed = errors.New("room destroyed")

	FileCacheDuration = 1 * time.Minute
)

// RoomState holds all playback state for a single chat.
type RoomState struct {
	mu sync.RWMutex

	// ID is the canonical playback target id (group or linked channel).
	ID int64
	// ChatID is the UI/context chat id where messages and controls are sent.
	ChatID int64

	filePath  string       // currently playing local media file
	track     *state.Track // active track metadata
	playing   bool         // whether playback is active
	paused    bool         // whether playback is paused
	muted     bool         // whether playback is muted
	speed     float64      // playback speed multiplier
	position  int          // current playback position in seconds
	updatedAt int64        // last state-update timestamp (unix seconds)
	loop      int          // loop mode/state value

	queue   []*state.Track // upcoming tracks
	shuffle bool           // queue shuffle mode

	autoplay      bool     // auto-play recommendations when the queue ends
	playedHistory []string // ring buffer of recent track IDs and normalized titles
	engineToken   string   // cryptographic session verification token for autoplay

	statusMsg *td.Message // latest status message in chat

	Assistant *Assistant  // assistant client bound to this room
	destroyed atomic.Bool // whether room cleanup has completed

	// Playback generation and transition coordinator
	generation                 atomic.Uint64
	activeTrackGen             atomic.Uint64
	manualTransitionInProgress atomic.Bool
	manualTransitionGen        atomic.Uint64
	streamStartTime            time.Time

	// Autoplay cancellation coordinator
	autoplayCancel    context.CancelFunc
	autoplayGen       uint64
	autoplayCandidate *state.Track

	// Session vibe tracking
	sessionVibeMu sync.RWMutex
	sessionVibe   *state.TrackVibeProfile
}

// Room management

func DropRoom(chatID int64) bool {
	_, file, line, _ := runtime.Caller(1)
	logger.Debugf("DeleteRoom called from %s:%d", file, line)

	roomsMu.Lock()
	room, ok := rooms[chatID]
	if !ok || room == nil || room.destroyed.Load() {
		roomsMu.Unlock()
		return false
	}

	delete(rooms, chatID)
	roomsMu.Unlock()

	room.cleanupFile()
	room.CancelPendingAutoplay()
	room.Stop()
	room.destroyed.Store(true)
	return true
}

// RoomFor returns the existing room for a chat.
func RoomFor(chatID int64) (*RoomState, bool) {
	roomsMu.RLock()
	room, exists := rooms[chatID]
	roomsMu.RUnlock()
	return room, exists
}

// CreateRoom returns the room for a chat, creating it if it does not exist.
func CreateRoom(chatID int64, ass *Assistant) *RoomState {
	room, _ := createNewRoom(chatID, ass)
	return room
}

func createNewRoom(chatID int64, ass *Assistant) (*RoomState, bool) {
	roomsMu.Lock()
	defer roomsMu.Unlock()

	room, exists := rooms[chatID]
	if !exists {
		persistedAutoplay, _ := database.Autoplay(chatID)
		room = &RoomState{
			ID:            chatID,
			ChatID:        chatID,
			queue:         []*state.Track{},
			speed:         1.0,
			autoplay:      persistedAutoplay,
			playedHistory: make([]string, 0, 100),
			engineToken:   fmt.Sprintf("ap-%d-%d", chatID, time.Now().UnixNano()),
			Assistant:     ass,
		}
		room.generation.Store(1)
		rooms[chatID] = room
	}

	return room, true
}

// SetAssistant rebinds the room to another assistant (used after the
// assistant for the chat was switched).
func (r *RoomState) SetAssistant(ass *Assistant) {
	r.mu.Lock()
	r.Assistant = ass
	r.mu.Unlock()
}

func AllRooms() map[int64]*RoomState {
	roomsMu.RLock()

	out := make(map[int64]*RoomState, len(rooms))
	var dead []int64

	for chatID, room := range rooms {
		if room == nil || room.destroyed.Load() {
			dead = append(dead, chatID)
			continue
		}
		out[chatID] = room
	}

	roomsMu.RUnlock()

	if len(dead) > 0 {
		roomsMu.Lock()
		for _, chatID := range dead {
			if room := rooms[chatID]; room == nil || room.destroyed.Load() {
				delete(rooms, chatID)
			}
		}
		roomsMu.Unlock()
	}

	return out
}

// Helpers

func (r *RoomState) Destroyed() bool {
	return r.destroyed.Load()
}

func (r *RoomState) updatePosition() {
	if r == nil || r.track == nil || r.updatedAt == 0 {
		return
	}

	current := time.Now().Unix()
	elapsed := float64(current - r.updatedAt)

	if r.playing && !r.paused {
		r.position += int(elapsed * r.speed)
		if r.position >= r.track.Duration {
			r.position = r.track.Duration
			r.playing = false
		}
	}
	r.updatedAt = current
}

// Getters

func (r *RoomState) FilePath() string {
	if r.Destroyed() {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.filePath
}

func (r *RoomState) Loop() int {
	if r.Destroyed() {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loop
}

func (r *RoomState) Position() int {
	if r.Destroyed() {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.position
}

func (r *RoomState) Queue() []*state.Track {
	if r.Destroyed() {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	q := make([]*state.Track, len(r.queue))
	copy(q, r.queue)
	return q
}

func (r *RoomState) Shuffle() bool {
	if r.Destroyed() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.shuffle
}

func (r *RoomState) Autoplay() bool {
	if r.Destroyed() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.autoplay
}

func (r *RoomState) Speed() float64 {
	if r.Destroyed() {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.speed
}

func (r *RoomState) Track() *state.Track {
	if r.Destroyed() {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.track
}

func (r *RoomState) StatusMsg() *td.Message {
	if r.Destroyed() {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.statusMsg
}

// Setters

func (r *RoomState) SetLoop(loop int) {
	if r.Destroyed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loop = loop
}

func (r *RoomState) SetShuffle(enabled bool) {
	if r.Destroyed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shuffle = enabled
}

func (r *RoomState) SetAutoplay(enabled bool) {
	if r.Destroyed() {
		return
	}
	r.mu.Lock()
	r.autoplay = enabled
	chatID := r.ChatID
	r.mu.Unlock()

	_ = database.SetAutoplay(chatID, enabled)
}

func (r *RoomState) addPlayedLocked(t *state.Track) {
	if t == nil {
		return
	}
	if t.ID != "" {
		r.playedHistory = append(r.playedHistory, "id:"+t.ID)
	}
	if norm := state.NormalizeTrackTitle(t.Title); norm != "" {
		r.playedHistory = append(r.playedHistory, "title:"+norm)
	}
	if len(r.playedHistory) > 100 {
		r.playedHistory = r.playedHistory[len(r.playedHistory)-100:]
	}
}

// AddPlayed records a track into the room's history ring buffer.
func (r *RoomState) AddPlayed(t *state.Track) {
	if r == nil || t == nil || r.Destroyed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addPlayedLocked(t)
}

// HasPlayed checks if a track ID or title was recently played.
func (r *RoomState) HasPlayed(id, title string) bool {
	if r == nil || r.Destroyed() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	targetID := "id:" + id
	targetTitle := ""
	if title != "" {
		targetTitle = "title:" + state.NormalizeTrackTitle(title)
	}

	for _, item := range r.playedHistory {
		if id != "" && item == targetID {
			return true
		}
		if targetTitle != "" && item == targetTitle {
			return true
		}
	}
	return false
}

// PlayedHistory returns a copy of recently played track identifiers.
func (r *RoomState) PlayedHistory() []string {
	if r == nil || r.Destroyed() {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.playedHistory))
	copy(out, r.playedHistory)
	return out
}

// ClearPlayedHistory resets the room's playback history buffer.
func (r *RoomState) ClearPlayedHistory() {
	if r == nil || r.Destroyed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.playedHistory = make([]string, 0, 100)
}

// AutoplayToken returns the room's unique autoplay session validation token.
func (r *RoomState) AutoplayToken() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.engineToken
}

// ValidateAutoplayEngine checks if autoplay is active and verified for this room.
func (r *RoomState) ValidateAutoplayEngine() bool {
	if r == nil || r.Destroyed() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.autoplay && r.engineToken != ""
}

func (r *RoomState) SetStatusMsg(m *td.Message) {
	if r.Destroyed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statusMsg = m
}

// Playback generation & transition coordinator

func (r *RoomState) Generation() uint64 {
	if r == nil {
		return 0
	}
	return r.generation.Load()
}

func (r *RoomState) BumpGeneration() uint64 {
	if r == nil {
		return 0
	}
	return r.generation.Add(1)
}

func (r *RoomState) ActiveTrackGen() uint64 {
	if r == nil {
		return 0
	}
	return r.activeTrackGen.Load()
}

func (r *RoomState) SetActiveTrackGen(gen uint64) {
	if r != nil {
		r.activeTrackGen.Store(gen)
	}
}

func (r *RoomState) StreamStartTime() time.Time {
	if r == nil {
		return time.Time{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.streamStartTime
}

func (r *RoomState) SetStreamStartTime(t time.Time) {
	if r != nil {
		r.mu.Lock()
		r.streamStartTime = t
		r.mu.Unlock()
	}
}

// CancelPendingAutoplay cancels any in-flight autoplay worker and clears pending candidate.
func (r *RoomState) CancelPendingAutoplay() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.autoplayCancel != nil {
		r.autoplayCancel()
		r.autoplayCancel = nil
		r.autoplayCandidate = nil
		return true
	}
	return false
}

// HasPendingAutoplay reports if an autoplay candidate download/preparation is in progress.
func (r *RoomState) HasPendingAutoplay() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.autoplayCancel != nil
}

// BeginAutoplay attempts to register an autoplay worker with the given generation.
// Returns a cancellable context, a cancel func, and whether the worker is valid to proceed.
func (r *RoomState) BeginAutoplay(gen uint64) (context.Context, context.CancelFunc, bool) {
	if r == nil || r.destroyed.Load() || r.manualTransitionInProgress.Load() {
		return nil, nil, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.generation.Load() != gen || len(r.queue) > 0 {
		return nil, nil, false
	}

	if r.autoplayCancel != nil {
		r.autoplayCancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.autoplayCancel = cancel
	r.autoplayGen = gen
	return ctx, cancel, true
}

// FinishAutoplay cleans up autoplay worker registration if generation matches.
func (r *RoomState) FinishAutoplay(gen uint64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.autoplayGen == gen {
		if r.autoplayCancel != nil {
			r.autoplayCancel = nil
		}
		r.autoplayCandidate = nil
	}
}

// IsAutoplayValid verifies that an autoplay worker running with generation `gen` is still valid.
// Returns false if a manual command arrived, generation advanced, queue has tracks, or room is destroyed.
func (r *RoomState) IsAutoplayValid(gen uint64) bool {
	if r == nil || r.destroyed.Load() {
		return false
	}
	if r.manualTransitionInProgress.Load() {
		return false
	}
	if r.generation.Load() != gen {
		return false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.autoplayGen != gen {
		return false
	}
	if len(r.queue) > 0 {
		return false
	}
	return true
}

// StartManualTransition starts a manual playback command (/play, /playforce, /skip)
// by incrementing the room generation, cancelling pending autoplay, and setting the transition lock.
func (r *RoomState) StartManualTransition(reason string) uint64 {
	if r == nil {
		return 0
	}
	gen := r.generation.Add(1)
	r.manualTransitionInProgress.Store(true)
	r.manualTransitionGen.Store(gen)

	r.mu.Lock()
	if r.autoplayCancel != nil {
		logger.Infof("[Transition] Cancelling pending autoplay due to %s (gen %d)", reason, gen)
		r.autoplayCancel()
		r.autoplayCancel = nil
		r.autoplayCandidate = nil
	}
	r.mu.Unlock()

	logger.Infof("[Transition] Started %s for room %d (gen %d)", reason, r.ID, gen)
	return gen
}

// FinishManualTransition marks completion of a manual playback command.
func (r *RoomState) FinishManualTransition(gen uint64) {
	if r == nil {
		return
	}
	if r.manualTransitionGen.Load() == gen {
		r.manualTransitionInProgress.Store(false)
		logger.Infof("[Transition] Finished manual transition for room %d (gen %d)", r.ID, gen)
	}
}

// IsManualTransitionInProgress reports if a manual play/skip operation is active.
func (r *RoomState) IsManualTransitionInProgress() bool {
	if r == nil {
		return false
	}
	return r.manualTransitionInProgress.Load()
}

// IsGenuinelyPlaying reports whether media is actively playing (not idle or pending autoplay).
func (r *RoomState) IsGenuinelyPlaying() bool {
	if r == nil || r.destroyed.Load() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.playing && r.track != nil && r.filePath != ""
}

// IsNaturalStreamEnd checks whether a streamEnd callback represents genuine track completion
// rather than a decoder crash, initialization failure, or stale generation.
func (r *RoomState) IsNaturalStreamEnd(gen uint64) (bool, string) {
	if r == nil || r.destroyed.Load() {
		return false, "room destroyed"
	}
	if r.manualTransitionInProgress.Load() {
		return false, "manual transition in progress"
	}
	if activeGen := r.activeTrackGen.Load(); activeGen != 0 && activeGen != gen {
		return false, fmt.Sprintf("stale generation (expected %d, got %d)", activeGen, gen)
	}

	r.mu.RLock()
	startTime := r.streamStartTime
	curTrack := r.track
	r.mu.RUnlock()

	if curTrack != nil && curTrack.Duration > 10 && !startTime.IsZero() {
		elapsed := time.Since(startTime)
		if elapsed < 3*time.Second {
			return false, fmt.Sprintf("premature stream end (%v elapsed for %ds track, possible decoder crash)", elapsed, curTrack.Duration)
		}
	}

	return true, "ok"
}

// Session Vibe Profile Management

// SetVibeProfile sets the room's current session vibe profile.
func (r *RoomState) SetVibeProfile(p *state.TrackVibeProfile) {
	if r == nil {
		return
	}
	r.sessionVibeMu.Lock()
	defer r.sessionVibeMu.Unlock()
	r.sessionVibe = p
}

// GetVibeProfile returns the current session vibe profile, or nil if none.
func (r *RoomState) GetVibeProfile() *state.TrackVibeProfile {
	if r == nil {
		return nil
	}
	r.sessionVibeMu.RLock()
	defer r.sessionVibeMu.RUnlock()
	if r.sessionVibe == nil {
		return nil
	}
	cpy := *r.sessionVibe
	return &cpy
}

// UpdateSessionVibeFromTrack classifies the track and stores it as the session vibe profile.
func (r *RoomState) UpdateSessionVibeFromTrack(t *state.Track) {
	if r == nil || t == nil {
		return
	}
	profile := state.ClassifyTrack(t.Title)
	t.VibeProfile = &profile
	r.SetVibeProfile(&profile)
	logger.Infof("[Vibe] Session profile updated for room %d from track %q: Genre=%s, Vibe=%s, Version=%s, Energy=%s",
		r.ID, t.Title, profile.Genre, profile.Vibe, profile.Version, profile.Energy)
}

// State checks

func (r *RoomState) Active() bool {
	if r.Destroyed() {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updatePosition()
	return r.track != nil && r.playing
}

func (r *RoomState) Paused() bool {
	if r.Destroyed() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.paused && r.track != nil && r.playing
}

func (r *RoomState) Muted() bool {
	if r.Destroyed() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.muted && r.track != nil && r.playing
}

func (r *RoomState) Parse() {
	if r.Destroyed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updatePosition()
}

// Queue

// NextTrack retrieves and prepares the next track in queue
func (r *RoomState) NextTrack() *state.Track {
	if r.Destroyed() {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.track != nil && r.loop > 0 {
		r.position = 0
		r.playing = false
		r.paused = false
		r.muted = false
		r.loop--
		r.updatedAt = time.Now().Unix()
		return r.track
	}

	r.releaseFile()

	if len(r.queue) == 0 {
		return nil
	}

	index := 0
	if r.shuffle {
		index = rand.Intn(len(r.queue))
	}

	next := r.queue[index]
	r.queue = append(r.queue[:index], r.queue[index+1:]...)

	r.track = next
	r.addPlayedLocked(next)
	r.position = 0
	r.playing = false
	r.paused = false
	r.muted = false
	r.updatedAt = time.Now().Unix()

	return next
}

// RemoveTrack removes track(s) from queue
func (r *RoomState) RemoveTrack(index int) {
	if r.Destroyed() {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if index == -1 {
		r.queue = []*state.Track{}
		return
	}

	if index >= 0 && index < len(r.queue) {
		r.queue = append(r.queue[:index], r.queue[index+1:]...)
	}
}

// MoveTrack moves a track from one position to another
func (r *RoomState) MoveTrack(from, to int) {
	if r.Destroyed() {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if from < 0 || from >= len(r.queue) ||
		to < 0 || to >= len(r.queue) ||
		from == to {
		return
	}

	item := r.queue[from]
	r.queue = append(r.queue[:from], r.queue[from+1:]...)

	if to >= len(r.queue) {
		r.queue = append(r.queue, item)
	} else {
		r.queue = append(r.queue[:to], append([]*state.Track{item}, r.queue[to:]...)...)
	}
}

// AddTracks appends multiple tracks to the queue
func (r *RoomState) AddTracks(tracks []*state.Track) {
	if r.Destroyed() {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.queue = append(r.queue, tracks...)
}

// Files

func isTrackUsed(trackID string, skipChatID int64) bool {
	for _, room := range rooms {
		if room == nil || room.track == nil || room.ChatID == skipChatID {
			continue
		}
		if room.track.ID == trackID {
			return true
		}
		if isTrackInQueue(trackID, room.queue) {
			return true
		}
	}
	return false
}

func isTrackInQueue(trackID string, queue []*state.Track) bool {
	limit := min(len(queue), 2)
	for _, q := range queue[:limit] {
		if q != nil && q.ID == trackID {
			return true
		}
	}
	return false
}

func (r *RoomState) releaseFile() {
	if r == nil || r.track == nil {
		return
	}
	scheduleRemove(r.track, r.ID)
}

func (r *RoomState) cleanupFile() {
	if r == nil {
		return
	}

	tracks := make([]*state.Track, 0, 3)
	if r.track != nil {
		tracks = append(tracks, r.track)
	}
	tracks = append(tracks, r.queue...)
	if len(tracks) > 2 {
		tracks = tracks[:2]
	}

	for _, t := range tracks {
		if t == nil || t.ID == "" {
			continue
		}
		scheduleRemove(t, r.ID)
	}
}

// scheduleRemove deletes the track file after FileCacheDuration,
// but only if no other room is using it at deletion time.
func scheduleRemove(track *state.Track, skipChatID int64) {
	if track == nil {
		return
	}

	if FileCacheDuration <= 0 {
		doRemove(track, skipChatID)
		return
	}

	t := *track
	time.AfterFunc(FileCacheDuration, func() {
		doRemove(&t, skipChatID)
	})

	logger.Debugf(
		"scheduled file removal in %s: %s:%s",
		FileCacheDuration, string(track.Source), track.ID,
	)
}

func doRemove(track *state.Track, skipChatID int64) {
	roomsMu.RLock()
	used := isTrackUsed(track.ID, skipChatID)
	roomsMu.RUnlock()

	if used {
		logger.Debugf(
			"file still in use, skipped remove: %s:%s",
			string(track.Source), track.ID,
		)
		return
	}

	findAndRemove(track)
}

func findAndRemove(track *state.Track) {
	t := "audio"
	if track.Video {
		t = "video"
	}

	files, err := filepath.Glob(filepath.Join("downloads", t+"_"+track.ID+"*"))
	if err != nil {
		return
	}

	for _, f := range files {
		os.Remove(f)
		logger.Debugf("removed file: %s", f)
	}
}
