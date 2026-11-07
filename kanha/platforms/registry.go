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

package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"KanhaMusic/kanha/logger"

	td "github.com/Kanha/Meow"
	"resty.dev/v3"

	"KanhaMusic/config"
	state "KanhaMusic/kanha/core/models"
	"KanhaMusic/kanha/utils"
)

type reg struct {
	mu     sync.RWMutex
	sorted []state.Platform
	byName map[state.PlatformName]state.Platform
}

var (
	global = &reg{
		byName: make(map[state.PlatformName]state.Platform),
	}
	rc = resty.New().SetTimeout(20 * time.Second)
)

func Register(p state.Platform) {
	global.mu.Lock()
	defer global.mu.Unlock()

	global.byName[p.Name()] = p
	global.sorted = append(global.sorted, p)
	sort.Slice(global.sorted, func(i, j int) bool {
		return global.sorted[i].Priority() > global.sorted[j].Priority()
	})
}

func GetPlatform(name state.PlatformName) (state.Platform, bool) {
	global.mu.RLock()
	defer global.mu.RUnlock()
	p, ok := global.byName[name]
	return p, ok
}

func ordered() []state.Platform {
	global.mu.RLock()
	defer global.mu.RUnlock()
	out := make([]state.Platform, len(global.sorted))
	copy(out, global.sorted)
	return out
}

func findFor(query string) state.Platform {
	for _, p := range ordered() {
		if p.CanGet(query) {
			return p
		}
	}
	return nil
}

func GetTracks(c *td.Client, m *td.Message, video bool) ([]*state.Track, error) {
	logger.Debug("GetTracks | video:" + strconv.FormatBool(video))

	if urls, _ := utils.ExtractURLs(c, m); len(urls) > 0 {
		tracks, errs := fetchFromURLs(urls, video)
		if len(tracks) > 0 {
			return tracks, nil
		}
		if !hasPlayableReply(c, m) {
			return nil, combineErrs("no supported platform for given URL(s)", errs)
		}
	}

	if q := m.Args(); q != "" {
		tracks, err := searchQuery(q, video)
		if err != nil {
			return nil, err
		}
		if len(tracks) > 0 {
			return tracks, nil
		}
		return nil, fmt.Errorf("no tracks found for query: %s", q)
	}

	if m.ReplyToMessageID() > 0 {
		return fromReply(c, m)
	}

	return nil, errors.New("no tracks found")
}

const minPlayableAudioBytes = 32 * 1024 // 32 KB minimum valid audio/video file

// AudioProbeMetadata contains extracted audio stream properties from ffprobe.
type AudioProbeMetadata struct {
	CodecName string
	Duration  float64
	Channels  int
	Title     string
	Artist    string
	ISRC      string
}

// ProbeAudioStream extracts audio metadata and stream information using ffprobe.
func ProbeAudioStream(path string) (*AudioProbeMetadata, error) {
	if ffprobePath, err := exec.LookPath("ffprobe"); err == nil && ffprobePath != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, ffprobePath,
			"-v", "quiet",
			"-print_format", "json",
			"-show_format",
			"-show_streams",
			path,
		)
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			var probeData struct {
				Streams []struct {
					CodecType string `json:"codec_type"`
					CodecName string `json:"codec_name"`
					Channels  int    `json:"channels"`
					Duration  string `json:"duration"`
				} `json:"streams"`
				Format struct {
					Duration string            `json:"duration"`
					Size     string            `json:"size"`
					Tags     map[string]string `json:"tags"`
				} `json:"format"`
			}
			if err := json.Unmarshal(out, &probeData); err == nil {
				res := &AudioProbeMetadata{}
				for _, st := range probeData.Streams {
					if st.CodecType == "audio" {
						res.CodecName = st.CodecName
						res.Channels = st.Channels
						if d, err := strconv.ParseFloat(st.Duration, 64); err == nil && d > 0 {
							res.Duration = d
						}
						break
					}
				}
				if res.Duration == 0 {
					if d, err := strconv.ParseFloat(probeData.Format.Duration, 64); err == nil && d > 0 {
						res.Duration = d
					}
				}
				if probeData.Format.Tags != nil {
					for k, v := range probeData.Format.Tags {
						kLower := strings.ToLower(k)
						if kLower == "title" {
							res.Title = v
						} else if kLower == "artist" {
							res.Artist = v
						} else if kLower == "isrc" {
							res.ISRC = v
						}
					}
				}
				if res.CodecName != "" {
					return res, nil
				}
			}
		}
	}
	return nil, errors.New("ffprobe unavailable or failed to parse audio stream")
}

