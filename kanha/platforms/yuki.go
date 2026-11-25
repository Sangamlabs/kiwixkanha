/*
 * ● KanhaMusic
 * ○ Yuki Music API Provider Platform Implementation
 *
 * Endpoints:
 *   Primary Audio/Video Stream: https://music.yukiapi.site/stream/{VIDEO_ID}?key={KEY}&type=audio|video
 *   Backup Audio/Video Stream:  https://play.yukiapi.site/stream/{VIDEO_ID}?key={KEY}&type=audio|video
 *   YouTube Music Search:       https://music.yukiapi.site/api/search?q={QUERY}&key={KEY}
 *   Track Metadata & Info:      https://music.yukiapi.site/info/{VIDEO_ID}?key={KEY}
 *   Lyrics:                     https://music.yukiapi.site/lyrics?title={TITLE}&artist={ARTIST}&key={KEY}
 */

package platforms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"KanhaMusic/config"
	state "KanhaMusic/kanha/core/models"
	"KanhaMusic/kanha/logger"

	td "github.com/Kanha/Meow"
)

const (
	PlatformYuki state.PlatformName = "Yuki"

	yukiAPITimeout        = 20 * time.Second
	yukiAudioTimeout      = 300 * time.Second
	yukiVideoTimeout      = 600 * time.Second
	yukiMinValidBytes     = 10_000
	yukiMaxJSONBytes      = 2 << 20 // 2 MB
	yukiMaxSearchResults  = 15
	yukiDefaultPrimaryURL = "https://music.yukiapi.site"
	yukiDefaultBackupURL  = "https://play.yukiapi.site"
)

type YukiPlatform struct {
	client *http.Client
}

func init() {
	Register(&YukiPlatform{
		client: &http.Client{
			Timeout: 0, // Request timeout is controlled by context.
		},
	})
}

func (y *YukiPlatform) Name() state.PlatformName { return PlatformYuki }
func (y *YukiPlatform) Priority() int            { return 100 }

func (y *YukiPlatform) CanGet(query string) bool {
	if strings.TrimSpace(config.YukiAPIKey) == "" {
		return false
	}
	q := strings.TrimSpace(query)
	return strings.HasPrefix(strings.ToLower(q), "yuki:")
}

func (y *YukiPlatform) Get(query string, video bool) ([]*state.Track, error) {
	if strings.TrimSpace(config.YukiAPIKey) == "" {
		return nil, errors.New("yuki: API key is not configured")
	}

	cleanQuery := strings.TrimSpace(query)
	if strings.HasPrefix(strings.ToLower(cleanQuery), "yuki:") {
		cleanQuery = strings.TrimSpace(cleanQuery[5:])
	}
	if cleanQuery == "" {
		return nil, errors.New("yuki: empty search query")
	}

	ctx, cancel := context.WithTimeout(context.Background(), yukiAPITimeout)
	defer cancel()

	tracks, err := y.search(ctx, cleanQuery, video)
	if err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return nil, errors.New("yuki: no tracks found")
	}
	return tracks, nil
}

func (y *YukiPlatform) CanDownload(source state.PlatformName) bool {
	if strings.TrimSpace(config.YukiAPIKey) == "" {
		return false
	}
	switch source {
	case PlatformYouTube, PlatformYuki, PlatformSpotify, PlatformDeezer:
		return true
	default:
		return false
	}
}

