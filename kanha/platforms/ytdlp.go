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
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"KanhaMusic/kanha/logger"

	td "github.com/Kanha/Meow"

	"KanhaMusic/config/cookies"
	state "KanhaMusic/kanha/core/models"
)

const PlatformYtDlp state.PlatformName = "YtDlp"

type YtdlpPlatform struct{}

type ytdlpInfo struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Duration    float64     `json:"duration"`
	Thumbnail   string      `json:"thumbnail"`
	URL         string      `json:"webpage_url"`
	OriginalURL string      `json:"original_url"`
	Uploader    string      `json:"uploader"`
	IsLive      bool        `json:"is_live"`
	Extractor   string      `json:"extractor"`
	Entries     []ytdlpInfo `json:"entries"`
}

var (
	bannedExtractors = map[string]bool{
		"alphaporno": true, "beeg": true, "behindkink": true, "bongacams": true,
		"cam4": true, "cammodels": true, "camsoda": true, "chaturbate": true,
		"drtuber": true, "eporner": true, "erocast": true, "eroprofile": true,
		"fourtube": true, "goshgay": true, "hellporno": true, "iwara": true,
		"lovehomeporn": true, "manyvids": true, "motherless": true, "murrtube": true,
		"nonktube": true, "noodlemagazine": true, "nubilesporn": true, "nuvid": true,
		"oftv": true, "peekvids": true, "pornbox": true, "pornflip": true,
		"pornhub": true, "pornotube": true, "pornovoisines": true, "pornoxo": true,
		"redgifs": true, "redtube": true, "rule34video": true, "sauceplus": true,
		"sexu": true, "slutload": true, "spankbang": true, "stripchat": true,
		"sunporno": true, "thisvid": true, "tnaflix": true, "toypics": true,
		"txxx": true, "xhamster": true, "xnxx": true, "xvideos": true,
		"xxxymovies": true, "youjizz": true, "youporn": true, "zenporn": true,
	}
	// audioOnlyExtractors are sites without video streams; a video request is
	// silently downgraded to audio.
	audioOnlyExtractors = map[string]bool{
		"soundcloud": true,
	}
	ytURLPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(youtube\.com|youtu\.be|music\.youtube\.com)`),
	}
)

func init() {
	Register(&YtdlpPlatform{})
}

func (y *YtdlpPlatform) Name() state.PlatformName { return PlatformYtDlp }
func (y *YtdlpPlatform) Priority() int            { return 60 }

func (y *YtdlpPlatform) CanGet(query string) bool {
	if _, err := sanitizeMediaURL(query); err != nil {
		return false
	}
	parsed, err := url.Parse(query)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Host)
	return host != "t.me" &&
		host != "telegram.me" &&
		host != "telegram.dog" &&
		!strings.HasSuffix(host, ".t.me")
}

func (y *YtdlpPlatform) Get(query string, video bool) ([]*state.Track, error) {
	safeURL, err := sanitizeMediaURL(query)
	if err != nil {
		return nil, errUnsafeURL
	}

	info, err := y.extractMetadata(safeURL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	if info.IsLive {
		return nil, errors.New("live streams are not supported")
	}

	if bannedExtractors[strings.ToLower(info.Extractor)] {
		return nil, errors.New("adult content is not allowed")
	}

	var tracks []*state.Track
	if len(info.Entries) > 0 {
		for _, entry := range info.Entries {
			if entry.IsLive || bannedExtractors[strings.ToLower(entry.Extractor)] {
				continue
			}
			tracks = append(tracks, y.toTrack(&entry, video))
		}
	} else {
		tracks = []*state.Track{y.toTrack(info, video)}
	}

	return tracks, nil
}

func (y *YtdlpPlatform) CanDownload(source state.PlatformName) bool {
	return source == PlatformYtDlp || source == PlatformYouTube
}

func (y *YtdlpPlatform) Download(
	ctx context.Context,
	track *state.Track,
	_ *td.Message,
) (string, error) {
	if f := findFile(track); f != "" {
		logger.Debug("YtDlp: cache hit " + f)
		return f, nil
	}

	safeURL, err := sanitizeMediaURL(track.URL)
	if err != nil {
		return "", errUnsafeURL
	}

	args := []string{
		"--no-playlist",
		"--no-part",
		"--geo-bypass",
		"--no-warnings",
		"--ignore-errors",
		"--no-check-certificate",
		"-q",
		"-o", getPath(track, ".%(ext)s"),
	}

	if track.Video {
		args = append(args,
			"-f", "(b[height>=360][height<=1080]/bv*[height>=360][height<=1080]/bv*)+(ba[abr>=180][abr<=360]/ba)/b",
		)
	} else {
		args = append(args,
			"-f", "ba[abr>=180][abr<=360]/ba",
			"-x",
			"--concurrent-fragments", "4",
		)
	}

	var usedCookie string
	if y.isYouTubeURL(track.URL) || track.Source == PlatformYouTube {
		if cookiePath := cookies.GetCookiePath(); cookiePath != "" {
			usedCookie = cookiePath
			args = append(args, "--cookies", cookiePath)
		}
	}

	args = append(args, "--", safeURL)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		findAndRemove(track)
		if isDownloadCancelled(err) {
			return "", err
		}
		errMsg := sanitizeYtDlpOutput(strings.TrimSpace(stderr.String()))
		if usedCookie != "" {
			logger.Warnf("[YouTube] yt-dlp download failed with cookies (%s); attempting fallback without cookies", errMsg)
			noCookieArgs := filterArgs(args, "--cookies", usedCookie)
			var stdoutRetry, stderrRetry bytes.Buffer
			retryCmd := exec.CommandContext(ctx, "yt-dlp", noCookieArgs...)
			retryCmd.Stdout = &stdoutRetry
			retryCmd.Stderr = &stderrRetry
			if rErr := retryCmd.Run(); rErr == nil {
				if p := findFile(track); p != "" {
					logger.Infof("YtDlp: downloaded %s (fallback without cookies succeeded)", p)
					return p, nil
				}
			} else {
				retryErr := sanitizeYtDlpOutput(strings.TrimSpace(stderrRetry.String()))
				logger.Warnf("[YouTube] yt-dlp fallback without cookies also failed: %s", retryErr)
			}
		}
		return "", fmt.Errorf(
			"yt-dlp failed: %w\nstdout: %s\nstderr: %s",
			err, sanitizeYtDlpOutput(strings.TrimSpace(stdout.String())), errMsg,
		)
	}

	p := findFile(track)
	if p == "" {
		return "", errors.New("yt-dlp produced no output file")
	}

	logger.Infof("YtDlp: downloaded %s", p)
	return p, nil
}

func (y *YtdlpPlatform) extractMetadata(urlStr string) (*ytdlpInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	args := []string{
		"-j", "--flat-playlist",
		"--no-warnings", "--no-check-certificate",
	}

	var usedCookie string
	if y.isYouTubeURL(urlStr) {
		if cookiePath := cookies.GetCookiePath(); cookiePath != "" {
			usedCookie = cookiePath
			args = append(args, "--cookies", cookiePath)
		}
	}

	args = append(args, "--", urlStr)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := sanitizeYtDlpOutput(strings.TrimSpace(stderr.String()))
		if usedCookie != "" {
			logger.Warnf("[YouTube] yt-dlp metadata extraction failed with cookies (%s); attempting fallback without cookies", errMsg)
			noCookieArgs := filterArgs(args, "--cookies", usedCookie)
			var stdoutRetry, stderrRetry bytes.Buffer
			retryCmd := exec.CommandContext(ctx, "yt-dlp", noCookieArgs...)
			retryCmd.Stdout = &stdoutRetry
			retryCmd.Stderr = &stderrRetry
			if rErr := retryCmd.Run(); rErr == nil {
				return y.parseMetadataOutput(stdoutRetry.Bytes())
			}
		}
		return nil, fmt.Errorf("metadata extraction failed: %w\n%s", err, errMsg)
	}

	return y.parseMetadataOutput(stdout.Bytes())
}

var tokenSanitizerRe = regexp.MustCompile(`(?i)(cookie|token|auth|session|key)[=:\s]+[A-Za-z0-9_\-\.\%=]{16,}`)

func sanitizeYtDlpOutput(s string) string {
	if s == "" {
		return ""
	}
	return tokenSanitizerRe.ReplaceAllString(s, "$1=***REDACTED***")
}

func (y *YtdlpPlatform) parseMetadataOutput(data []byte) (*ytdlpInfo, error) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")

	if len(lines) > 1 {
		var info ytdlpInfo
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var entry ytdlpInfo
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				logger.Debugf("YtDlp: skip bad entry: %v", err)
				continue
			}
			info.Entries = append(info.Entries, entry)
		}
		if len(info.Entries) == 0 {
			return nil, errors.New("no valid entries in playlist or search results")
		}
		return &info, nil
	}

	var info ytdlpInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("failed to parse metadata JSON: %w", err)
	}
	return &info, nil
}

// SearchTracks executes a yt-dlp search query using the configured cookies.
func (y *YtdlpPlatform) SearchTracks(query string, limit int, video bool) ([]*state.Track, error) {
	if limit <= 0 {
		limit = 10
	}
	searchURL := fmt.Sprintf("ytsearch%d:%s", limit, query)
	info, err := y.extractMetadata(searchURL)
	if err != nil {
		return nil, err
	}

	var tracks []*state.Track
	if len(info.Entries) > 0 {
		for _, entry := range info.Entries {
			if entry.IsLive || bannedExtractors[strings.ToLower(entry.Extractor)] {
				continue
			}
			t := y.toTrack(&entry, video)
			t.Source = PlatformYouTube
			tracks = append(tracks, t)
		}
	} else if info.ID != "" {
		t := y.toTrack(info, video)
		t.Source = PlatformYouTube
		tracks = append(tracks, t)
	}
	return tracks, nil
}

func filterArgs(args []string, dropFlag, dropVal string) []string {
	out := make([]string, 0, len(args))
	skipNext := false
	for _, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if a == dropFlag {
			skipNext = true
			continue
		}
		if strings.HasPrefix(a, dropFlag+"=") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func (y *YtdlpPlatform) toTrack(info *ytdlpInfo, video bool) *state.Track {
	if video && audioOnlyExtractors[strings.ToLower(info.Extractor)] {
		video = false
	}
	return &state.Track{
		ID:       info.ID,
		Title:    info.Title,
		Duration: int(info.Duration),
		Artwork:  info.Thumbnail,
		URL:      firstNonEmpty(info.OriginalURL, info.URL),
		Source:   PlatformYtDlp,
		Video:    video,
	}
}

func (y *YtdlpPlatform) isYouTubeURL(urlStr string) bool {
	lower := strings.ToLower(urlStr)
	if strings.HasPrefix(lower, "ytsearch") {
		return true
	}
	for _, p := range ytURLPatterns {
		if p.MatchString(urlStr) {
			return true
		}
	}
	return false
}