// ValidateTrackAudio checks if the downloaded file exists, is non-empty, has reasonable size,
// does not contain text/HTML error responses, has a valid audio stream via ffprobe,
// checks plausible duration against track metadata, and decodes successfully via ffmpeg.
func ValidateTrackAudio(path string, track *state.Track) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("empty file path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("media file not found: %w", err)
	}
	if info.IsDir() {
		return errors.New("media file path points to a directory")
	}
	if info.Size() < minPlayableAudioBytes {
		return fmt.Errorf("media file too small or empty (%d bytes, min required %d)", info.Size(), minPlayableAudioBytes)
	}

	// Check initial bytes for HTML/JSON error payloads (e.g. 403 Forbidden, Cloudflare, API errors)
	headerBuf := make([]byte, 1024)
	f, err := os.Open(path)
	if err == nil {
		n, _ := f.Read(headerBuf)
		_ = f.Close()
		if n > 0 {
			lowerHeader := strings.ToLower(string(headerBuf[:n]))
			errorSignatures := []string{
				"<html",
				"<!doctype",
				"{\"error\"",
				"{\"message\"",
				"{\"code\":",
				"{\"status\":",
				"\"status\":\"error\"",
				"access denied",
				"404 not found",
				"403 forbidden",
				"internal server error",
				"bad gateway",
				"service unavailable",
			}
			for _, sig := range errorSignatures {
				if strings.Contains(lowerHeader, sig) {
					return fmt.Errorf("file contains textual/HTML error payload instead of valid audio: %s", sig)
				}
			}
		}
	}

	// Allow skipping external codec probing via environment variable (e.g. in minimal unit test mocks)
	if os.Getenv("SKIP_AUDIO_CODEC_PROBE") == "1" {
		return nil
	}

	// Real audio-stream probing with ffprobe if installed
	var probedDuration float64
	probeMeta, probeErr := ProbeAudioStream(path)
	if probeErr == nil && probeMeta != nil {
		probedDuration = probeMeta.Duration

		// Verify Plausible Duration
		if track != nil && track.Duration > 20 && probedDuration > 0 {
			expectedDur := float64(track.Duration)
			diff := math.Abs(probedDuration - expectedDur)
			maxTol := 30.0
			if pctTol := expectedDur * 0.20; pctTol > maxTol {
				maxTol = pctTol
			}
			if diff > maxTol {
				return fmt.Errorf("duration mismatch: expected ~%ds, probed %.1fs (diff %.1fs > tol %.1fs)",
					track.Duration, probedDuration, diff, maxTol)
			}
		}

		// Verify Version Tag if available from ID3 tags
		if probeMeta.Title != "" && track != nil && track.Title != "" {
			probedVer := state.ClassifyTrack(probeMeta.Title).Version
			trackVer := state.ClassifyTrack(track.Title).Version
			if probedVer != "" && trackVer != "" && probedVer != trackVer {
				return fmt.Errorf("version mismatch: expected %s, probed audio tag indicates %s (%q)",
					trackVer, probedVer, probeMeta.Title)
			}
		}
	} else if ffprobePath, err := exec.LookPath("ffprobe"); err == nil && ffprobePath != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, ffprobePath,
			"-v", "error",
			"-select_streams", "a:0",
			"-show_entries", "stream=codec_name",
			"-of", "default=noprint_wrappers=1:nokey=1",
			path,
		)
		out, err := cmd.Output()
		if err != nil || len(strings.TrimSpace(string(out))) == 0 {
			return fmt.Errorf("ffprobe audio stream validation failed: %v", err)
		}
	}

	// Real audio decode test with ffmpeg if installed (catches corrupt frames and bad headers)
	if ffmpegPath, err := exec.LookPath("ffmpeg"); err == nil && ffmpegPath != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, ffmpegPath,
			"-v", "error",
			"-xerror",
			"-i", path,
			"-t", "1.5",
			"-f", "null",
			"-",
		)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			errMsg := strings.TrimSpace(stderr.String())
			if errMsg != "" {
				return fmt.Errorf("audio decoding test failed: %s (%w)", errMsg, err)
			}
			return fmt.Errorf("audio decoding test failed: %w", err)
		}
	}

	return nil
}

// ValidateAudioFile maintains backward-compatible validation for general audio files.
func ValidateAudioFile(path string) error {
	return ValidateTrackAudio(path, nil)
}

