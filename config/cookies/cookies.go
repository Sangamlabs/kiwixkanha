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

package cookies

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"KanhaMusic/kanha/logger"

	"resty.dev/v3"

	"KanhaMusic/config"
)

const (
	cookieDir = "config/cookies"
	cacheTTL  = 10 * time.Second
)

// CookieFileInfo contains validated metadata about a Netscape cookie file without exposing its tokens.
type CookieFileInfo struct {
	Path         string
	Size         int64
	ModTime      time.Time
	TotalCookies int
	ExpiredCount int
	IsValid      bool
}

type cookieManager struct {
	mu           sync.RWMutex
	lastChecked  time.Time
	cachedResult string
	cachedInfo   *CookieFileInfo
}

var (
	mgr          = &cookieManager{}
	client       = resty.New().SetTimeout(30 * time.Second)
	b64Mu        sync.Mutex
	lastB64Value string
)

func init() {
	if _, err := os.Stat("go.mod"); err == nil {
		_ = os.MkdirAll("config/cookie", 0755)
		_ = os.MkdirAll(cookieDir, 0755)
	}

	logger.Debug("🔹 Initializing cookies...")

	// Initialize Base64 cookies if provided
	InitBase64Cookies()

	if config.CookiesLink != "" {
		urls := strings.FieldsSeq(config.CookiesLink)
		for url := range urls {
			if err := downloadCookieFile(url); err != nil {
				logger.Warnf(
					"Failed to download cookie file from %s: %v",
					url,
					err,
				)
			}
		}
	}
}

// InitBase64Cookies inspects the COOKIES_B64 environment variable and decodes it if present.
func InitBase64Cookies() {
	b64 := strings.TrimSpace(os.Getenv("COOKIES_B64"))
	if b64 == "" {
		b64 = strings.TrimSpace(config.CookiesB64)
	}
	if b64 == "" {
		return
	}

	b64Mu.Lock()
	defer b64Mu.Unlock()
	if b64 == lastB64Value {
		return
	}

	target := strings.TrimSpace(config.YouTubeCookiesFile)
	if target == "" {
		target = filepath.Join("config", "cookie", "cookie.txt")
	}

	if err := DecodeAndSaveCookiesB64(b64, target); err == nil {
		lastB64Value = b64
	}
}

// ResetBase64CacheForTesting clears in-memory Base64 and manager caches during tests.
func ResetBase64CacheForTesting() {
	b64Mu.Lock()
	lastB64Value = ""
	b64Mu.Unlock()
	mgr.mu.Lock()
	mgr.lastChecked = time.Time{}
	mgr.cachedResult = ""
	mgr.cachedInfo = nil
	mgr.mu.Unlock()
}

// decodeBase64CookieData handles standard, unpadded, and URL-safe Base64 encodings with newline stripping.
func decodeBase64CookieData(raw string) ([]byte, error) {
	clean := strings.TrimSpace(raw)
	clean = strings.ReplaceAll(clean, "\r", "")
	clean = strings.ReplaceAll(clean, "\n", "")
	clean = strings.ReplaceAll(clean, " ", "")

	if clean == "" {
		return nil, errors.New("empty base64 string")
	}

	// 1. Standard encoding
	if data, err := base64.StdEncoding.DecodeString(clean); err == nil {
		return data, nil
	}

	// 2. Raw standard encoding (no padding)
	if data, err := base64.RawStdEncoding.DecodeString(clean); err == nil {
		return data, nil
	}

	// 3. URL-safe encoding
	if data, err := base64.URLEncoding.DecodeString(clean); err == nil {
		return data, nil
	}

	// 4. Raw URL-safe encoding
	if data, err := base64.RawURLEncoding.DecodeString(clean); err == nil {
		return data, nil
	}

	return nil, errors.New("invalid base64 encoding")
}

