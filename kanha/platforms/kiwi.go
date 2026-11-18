/*
 * ● Kiwi Music (KanhaMusic fork)
 * ○ Kiwi Music API Provider Platform Implementation
 *
 * Supported API:
 *   GET /v1/search?q=SONG_NAME
 *   GET /v1/resolve/{track_id}
 *   GET /v1/stream/{track_id}
 *
 * Authentication:
 *   X-API-Key: YOUR_API_KEY
 *   Accept: application/json
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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"KanhaMusic/config"
	state "KanhaMusic/kanha/core/models"
	"KanhaMusic/kanha/logger"

	td "github.com/Kanha/Meow"
)

const (
	PlatformKiwiMusic state.PlatformName = "KiwiMusic"

	kiwiAPITimeout       = 20 * time.Second
	kiwiAudioTimeout     = 300 * time.Second
	kiwiMinValidBytes    = 10_000
	kiwiMaxJSONBytes     = 2 << 20 // 2 MB
	kiwiMaxErrorBytes    = 4096
	kiwiMaxSearchResults = 15
)

type KiwiMusicPlatform struct {
	client *http.Client
}

func init() {
	Register(&KiwiMusicPlatform{
		client: &http.Client{
			Timeout: 0, // Request timeout is controlled by context.
		},
	})
}

func (k *KiwiMusicPlatform) Name() state.PlatformName {
	return PlatformKiwiMusic
}

func (k *KiwiMusicPlatform) Priority() int {
	return 100
}

func (k *KiwiMusicPlatform) CanGet(query string) bool {
	if strings.TrimSpace(config.KiwiMusicAPIURL) == "" ||
		strings.TrimSpace(config.KiwiMusicAPIKey) == "" {
		return false
	}

	q := strings.TrimSpace(query)
	if q == "" {
		return false
	}

	return strings.HasPrefix(strings.ToLower(q), "kiwi:") || isKiwiURL(q)
}

func (k *KiwiMusicPlatform) Get(
	query string,
	_ bool, // Audio only initially; video is not supported.
) ([]*state.Track, error) {
	if strings.TrimSpace(config.KiwiMusicAPIURL) == "" {
		return nil, errors.New("kiwimusic: API URL is not configured")
	}

	if strings.TrimSpace(config.KiwiMusicAPIKey) == "" {
		return nil, errors.New("kiwimusic: API key is not configured")
	}

	cleanQuery := strings.TrimSpace(query)
	if strings.HasPrefix(strings.ToLower(cleanQuery), "kiwi:") {
		cleanQuery = strings.TrimSpace(cleanQuery[5:])
	}

	if cleanQuery == "" {
		return nil, errors.New("kiwimusic: empty search query")
	}

	searchCtx, cancel := context.WithTimeout(context.Background(), kiwiAPITimeout)
	defer cancel()

	apiURL, err := url.Parse(
		strings.TrimRight(config.KiwiMusicAPIURL, "/") + "/v1/search",
	)
	if err != nil {
		return nil, errors.New("kiwimusic: invalid API URL")
	}

	params := apiURL.Query()
	params.Set("q", cleanQuery)
	apiURL.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(
		searchCtx,
		http.MethodGet,
		apiURL.String(),
		nil,
	)
	if err != nil {
		return nil, errors.New("kiwimusic: failed to create search request")
	}

	req.Header.Set("X-API-Key", config.KiwiMusicAPIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	)

	client := k.httpClient()
	resp, err := client.Do(req)
	if err != nil {
		if isDownloadCancelled(err) {
			return nil, err
		}
		return nil, sanitizeAPIError(
			fmt.Errorf("kiwimusic search request failed: %w", err),
			config.KiwiMusicAPIKey,
		)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, kiwiMaxErrorBytes))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, sanitizeAPIError(
			fmt.Errorf(
				"kiwimusic search API returned HTTP %d: %s",
				resp.StatusCode,
				message,
			),
			config.KiwiMusicAPIKey,
		)
	}

	tracks, err := decodeKiwiSearchResponse(resp.Body)
	if err != nil {
		return nil, sanitizeAPIError(
			fmt.Errorf("kiwimusic search decode failed: %w", err),
			config.KiwiMusicAPIKey,
		)
	}

	return tracks, nil
}

func (k *KiwiMusicPlatform) CanDownload(
	source state.PlatformName,
) bool {
	if strings.TrimSpace(config.KiwiMusicAPIURL) == "" ||
		strings.TrimSpace(config.KiwiMusicAPIKey) == "" {
		return false
	}

	switch source {
	case PlatformKiwiMusic, PlatformYouTube, PlatformSpotify, PlatformDeezer:
		return true
	default:
		return false
	}
}

func (k *KiwiMusicPlatform) Download(
	ctx context.Context,
	track *state.Track,
	_ *td.Message,
) (string, error) {
	if track == nil {
		return "", errors.New("kiwimusic: nil track")
	}

	if strings.TrimSpace(config.KiwiMusicAPIURL) == "" {
		return "", errors.New("kiwimusic: API URL is not configured")
	}

	if strings.TrimSpace(config.KiwiMusicAPIKey) == "" {
		return "", errors.New("kiwimusic: API key is not configured")
	}

	trackID := strings.TrimSpace(track.ID)
	if trackID == "" {
		return "", errors.New("kiwimusic: track ID is empty")
	}

	// Path traversal protection
	if strings.ContainsAny(trackID, `/\`) || trackID == "." || trackID == ".." {
		return "", errors.New("kiwimusic: invalid track ID")
	}

	// 2. Check existing local cache using existing findFile(track)
	if cached := findFile(track); cached != "" {
		logger.Debug("KiwiMusic: cache hit " + cached)
		return cached, nil
	}

	path := getPath(track, ".mp3")

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("kiwimusic: create download directory: %w", err)
	}

	downloadCtx, cancel := context.WithTimeout(ctx, kiwiAudioTimeout)
	defer cancel()

	logger.Infof("KiwiMusic: resolving and downloading audio for %q (ID=%s)", track.Title, trackID)

	// Preferred workflow:
	// 3. Resolve the track using Kiwi backend.
	// 4. Obtain the actual hosted audio URL.
	// 5. Download the audio using request context.
	var downloadErr error
	mediaURL, resolveErr := k.resolveTrack(downloadCtx, trackID)
	if resolveErr == nil && mediaURL != "" {
		downloadErr = k.downloadMediaURL(downloadCtx, mediaURL, path)
	} else {
		// Fallback to /v1/stream/{track_id} if resolve did not return URL
		logger.Debugf("KiwiMusic: resolve endpoint did not return URL (%v), attempting stream endpoint", resolveErr)
		downloadErr = k.streamMedia(downloadCtx, trackID, path)
	}

	if downloadErr != nil {
		_ = os.Remove(path)
		if isDownloadCancelled(downloadErr) {
			return "", downloadErr
		}
		return "", sanitizeAPIError(
			fmt.Errorf("kiwimusic download failed: %w", downloadErr),
			config.KiwiMusicAPIKey,
		)
	}

	// 7. Validate the result using the existing ValidateTrackAudio() function
	if err := ValidateTrackAudio(path, track); err != nil {
		_ = os.Remove(path)
		return "", sanitizeAPIError(
			fmt.Errorf("kiwimusic: audio validation failed: %w", err),
			config.KiwiMusicAPIKey,
		)
	}

	// 8. Return the local file path
	logger.Infof("KiwiMusic: download completed -> %s", path)
	return path, nil
}

func (k *KiwiMusicPlatform) httpClient() *http.Client {
	if k.client != nil {
		return k.client
	}
	return http.DefaultClient
}

// safeClient returns an HTTP client configured to follow redirects safely and protect against SSRF.
func (k *KiwiMusicPlatform) safeClient() *http.Client {
	base := k.httpClient()
	cl := *base
	cl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if _, err := validateKiwiMediaURL(req.URL.String()); err != nil {
			return fmt.Errorf("unsafe redirect URL: %w", err)
		}
		// Strip sensitive auth headers when redirecting to a different host (e.g. storage CDN)
		if len(via) > 0 && !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
			req.Header.Del("X-API-Key")
		}
		return nil
	}
	return &cl
}

// noRedirectClient returns an HTTP client that does not follow redirects,
// useful for inspecting redirect Location headers.
func (k *KiwiMusicPlatform) noRedirectClient() *http.Client {
	base := k.httpClient()
	cl := *base
	cl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &cl
}

func (k *KiwiMusicPlatform) resolveTrack(
	ctx context.Context,
	trackID string,
) (string, error) {
	resolveURL, err := url.Parse(
		strings.TrimRight(config.KiwiMusicAPIURL, "/") + "/v1/resolve/" + url.PathEscape(trackID),
	)
	if err != nil {
		return "", errors.New("kiwimusic: invalid resolve endpoint URL")
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		resolveURL.String(),
		nil,
	)
	if err != nil {
		return "", errors.New("kiwimusic: failed to create resolve request")
	}

	req.Header.Set("X-API-Key", config.KiwiMusicAPIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	)

	client := k.noRedirectClient()
	resp, err := client.Do(req)
	if err != nil {
		if isDownloadCancelled(err) {
			return "", err
		}
		return "", fmt.Errorf("kiwimusic resolve request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc != "" {
			return loc, nil
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, kiwiMaxErrorBytes))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return "", fmt.Errorf("kiwimusic resolve API returned HTTP %d: %s", resp.StatusCode, message)
	}

	return extractKiwiDownloadURL(resp.Body)
}

func (k *KiwiMusicPlatform) downloadMediaURL(
	ctx context.Context,
	mediaURL string,
	path string,
) error {
	safeURL, err := validateKiwiMediaURL(mediaURL)
	if err != nil {
		return fmt.Errorf("kiwimusic: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		safeURL,
		nil,
	)
	if err != nil {
		return errors.New("kiwimusic: failed to create media download request")
	}

	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	)
	req.Header.Set("Accept", "*/*")

	client := k.safeClient()
	resp, err := client.Do(req)
	if err != nil {
		if isDownloadCancelled(err) {
			return err
		}
		return fmt.Errorf("kiwimusic: media download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("kiwimusic: media server returned HTTP %d", resp.StatusCode)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") ||
		strings.Contains(contentType, "text/html") ||
		strings.Contains(contentType, "text/plain") {
		return fmt.Errorf("kiwimusic: media URL returned non-media content type: %s", contentType)
	}

	if err := saveKiwiResponse(resp.Body, path); err != nil {
		_ = os.Remove(path)
		return err
	}

	if err := validateKiwiFile(path); err != nil {
		_ = os.Remove(path)
		return err
	}

	return nil
}