func (y *YukiPlatform) Download(
	ctx context.Context,
	track *state.Track,
	_ *td.Message,
) (string, error) {
	if track == nil {
		return "", errors.New("yuki: nil track")
	}
	if strings.TrimSpace(config.YukiAPIKey) == "" {
		return "", errors.New("yuki: API key is not configured")
	}
	if strings.TrimSpace(track.ID) == "" {
		return "", errors.New("yuki: missing track id")
	}
	if strings.ContainsAny(track.ID, `/\`) || track.ID == "." || track.ID == ".." {
		return "", errors.New("yuki: invalid track id")
	}

	if p := findFile(track); p != "" {
		logger.Debug("Yuki: cache hit " + p)
		return p, nil
	}

	videoID := strings.TrimSpace(track.ID)
	if track.Source == PlatformSpotify || track.Source == PlatformDeezer {
		resolvedID, err := y.resolveVideoID(ctx, track)
		if err != nil {
			return "", err
		}
		videoID = resolvedID
	}

	dtype, ext, timeout := "audio", ".mp3", yukiAudioTimeout
	if track.Video {
		dtype, ext, timeout = "video", ".mp4", yukiVideoTimeout
	}

	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	path := getPath(track, ext)
	if err := os.MkdirAll("downloads", 0o755); err != nil {
		return "", fmt.Errorf("yuki: create download directory: %w", err)
	}

	endpoints := y.streamEndpoints()
	var lastErr error
	for idx, baseURL := range endpoints {
		if dctx.Err() != nil {
			return "", dctx.Err()
		}

		streamURL := fmt.Sprintf(
			"%s/stream/%s?key=%s&type=%s",
			baseURL,
			url.PathEscape(videoID),
			url.QueryEscape(config.YukiAPIKey),
			url.QueryEscape(dtype),
		)

		err := y.downloadFromURL(dctx, streamURL, path)
		if err == nil {
			return path, nil
		}
		lastErr = err
		if isDownloadCancelled(err) {
			return "", err
		}
		if idx < len(endpoints)-1 {
			logger.Warnf("Yuki: primary stream failed (%v), failing over to backup %s", err, endpoints[idx+1])
		}
	}

	return "", lastErr
}

func (y *YukiPlatform) primaryBaseURL() string {
	u := strings.TrimRight(strings.TrimSpace(config.YukiAPIURL), "/")
	if u == "" {
		return yukiDefaultPrimaryURL
	}
	return u
}

func (y *YukiPlatform) backupBaseURL() string {
	u := strings.TrimRight(strings.TrimSpace(config.YukiBackupAPIURL), "/")
	if u == "" {
		return yukiDefaultBackupURL
	}
	return u
}

func (y *YukiPlatform) streamEndpoints() []string {
	primary := y.primaryBaseURL()
	backup := y.backupBaseURL()
	if backup != "" && backup != primary {
		return []string{primary, backup}
	}
	return []string{primary}
}

func (y *YukiPlatform) downloadFromURL(ctx context.Context, streamURL, path string) error {
	r, err := rc.R().
		SetContext(ctx).
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36").
		SetResponseSaveFileName(path).
		Get(streamURL)
	if err != nil {
		_ = os.Remove(path)
		if isDownloadCancelled(err) {
			return err
		}
		return sanitizeAPIError(
			fmt.Errorf("yuki request failed: %w", err),
			config.YukiAPIKey,
		)
	}
	if r.IsStatusFailure() {
		_ = os.Remove(path)
		return sanitizeAPIError(
			fmt.Errorf("yuki returned HTTP %d", r.StatusCode()),
			config.YukiAPIKey,
		)
	}

	info, statErr := os.Stat(path)
	if statErr != nil || info.Size() < yukiMinValidBytes {
		_ = os.Remove(path)
		return errors.New("yuki returned an invalid or empty file")
	}

	return nil
}

func (y *YukiPlatform) resolveVideoID(ctx context.Context, track *state.Track) (string, error) {
	query := strings.TrimSpace(track.Title)
	if query == "" {
		return "", errors.New("yuki: track title is empty for search resolution")
	}
	sctx, cancel := context.WithTimeout(ctx, yukiAPITimeout)
	defer cancel()

	tracks, err := y.search(sctx, query, track.Video)
	if err != nil {
		return "", err
	}
	if len(tracks) == 0 || strings.TrimSpace(tracks[0].ID) == "" {
		return "", errors.New("yuki: failed to resolve video id from search")
	}
	return tracks[0].ID, nil
}

func (y *YukiPlatform) search(ctx context.Context, query string, video bool) ([]*state.Track, error) {
	searchURL := fmt.Sprintf(
		"%s/api/search?q=%s&key=%s",
		y.primaryBaseURL(),
		url.QueryEscape(query),
		url.QueryEscape(config.YukiAPIKey),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, errors.New("yuki: failed to create search request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := y.client.Do(req)
	if err != nil {
		return nil, sanitizeAPIError(fmt.Errorf("yuki search failed: %w", err), config.YukiAPIKey)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, sanitizeAPIError(fmt.Errorf("yuki search returned HTTP %d", resp.StatusCode), config.YukiAPIKey)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, yukiMaxJSONBytes))
	if err != nil {
		return nil, sanitizeAPIError(fmt.Errorf("yuki search read failed: %w", err), config.YukiAPIKey)
	}

	return parseYukiSearchResults(body, video)
}

func parseYukiSearchResults(body []byte, video bool) ([]*state.Track, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("yuki: invalid search JSON: %w", err)
	}

	var items []any
	switch v := raw.(type) {
	case []any:
		items = v
	case map[string]any:
		for _, key := range []string{"results", "tracks", "data", "items"} {
			if arr, ok := v[key].([]any); ok {
				items = arr
				break
			}
		}
		if len(items) == 0 {
			if obj, ok := v["result"].(map[string]any); ok {
				items = []any{obj}
			} else if _, hasID := v["id"]; hasID {
				items = []any{v}
			} else if _, hasVID := v["video_id"]; hasVID {
				items = []any{v}
			}
		}
	}

	tracks := make([]*state.Track, 0, len(items))
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		id := firstNonEmpty(
			safeStr(m["id"]),
			safeStr(m["video_id"]),
			safeStr(m["videoId"]),
		)
		title := firstNonEmpty(safeStr(m["title"]), safeStr(m["name"]))
		if id == "" || title == "" {
			continue
		}
		artist := firstNonEmpty(safeStr(m["artist"]), safeStr(m["channel"]), safeStr(m["author"]))
		artwork := firstNonEmpty(safeStr(m["thumbnail"]), safeStr(m["artwork"]), safeStr(m["thumb"]))
		trackURL := firstNonEmpty(safeStr(m["url"]), "https://www.youtube.com/watch?v="+id)
		dur := parseYukiDuration(m["duration"])

		tracks = append(tracks, &state.Track{
			ID:       id,
			Title:    title,
			Artist:   artist,
			Duration: dur,
			Artwork:  artwork,
			URL:      trackURL,
			Video:    video,
			Source:   PlatformYuki,
		})
		if len(tracks) >= yukiMaxSearchResults {
			break
		}
	}

	return tracks, nil
}

func parseYukiDuration(v any) int {
	switch d := v.(type) {
	case float64:
		return int(d)
	case int:
		return d
	case string:
		s := strings.TrimSpace(d)
		if s == "" {
			return 0
		}
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
		parts := strings.Split(s, ":")
		total := 0
		for _, p := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil {
				return 0
			}
			total = total*60 + n
		}
		return total
	}
	return 0
}