// DecodeAndSaveCookiesB64 decodes a base64-encoded Netscape cookie string, validates
// the cookie format, and atomically writes it to targetPath with 0600 permissions.
// If b64Str is empty, it returns nil without modifying anything.
// It never logs cookie contents or decoded secrets.
func DecodeAndSaveCookiesB64(b64Str string, targetPath string) error {
	trimmed := strings.TrimSpace(b64Str)
	if trimmed == "" {
		return nil
	}

	data, err := decodeBase64CookieData(trimmed)
	if err != nil {
		logger.Warnf("[YouTube] Failed to decode COOKIES_B64: invalid base64 encoding")
		return fmt.Errorf("invalid base64 encoding: %w", err)
	}

	if targetPath == "" {
		targetPath = strings.TrimSpace(config.YouTubeCookiesFile)
		if targetPath == "" {
			targetPath = filepath.Join("config", "cookie", "cookie.txt")
		}
	}

	// Validate the decoded Netscape cookie format before writing
	cookieInfo, err := ValidateNetscapeCookieBytes(data, targetPath)
	if err != nil {
		logger.Warnf("[YouTube] COOKIES_B64 data failed Netscape validation: %v", err)
		return fmt.Errorf("cookie data failed Netscape validation: %w", err)
	}

	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		logger.Warnf("[YouTube] Failed to create directory %s for cookies: %v", targetDir, err)
		return fmt.Errorf("failed creating cookie directory: %w", err)
	}

	// Atomic write: write to temp file in the same directory, chmod 0600, then rename
	tmpFile, err := os.CreateTemp(targetDir, "cookie-tmp-*.txt")
	if err != nil {
		logger.Warnf("[YouTube] Failed to create temp cookie file in %s: %v", targetDir, err)
		return fmt.Errorf("failed creating temp cookie file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed setting permissions on temp cookie file: %w", err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed writing to temp cookie file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed syncing temp cookie file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed closing temp cookie file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpName, targetPath); err != nil {
		logger.Warnf("[YouTube] Failed to rename temp cookie file to %s: %v", targetPath, err)
		return fmt.Errorf("failed atomically replacing cookie file: %w", err)
	}

	_ = os.Chmod(targetPath, 0600)

	// Invalidate cookie manager cache so new file is recognized immediately
	mgr.mu.Lock()
	mgr.lastChecked = time.Time{}
	mgr.cachedResult = ""
	mgr.cachedInfo = nil
	mgr.mu.Unlock()

	active := cookieInfo.TotalCookies - cookieInfo.ExpiredCount
	logger.Infof("[YouTube] Successfully decoded COOKIES_B64 into %s (%d bytes, %d active cookies)",
		targetPath, cookieInfo.Size, active)
	return nil
}

func downloadCookieFile(url string) error {
	id := filepath.Base(url)
	rawURL := "https://batbin.me/raw/" + id
	filePath := filepath.Join(cookieDir, id+".txt")

	resp, err := client.R().
		SetResponseSaveFileName(filePath).
		Get(rawURL)
	if err != nil {
		return err
	}

	if resp.IsStatusFailure() {
		return fmt.Errorf(
			"unexpected status %d from %s",
			resp.StatusCode(),
			rawURL,
		)
	}

	return nil
}

// ValidateNetscapeCookieFile inspects a file to verify it exists, is non-empty,
// and matches the Netscape HTTP cookie specification without logging tokens or credentials.
func ValidateNetscapeCookieFile(filePath string) (*CookieFileInfo, error) {
	if strings.TrimSpace(filePath) == "" {
		return nil, errors.New("empty cookie file path")
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("cookie file not found: %w", err)
	}
	if info.IsDir() {
		return nil, errors.New("cookie path points to a directory, not a file")
	}
	if info.Size() == 0 {
		return nil, errors.New("cookie file is empty (0 bytes)")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("cookie file cannot be read: %w", err)
	}

	cookieInfo, err := ValidateNetscapeCookieBytes(data, filePath)
	if err != nil {
		return nil, err
	}

	cookieInfo.Size = info.Size()
	cookieInfo.ModTime = info.ModTime()
	return cookieInfo, nil
}

// ValidateNetscapeCookieBytes validates byte content against the Netscape HTTP cookie specification
// without logging tokens or credentials.
func ValidateNetscapeCookieBytes(data []byte, virtualPath string) (*CookieFileInfo, error) {
	if len(data) == 0 {
		return nil, errors.New("cookie data is empty (0 bytes)")
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 256*1024)

	var (
		totalCookies      int
		expiredCount      int
		hasNetscapeHeader bool
		hasValidDomain    bool
		now               = time.Now().Unix()
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "netscape") || strings.Contains(lower, "cookie file") {
				hasNetscapeHeader = true
			}
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}

		totalCookies++
		domain := strings.ToLower(parts[0])
		if strings.Contains(domain, "youtube") || strings.Contains(domain, "google") {
			hasValidDomain = true
		}

		// Field index 4 is expiration timestamp in seconds
		if exp, err := strconv.ParseInt(parts[4], 10, 64); err == nil && exp > 0 {
			if exp < now {
				expiredCount++
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed reading cookie data: %w", err)
	}

	if totalCookies == 0 {
		return nil, errors.New("no valid Netscape cookie entries found in file")
	}

	if !hasNetscapeHeader && !hasValidDomain && totalCookies < 2 {
		return nil, errors.New("file does not appear to be a Netscape cookie file")
	}

	return &CookieFileInfo{
		Path:         virtualPath,
		Size:         int64(len(data)),
		ModTime:      time.Now(),
		TotalCookies: totalCookies,
		ExpiredCount: expiredCount,
		IsValid:      true,
	}, nil
}