func (k *KiwiMusicPlatform) streamMedia(
	ctx context.Context,
	trackID string,
	path string,
) error {
	streamURL, err := url.Parse(
		strings.TrimRight(config.KiwiMusicAPIURL, "/") + "/v1/stream/" + url.PathEscape(trackID),
	)
	if err != nil {
		return errors.New("kiwimusic: invalid stream endpoint URL")
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		streamURL.String(),
		nil,
	)
	if err != nil {
		return errors.New("kiwimusic: failed to create stream request")
	}

	req.Header.Set("X-API-Key", config.KiwiMusicAPIKey)
	req.Header.Set("Accept", "application/json, audio/*, */*")
	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	)

	client := k.safeClient()
	resp, err := client.Do(req)
	if err != nil {
		if isDownloadCancelled(err) {
			return err
		}
		return fmt.Errorf("kiwimusic stream request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, kiwiMaxErrorBytes))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("kiwimusic stream API returned HTTP %d: %s", resp.StatusCode, message)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") ||
		strings.Contains(contentType, "text/json") {
		downloadURL, err := extractKiwiDownloadURL(resp.Body)
		if err != nil {
			return fmt.Errorf("kiwimusic: %w", err)
		}
		return k.downloadMediaURL(ctx, downloadURL, path)
	}

	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "text/plain") {
		return fmt.Errorf("kiwimusic: stream endpoint returned non-media content type: %s", contentType)
	}

	if err := saveKiwiResponse(resp.Body, path); err != nil {
		_ = os.Remove(path)
		return err
	}

	if err := validateKiwiFile(path); err != nil {
		_ = os.Remove(path)
		return err
	}

	return nil
}