// Download fetches the given track and returns its local file path.
// It checks verified Telegram audio cache, tries registered platforms in priority order,
// validates downloaded audio streams, rejects corrupt/mismatched audio, and falls back cleanly.
func Download(
	ctx context.Context,
	track *state.Track,
	msg *td.Message,
) (string, error) {
	if track == nil {
		return "", errors.New("nil track provided for download")
	}

	// 1. Check verified Telegram Audio Cache
	if cacheEntry, reason, hit := GetTelegramAudioCache().MatchTrack(track); hit && cacheEntry != nil {
		track.CachedFileID = cacheEntry.TelegramFileID
		track.FileUniqueID = cacheEntry.TelegramFileUniqueID
		track.CacheMatchReason = reason

		logger.Infof("[Diagnostic] Query=%q | SelectedProvider=TelegramCache | ProviderTrackID=%s | ReturnedTitle=%q | ReturnedArtist=%q | TelegramFileUniqueID=%s | CacheMatchReason=%s",
			track.Title, track.ID, cacheEntry.Title, cacheEntry.Artist, cacheEntry.TelegramFileUniqueID, reason)

		cachedLocalPath := getPath(&state.Track{ID: cacheEntry.TelegramFileID, Video: track.Video}, ".mp3")
		if fileExists(cachedLocalPath) {
			if err := ValidateTrackAudio(cachedLocalPath, track); err == nil {
				logger.Infof("[Diagnostic] Cache match validated from disk: TrackID=%s | Path=%s", track.ID, cachedLocalPath)
				return cachedLocalPath, nil
			}
			_ = os.Remove(cachedLocalPath)
		}
	} else {
		track.CacheMatchReason = reason
		logger.Debugf("[Diagnostic] Query=%q | TrackID=%s | CacheMatchReason=%s", track.Title, track.ID, reason)
	}

	var errs []string
	const perProviderTimeout = 25 * time.Second

	for _, p := range ordered() {
		if !p.CanDownload(track.Source) {
			continue
		}

		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		pCtx, pCancel := context.WithTimeout(ctx, perProviderTimeout)
		logger.Infof("[Diagnostic] Query=%q | SelectedProvider=%s | ProviderTrackID=%s | ReturnedTitle=%q | ReturnedArtist=%q | TelegramFileUniqueID=%s | CacheMatchReason=%s",
			track.Title, p.Name(), track.ID, track.Title, track.Artist, track.FileUniqueID, track.CacheMatchReason)

		path, err := p.Download(pCtx, track, msg)
		pCancel()

		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			logger.Warnf("[Diagnostic] Provider %s failed for %q (ID=%s): %v", p.Name(), track.Title, track.ID, err)
			errs = append(errs, string(p.Name())+": "+err.Error())
			continue
		}

		// Validate downloaded file
		if valErr := ValidateTrackAudio(path, track); valErr != nil {
			fileSize := int64(0)
			if fi, sErr := os.Stat(path); sErr == nil {
				fileSize = fi.Size()
			}
			logger.Warnf("[Diagnostic] Validation Result: FAIL (%v) for provider %s | TrackID=%s | ActualSize=%d | Action: Falling back to next provider",
				valErr, p.Name(), track.ID, fileSize)

			_ = os.Remove(path)
			errs = append(errs, string(p.Name())+": "+valErr.Error())
			continue
		}

		// Validation PASSED
		fileSize := int64(0)
		probedDur := float64(track.Duration)
		if fi, sErr := os.Stat(path); sErr == nil {
			fileSize = fi.Size()
		}
		if meta, pErr := ProbeAudioStream(path); pErr == nil && meta.Duration > 0 {
			probedDur = meta.Duration
		}

		logger.Infof("[Diagnostic] Validation Result: PASS for provider %s | TrackID=%s | Title=%q | ActualSize=%d | ActualDuration=%.1fs | FinalPlaybackTrackID=%s",
			p.Name(), track.ID, track.Title, fileSize, probedDur, track.ID)

		return path, nil
	}

	if len(errs) > 0 {
		return "", combineErrs("download failed on all eligible providers", errs)
	}
	return "", errors.New("no downloader for source: " + string(track.Source))
}

func fetchFromURLs(urls []string, video bool) ([]*state.Track, []string) {
	var tracks []*state.Track
	var errs []string

	for _, u := range urls {
		p := findFor(u)
		if p == nil {
			errs = append(errs, "no platform for: "+u)
			continue
		}

		logger.Debug("URL matched " + string(p.Name()) + ": " + u)
		got, err := p.Get(u, video)
		if err != nil {
			if strings.Contains(err.Error(), "failed to extract metadata") {
				continue
			}
			errs = append(errs, string(p.Name())+": "+err.Error())
			continue
		}
		tracks = append(tracks, got...)
	}

	return tracks, errs
}

