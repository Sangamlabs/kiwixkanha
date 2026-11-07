/*
 * ● Kiwi Music (KanhaMusic fork)
 * ○ Telegram Audio Cache & Strict Identity Verification Engine
 */

package platforms

import (
	"strings"
	"sync"
	"time"

	state "KanhaMusic/kanha/core/models"
	"KanhaMusic/kanha/logger"
)

// TelegramCacheEntry models a verified Telegram-hosted audio file linked to canonical source metadata.
type TelegramCacheEntry struct {
	ProviderTrackID      string             `json:"provider_track_id"` // e.g. YouTube video ID "lZ2PhyBF3GQ"
	Source               state.PlatformName `json:"source"`            // e.g. PlatformYouTube, PlatformSpotify
	ISRC                 string             `json:"isrc"`              // International Standard Recording Code
	Title                string             `json:"title"`
	Artist               string             `json:"artist"`
	NormalizedTitle      string             `json:"normalized_title"`
	NormalizedArtist     string             `json:"normalized_artist"`
	Version              state.VersionType  `json:"version"`
	Duration             int                `json:"duration"`         // in seconds
	TelegramFileID       string             `json:"telegram_file_id"` // remote file ID (e.g. CQACAg...)
	TelegramFileUniqueID string             `json:"telegram_file_unique_id"`
	FileSize             int64              `json:"file_size"`
	CreatedAt            time.Time          `json:"created_at"`
}

type TelegramAudioCache struct {
	mu           sync.RWMutex
	byProviderID map[string]*TelegramCacheEntry // key: source + ":" + providerTrackID
	byISRC       map[string]*TelegramCacheEntry // key: isrc
	byFileID     map[string]*TelegramCacheEntry // key: telegram file ID
	byKey        map[string]*TelegramCacheEntry // key: CanonicalRecordingKey
}

var (
	globalTelegramCache     *TelegramAudioCache
	globalTelegramCacheOnce sync.Once
)

// GetTelegramAudioCache returns the singleton Telegram audio cache.
func GetTelegramAudioCache() *TelegramAudioCache {
	globalTelegramCacheOnce.Do(func() {
		globalTelegramCache = &TelegramAudioCache{
			byProviderID: make(map[string]*TelegramCacheEntry),
			byISRC:       make(map[string]*TelegramCacheEntry),
			byFileID:     make(map[string]*TelegramCacheEntry),
			byKey:        make(map[string]*TelegramCacheEntry),
		}
	})
	return globalTelegramCache
}