func extractKiwiDownloadURL(reader io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, kiwiMaxJSONBytes))
	if err != nil {
		return "", errors.New("failed to read API JSON response")
	}

	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", errors.New("API returned invalid JSON")
	}

	candidate := findDownloadURL(payload)
	if candidate == "" {
		return "", errors.New("API JSON did not contain a recognized download URL")
	}

	return validateKiwiMediaURL(candidate)
}

func validateKiwiMediaURL(raw string) (string, error) {
	u := strings.TrimSpace(raw)
	if u == "" {
		return "", errors.New("empty media URL")
	}

	for _, r := range u {
		if r < 32 || r == 127 {
			return "", errors.New("media URL contains control characters")
		}
	}

	parsed, err := url.Parse(u)
	if err != nil {
		return "", errors.New("invalid media URL")
	}

	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("unsupported media URL scheme")
	}

	if parsed.Host == "" {
		return "", errors.New("empty media URL host")
	}

	// In unit tests, mock servers may run on localhost / loopback.
	// If the host matches configured KiwiMusicAPIURL, allow it.
	if strings.TrimSpace(config.KiwiMusicAPIURL) != "" {
		if cfgURL, err := url.Parse(config.KiwiMusicAPIURL); err == nil && cfgURL.Host != "" {
			if strings.EqualFold(parsed.Host, cfgURL.Host) {
				return parsed.String(), nil
			}
		}
	}

	// For external hosted URLs (such as Yukysbox or external CDNs), apply full SSRF sanitation
	return sanitizeMediaURL(u)
}