// AutoplayTracks fetches non-repeating recommended tracks similar to the last played track.
// It uses multi-tiered recommendations (YouTube mix playlist, related watch-next videos, and search fallback).
// It verifies candidates against the room's playback history to prevent repeating songs.
func AutoplayTracks(last *state.Track, limit int, history ...string) ([]*state.Track, error) {
	if last == nil {
		return nil, errors.New("cannot resolve autoplay for nil track")
	}

	ytRaw, ok := GetPlatform(PlatformYouTube)
	if !ok {
		return nil, errors.New("youtube platform not registered")
	}
	yt := ytRaw.(*YouTubePlatform)

	targetID := last.ID
	if last.Source != PlatformYouTube || targetID == "" {
		// Non-YouTube track: resolve equivalent YouTube track first using title
		tracks, err := yt.VideoSearch(last.Title, true)
		if err == nil && len(tracks) > 0 {
			targetID = tracks[0].ID
		}
	}

	fetchLimit := limit * 3
	if fetchLimit < 15 {
		fetchLimit = 15
	}

	candidates, err := yt.AutoplayCandidates(targetID, last.Title, fetchLimit)
	if err != nil || len(candidates) == 0 {
		return nil, errors.New("failed to fetch autoplay candidates")
	}

	// Filter candidates against history ring buffer and current track
	historyMap := make(map[string]bool)
	for _, h := range history {
		historyMap[h] = true
	}

	var filtered []*state.Track
	for _, t := range candidates {
		if t == nil || t.ID == "" || t.ID == last.ID {
			continue
		}
		if historyMap["id:"+t.ID] {
			continue
		}
		norm := state.NormalizeTrackTitle(t.Title)
		if norm != "" && historyMap["title:"+norm] {
			continue
		}
		filtered = append(filtered, t)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}

	// If everything was filtered out by strict history, fall back to candidates differing from last.ID
	if len(filtered) == 0 {
		for _, t := range candidates {
			if t != nil && t.ID != "" && t.ID != last.ID {
				filtered = append(filtered, t)
				if limit > 0 && len(filtered) >= limit {
					break
				}
			}
		}
	}

	return withVideo(filtered, last.Video), nil
}

// ResolveQuery resolves a plain URL or search query string to track(s),
// without requiring a Telegram message. Used by features that store a raw
// query/URL (e.g. playlist management) rather than a live message.
func ResolveQuery(query string, video bool) ([]*state.Track, error) {
	if p := findFor(query); p != nil {
		if got, err := p.Get(query, video); err == nil && len(got) > 0 {
			return got, nil
		}
	}
	return searchQuery(query, video)
}

func searchQuery(q string, video bool) ([]*state.Track, error) {
	if p := findFor(q); p != nil && p.Name() != PlatformYouTube {
		if got, err := p.Get(q, video); err == nil && len(got) > 0 {
			return got, nil
		}
	}

	yt, ok := GetPlatform(PlatformYouTube)
	if !ok {
		return nil, errors.New("youtube platform not registered")
	}

	tracks, err := yt.Get(q, video)
	if err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return nil, nil
	}
	return []*state.Track{tracks[0]}, nil
}

func fromReply(c *td.Client, m *td.Message) ([]*state.Track, error) {
	target, isVideo, _ := playableMedia(c, m)
	if target == nil {
		return nil, errors.New("⚠️ Reply with a valid media (audio/video)")
	}

	tgp, ok := GetPlatform(PlatformTelegram)
	if !ok {
		return nil, errors.New("telegram platform not registered")
	}

	track, err := tgp.(*TelegramPlatform).GetTracksByMessage(c, target)
	if err != nil {
		return nil, err
	}
	track.Video = isVideo
	return []*state.Track{track}, nil
}

func hasPlayableReply(c *td.Client, m *td.Message) bool {
	if m.ReplyToMessageID() <= 0 {
		return false
	}
	msg, _, _ := playableMedia(c, m)
	return msg != nil
}

func combineErrs(prefix string, errs []string) error {
	if len(errs) == 0 {
		return errors.New(prefix)
	}
	return errors.New(prefix + "\n• " + strings.Join(errs, "\n• "))
}

func GetYouTubeMixPlaylist(_ context.Context, playlistID string) ([]*state.Track, error) {
	ytRaw, ok := GetPlatform(PlatformYouTube)
	if !ok {
		return nil, errors.New("youtube platform not registered")
	}
	yt := ytRaw.(*YouTubePlatform)
	limit := config.QueueLimit
	if limit <= 0 {
		limit = 15
	}
	return yt.fetchMixPlaylist(playlistID, limit)
}

func SearchTracks(query string, video bool) ([]*state.Track, error) {
	ytRaw, ok := GetPlatform(PlatformYouTube)
	if !ok {
		return nil, errors.New("youtube platform not registered")
	}
	yt := ytRaw.(*YouTubePlatform)
	return yt.VideoSearch(query)
}