// Store records a verified Telegram audio file reference along with its exact source metadata.
func (c *TelegramAudioCache) Store(entry *TelegramCacheEntry) {
	if entry == nil || entry.TelegramFileID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if entry.Version == "" {
		entry.Version = state.ClassifyTrack(entry.Title).Version
	}
	if entry.NormalizedTitle == "" {
		entry.NormalizedTitle = state.NormalizeTrackTitle(entry.Title)
	}
	if entry.NormalizedArtist == "" && entry.Artist != "" {
		entry.NormalizedArtist = state.NormalizeArtist(entry.Artist)
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	// 1. Index by Source + ProviderTrackID
	if entry.ProviderTrackID != "" && entry.Source != "" {
		c.byProviderID[string(entry.Source)+":"+entry.ProviderTrackID] = entry
	}

	// 2. Index by ISRC
	if entry.ISRC != "" {
		c.byISRC[strings.ToUpper(strings.TrimSpace(entry.ISRC))] = entry
	}

	// 3. Index by Telegram File ID
	c.byFileID[entry.TelegramFileID] = entry

	// 4. Index by Canonical Recording Key
	recKey := state.CanonicalRecordingKey(entry.Title, entry.Artist)
	c.byKey[recKey] = entry

	logger.Debugf("[TelegramCache] Stored entry: TrackID=%s | FileID=%s | Title=%q | Version=%s | Key=%s",
		entry.ProviderTrackID, entry.TelegramFileID, entry.Title, entry.Version, recKey)
}

// MatchTrack searches for a verified Telegram audio cache hit.
// It enforces strict identity:
// - Never selects a cached audio file using title similarity alone.
// - Prefers exact provider track IDs and ISRC.
// - Strictly separates original, remix, slowed, sped-up, and live versions.
// - Requires plausible duration match.
// - Returns (entry, reason, true) on exact match, or (nil, "cache_miss", false).
func (c *TelegramAudioCache) MatchTrack(track *state.Track) (*TelegramCacheEntry, string, bool) {
	if track == nil {
		return nil, "cache_miss_nil_track", false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	targetVersion := state.ClassifyTrack(track.Title).Version

	// 1. Priority 1: Exact Provider Track ID Match
	if track.ID != "" && track.Source != "" {
		pidKey := string(track.Source) + ":" + track.ID
		if entry, ok := c.byProviderID[pidKey]; ok && entry != nil {
			// Verify version compatibility
			if entry.Version != "" && targetVersion != "" && entry.Version != targetVersion {
				logger.Warnf("[TelegramCache] Provider ID matched %s but version mismatch (cached=%s, requested=%s) — rejecting",
					track.ID, entry.Version, targetVersion)
				return nil, "rejected_version_mismatch", false
			}
			return entry, "exact_provider_track_id", true
		}
	}

	// 2. Priority 2: Exact ISRC Match
	if track.ISRC != "" {
		isrcKey := strings.ToUpper(strings.TrimSpace(track.ISRC))
		if entry, ok := c.byISRC[isrcKey]; ok && entry != nil {
			if entry.Version != "" && targetVersion != "" && entry.Version != targetVersion {
				return nil, "rejected_version_mismatch", false
			}
			return entry, "exact_isrc", true
		}
	}

	// 3. Priority 3: Canonical Recording Key Match (Normalized Title + Normalized Artist + Version)
	recKey := state.CanonicalRecordingKey(track.Title, track.Artist)
	if entry, ok := c.byKey[recKey]; ok && entry != nil {
		// Strict Version Verification
		if entry.Version != targetVersion {
			logger.Warnf("[TelegramCache] Version mismatch for %q: entry=%s, requested=%s — treating as cache miss",
				track.Title, entry.Version, targetVersion)
			return nil, "rejected_version_mismatch", false
		}

		// Strict Artist Verification: if both specify an artist, they must match
		if track.Artist != "" && entry.Artist != "" {
			normReqArtist := state.NormalizeArtist(track.Artist)
			if entry.NormalizedArtist != "" && normReqArtist != "" && entry.NormalizedArtist != normReqArtist {
				logger.Warnf("[TelegramCache] Artist mismatch for %q: entry=%q, requested=%q — treating as cache miss",
					track.Title, entry.Artist, track.Artist)
				return nil, "rejected_artist_mismatch", false
			}
		}

		// Strict Duration Tolerance: must be within 6 seconds or 5%
		if track.Duration > 0 && entry.Duration > 0 {
			diff := entry.Duration - track.Duration
			if diff < 0 {
				diff = -diff
			}
			maxTol := 6
			if pctTol := track.Duration / 20; pctTol > maxTol {
				maxTol = pctTol
			}
			if diff > maxTol {
				logger.Warnf("[TelegramCache] Duration mismatch for %q: entry=%ds, requested=%ds (diff %ds > tol %ds) — treating as cache miss",
					track.Title, entry.Duration, track.Duration, diff, maxTol)
				return nil, "rejected_duration_mismatch", false
			}
		}

		return entry, "strict_metadata_verified", true
	}

	// 4. Title similarity alone is explicitly rejected
	return nil, "cache_miss", false
}

// GetByFileID retrieves a cache entry by its Telegram remote file ID.
func (c *TelegramAudioCache) GetByFileID(fileID string) (*TelegramCacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.byFileID[fileID]
	return entry, ok
}

// Invalidate removes a track from the cache by its provider ID.
func (c *TelegramAudioCache) Invalidate(source state.PlatformName, providerTrackID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	pidKey := string(source) + ":" + providerTrackID
	if entry, ok := c.byProviderID[pidKey]; ok {
		delete(c.byProviderID, pidKey)
		if entry.ISRC != "" {
			delete(c.byISRC, strings.ToUpper(entry.ISRC))
		}
		delete(c.byFileID, entry.TelegramFileID)
		delete(c.byKey, state.CanonicalRecordingKey(entry.Title, entry.Artist))
	}
}

// Clear flushes all entries in the cache.
func (c *TelegramAudioCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byProviderID = make(map[string]*TelegramCacheEntry)
	c.byISRC = make(map[string]*TelegramCacheEntry)
	c.byFileID = make(map[string]*TelegramCacheEntry)
	c.byKey = make(map[string]*TelegramCacheEntry)
}