func saveKiwiResponse(reader io.Reader, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return errors.New("could not create download file")
	}

	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()

	if copyErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("failed to save media: %w", copyErr)
	}

	if closeErr != nil {
		_ = os.Remove(path)
		return errors.New("failed to close downloaded file")
	}

	return nil
}

func validateKiwiFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return errors.New("downloaded file was not created")
	}

	if info.Size() < kiwiMinValidBytes {
		return fmt.Errorf("downloaded file is empty or too small (%d bytes)", info.Size())
	}

	f, err := os.Open(path)
	if err == nil {
		buf := make([]byte, 512)
		n, _ := f.Read(buf)
		_ = f.Close()
		if n > 0 {
			lower := strings.ToLower(string(buf[:n]))
			if strings.Contains(lower, "<html") ||
				strings.Contains(lower, "<!doctype") ||
				strings.Contains(lower, "{\"error\"") ||
				strings.Contains(lower, "{\"message\"") {
				return errors.New("downloaded file contains HTML or error payload instead of valid audio")
			}
		}
	}

	return nil
}

func isKiwiURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	if strings.Contains(host, "kiwi") {
		return true
	}
	if strings.TrimSpace(config.KiwiMusicAPIURL) != "" {
		if cfgURL, err := url.Parse(config.KiwiMusicAPIURL); err == nil && cfgURL.Host != "" {
			if strings.EqualFold(u.Host, cfgURL.Host) {
				return true
			}
		}
	}
	return false
}

type kiwiSearchRawItem struct {
	ID        any    `json:"id"`
	TrackID   any    `json:"track_id"`
	TrackId   any    `json:"trackId"`
	SongID    any    `json:"song_id"`
	Title     string `json:"title"`
	Name      string `json:"name"`
	Artist    any    `json:"artist"`
	Artists   any    `json:"artists"`
	Singer    string `json:"singer"`
	Author    string `json:"author"`
	Duration  any    `json:"duration"`
	Thumbnail string `json:"thumbnail"`
	Artwork   string `json:"artwork"`
	Image     string `json:"image"`
	Cover     string `json:"cover"`
	Thumb     string `json:"thumb"`
	Picture   string `json:"picture"`
	URL       string `json:"url"`
}