func candidatePaths() []string {
	var candidates []string
	seen := make(map[string]bool)

	add := func(p string) {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			candidates = append(candidates, p)
		}
	}

	// 1. Configured primary path (defaults to config/cookie/cookie.txt or YOUTUBE_COOKIES_FILE)
	primary := strings.TrimSpace(config.YouTubeCookiesFile)
	if primary != "" {
		add(primary)
	}

	// 2. Default target location
	defaultPath := filepath.Join("config", "cookie", "cookie.txt")
	add(defaultPath)

	// 3. Fallback locations in config/cookies
	add(filepath.Join(cookieDir, "cookie.txt"))
	add(filepath.Join(cookieDir, "cookies.txt"))

	// 4. Any .txt in config/cookies (excluding example.txt)
	if matches, err := filepath.Glob(filepath.Join(cookieDir, "*.txt")); err == nil {
		for _, m := range matches {
			if filepath.Base(m) == "example.txt" {
				continue
			}
			add(m)
		}
	}

	return candidates
}

// GetCookiePath returns the verified path to the YouTube cookie file, or "" if none is available.
// It never prints cookie contents and caches validation to prevent disk thrashing.
func GetCookiePath() string {
	InitBase64Cookies()

	mgr.mu.RLock()
	if time.Since(mgr.lastChecked) < cacheTTL && mgr.cachedInfo != nil {
		path := mgr.cachedResult
		mgr.mu.RUnlock()
		return path
	}
	mgr.mu.RUnlock()

	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if time.Since(mgr.lastChecked) < cacheTTL && mgr.cachedInfo != nil {
		return mgr.cachedResult
	}

	var expiredFallback *CookieFileInfo
	candidates := candidatePaths()
	for _, cand := range candidates {
		info, err := os.Stat(cand)
		if err != nil || info.IsDir() || info.Size() == 0 {
			continue
		}

		if mgr.cachedInfo != nil && mgr.cachedInfo.Path == cand &&
			mgr.cachedInfo.Size == info.Size() && mgr.cachedInfo.ModTime.Equal(info.ModTime()) {
			mgr.lastChecked = time.Now()
			mgr.cachedResult = cand
			return cand
		}

		cookieInfo, err := ValidateNetscapeCookieFile(cand)
		if err != nil {
			logger.Warnf("[YouTube] Cookie file %s failed validation: %v", cand, err)
			continue
		}

		if cookieInfo.ExpiredCount == cookieInfo.TotalCookies {
			logger.Warnf("[YouTube] Cookie file %s: all %d cookies appear expired; checking for alternatives",
				cand, cookieInfo.TotalCookies)
			if expiredFallback == nil {
				expiredFallback = cookieInfo
			}
			continue
		}

		mgr.cachedInfo = cookieInfo
		mgr.cachedResult = cand
		mgr.lastChecked = time.Now()

		active := cookieInfo.TotalCookies - cookieInfo.ExpiredCount
		logger.Infof("[YouTube] Loaded cookie file: %s (%d bytes, %d active cookies)",
			cand, cookieInfo.Size, active)
		return cand
	}

	if expiredFallback != nil {
		mgr.cachedInfo = expiredFallback
		mgr.cachedResult = expiredFallback.Path
		mgr.lastChecked = time.Now()
		logger.Warnf("[YouTube] Using cookie file with expired cookies as fallback: %s", expiredFallback.Path)
		return expiredFallback.Path
	}

	if mgr.cachedResult != "" || mgr.cachedInfo == nil {
		logger.Debug("[YouTube] No valid cookie file found; proceeding with unauthenticated requests")
	}
	mgr.cachedResult = ""
	mgr.cachedInfo = &CookieFileInfo{IsValid: false}
	mgr.lastChecked = time.Now()
	return ""
}

// GetRandomCookieFile maintains backward compatibility with existing callers.
func GetRandomCookieFile() (string, error) {
	if p := GetCookiePath(); p != "" {
		return p, nil
	}
	return "", nil
}