func decodeKiwiSearchResponse(reader io.Reader) ([]*state.Track, error) {
	data, err := io.ReadAll(io.LimitReader(reader, kiwiMaxJSONBytes))
	if err != nil {
		return nil, errors.New("failed to read search response")
	}

	var rawItems []kiwiSearchRawItem

	// Try unmarshaling as direct slice
	if err := json.Unmarshal(data, &rawItems); err != nil {
		// Try unmarshaling as wrapped object: {"results": [...]} / {"tracks": [...]} / {"data": [...]}
		var wrapped map[string]json.RawMessage
		if objErr := json.Unmarshal(data, &wrapped); objErr != nil {
			return nil, errors.New("invalid JSON response from search API")
		}

		foundList := false
		for _, key := range []string{"results", "tracks", "data", "items"} {
			if rawList, ok := wrapped[key]; ok {
				if err := json.Unmarshal(rawList, &rawItems); err == nil {
					foundList = true
					break
				}
			}
		}

		if !foundList {
			return nil, nil
		}
	}

	var tracks []*state.Track
	for _, item := range rawItems {
		trackID := extractIDString(item.ID, item.TrackID, item.TrackId, item.SongID)
		title := strings.TrimSpace(firstNonEmpty(item.Title, item.Name))

		// Do not return malformed tracks with empty IDs or titles
		if trackID == "" || title == "" {
			continue
		}

		artist := extractArtistString(item.Artist, item.Artists, item.Singer, item.Author)
		duration := extractDurationInt(item.Duration)
		thumbnail := strings.TrimSpace(firstNonEmpty(
			item.Thumbnail,
			item.Artwork,
			item.Image,
			item.Cover,
			item.Thumb,
			item.Picture,
		))

		trackURL := strings.TrimSpace(item.URL)
		if trackURL == "" && strings.TrimSpace(config.KiwiMusicAPIURL) != "" {
			trackURL = fmt.Sprintf("%s/v1/resolve/%s", strings.TrimRight(config.KiwiMusicAPIURL, "/"), trackID)
		}

		tracks = append(tracks, &state.Track{
			ID:       trackID,
			Title:    title,
			Artist:   artist,
			Duration: duration,
			Artwork:  thumbnail,
			URL:      trackURL,
			Source:   PlatformKiwiMusic,
			Video:    false,
		})

		if len(tracks) >= kiwiMaxSearchResults {
			break
		}
	}

	return tracks, nil
}

func extractIDString(values ...any) string {
	for _, v := range values {
		if v == nil {
			continue
		}
		switch val := v.(type) {
		case string:
			if s := strings.TrimSpace(val); s != "" {
				return s
			}
		case float64:
			if val > 0 {
				return strconv.FormatInt(int64(val), 10)
			}
		case int:
			if val > 0 {
				return strconv.Itoa(val)
			}
		case int64:
			if val > 0 {
				return strconv.FormatInt(val, 10)
			}
		}
	}
	return ""
}

func extractArtistString(values ...any) string {
	for _, v := range values {
		if v == nil {
			continue
		}
		switch val := v.(type) {
		case string:
			if s := strings.TrimSpace(val); s != "" {
				return s
			}
		case []any:
			var names []string
			for _, elem := range val {
				if str, ok := elem.(string); ok && strings.TrimSpace(str) != "" {
					names = append(names, strings.TrimSpace(str))
				} else if m, ok := elem.(map[string]any); ok {
					if name, ok := m["name"].(string); ok && strings.TrimSpace(name) != "" {
						names = append(names, strings.TrimSpace(name))
					}
				}
			}
			if len(names) > 0 {
				return strings.Join(names, ", ")
			}
		}
	}
	return ""
}

func extractDurationInt(val any) int {
	if val == nil {
		return 0
	}
	switch v := val.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		s := strings.TrimSpace(v)
		if d, err := strconv.Atoi(s); err == nil {
			return d
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int(f)
		}
		// Try MM:SS or HH:MM:SS format
		parts := strings.Split(s, ":")
		if len(parts) == 2 {
			m, _ := strconv.Atoi(parts[0])
			sec, _ := strconv.Atoi(parts[1])
			return m*60 + sec
		} else if len(parts) == 3 {
			h, _ := strconv.Atoi(parts[0])
			m, _ := strconv.Atoi(parts[1])
			sec, _ := strconv.Atoi(parts[2])
			return h*3600 + m*60 + sec
		}
	}
	return 0
}
