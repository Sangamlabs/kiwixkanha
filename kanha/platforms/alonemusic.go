/*
 * AloneMusic API downloader for Kiwi Music.
 *
 * Supported API:
 *   GET /download?query=Song+Name&dl_type=audio&api_key=KEY
 *   GET /download?query=Video+Name&dl_type=video&api_key=KEY
 *
 * The downloader accepts:
 *   1. Direct audio/video response.
 *   2. JSON response containing a downloadable URL.
 *
 * NOTE:
 * The exact JSON response schema must be confirmed against the
 * AloneMusic API. This implementation checks common URL field names.
 */

package platforms

import (
	"KanhaMusic/config"
	state "KanhaMusic/kanha/core/models"
	"KanhaMusic/kanha/logger"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	td "github.com/Kanha/Meow"
)

const (
	PlatformAloneMusic state.PlatformName = "AloneMusic"

	aloneAudioTimeout = 300 * time.Second
	aloneVideoTimeout = 600 * time.Second

	aloneMinValidBytes = 10_000
	aloneMaxJSONBytes  = 2 << 20 // 2 MB
)

type AloneMusicPlatform struct {
	client *http.Client
}

func init() {
	Register(&AloneMusicPlatform{
		client: &http.Client{
			Timeout: 0, // Request timeout is controlled by context.
		},
	})
}

func (a *AloneMusicPlatform) Name() state.PlatformName {
	return PlatformAloneMusic
}

func (a *AloneMusicPlatform) Priority() int {
	// Higher priority than Meow (85).
	return 100
}

// AloneMusic is currently used only as a downloader.
// Existing YouTube/Spotify/Deezer search and metadata remain unchanged.
func (a *AloneMusicPlatform) CanGet(_ string) bool {
	return false
}

func (a *AloneMusicPlatform) Get(
	_ string,
	_ bool,
) ([]*state.Track, error) {
	return nil, errors.New("alonemusic: search is not enabled")
}

func (a *AloneMusicPlatform) CanDownload(
	source state.PlatformName,
) bool {
	if strings.TrimSpace(config.AloneMusicAPIURL) == "" ||
		strings.TrimSpace(config.AloneMusicAPIKey) == "" {
		return false
	}

	switch source {
	case PlatformYouTube, PlatformSpotify, PlatformDeezer:
		return true
	default:
		return false
	}
}

func (a *AloneMusicPlatform) Download(
	ctx context.Context,
	track *state.Track,
	_ *td.Message,
) (string, error) {
	if track == nil {
		return "", errors.New("alonemusic: nil track")
	}

	if strings.TrimSpace(config.AloneMusicAPIURL) == "" {
		return "", errors.New("alonemusic: API URL is not configured")
	}

	if strings.TrimSpace(config.AloneMusicAPIKey) == "" {
		return "", errors.New("alonemusic: API key is not configured")
	}

	// Reuse an already downloaded file if available.
	if path := findFile(track); path != "" {
		logger.Debug("AloneMusic: cache hit " + path)
		return path, nil
	}

	query := strings.TrimSpace(track.Title)
	if query == "" {
		return "", errors.New("alonemusic: track title is empty")
	}

	dlType := "audio"
	extension := ".mp3"
	timeout := aloneAudioTimeout

	if track.Video {
		dlType = "video"
		extension = ".mp4"
		timeout = aloneVideoTimeout
	}

	downloadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	apiURL, err := url.Parse(
		strings.TrimRight(config.AloneMusicAPIURL, "/") + "/download",
	)
	if err != nil {
		return "", errors.New("alonemusic: invalid API URL")
	}

	params := apiURL.Query()
	params.Set("query", query)
	params.Set("dl_type", dlType)
	params.Set("api_key", config.AloneMusicAPIKey)
	apiURL.RawQuery = params.Encode()

	path := getPath(track, extension)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("alonemusic: create download directory: %w", err)
	}

	logger.Infof(
		"AloneMusic: requesting %s download for %q",
		dlType,
		query,
	)

	req, err := http.NewRequestWithContext(
		downloadCtx,
		http.MethodGet,
		apiURL.String(),
		nil,
	)
	if err != nil {
		return "", errors.New("alonemusic: failed to create request")
	}

	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	)
	req.Header.Set("Accept", "*/*")

	client := a.client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		if isDownloadCancelled(err) {
			return "", err
		}

		return "", sanitizeAPIError(
			fmt.Errorf("alonemusic request failed: %w", err),
			config.AloneMusicAPIKey,
		)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		message := strings.TrimSpace(string(body))

		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}

		return "", sanitizeAPIError(
			fmt.Errorf(
				"alonemusic API returned HTTP %d: %s",
				resp.StatusCode,
				message,
			),
			config.AloneMusicAPIKey,
		)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))

	// Some APIs return JSON containing a download URL instead of
	// streaming the actual media file.
	if strings.Contains(contentType, "application/json") ||
		strings.Contains(contentType, "text/json") {
		downloadURL, err := extractAloneMusicDownloadURL(resp.Body)
		if err != nil {
			return "", fmt.Errorf("alonemusic: %w", err)
		}

		return a.downloadMediaURL(
			downloadCtx,
			downloadURL,
			path,
		)
	}

	// Otherwise, assume the endpoint directly streams the media.
	if err := saveAloneMusicResponse(resp.Body, path); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("alonemusic: %w", err)
	}

	if err := validateAloneMusicFile(path); err != nil {
		os.Remove(path)
		return "", err
	}

	logger.Info("AloneMusic: download completed -> " + path)
	return path, nil
}

// extractAloneMusicDownloadURL searches common JSON URL fields.
// The exact response schema should be verified with the API provider.
func extractAloneMusicDownloadURL(
	reader io.Reader,
) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, aloneMaxJSONBytes))
	if err != nil {
		return "", errors.New("failed to read API JSON response")
	}

	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", errors.New("API returned invalid JSON")
	}

	candidate := findDownloadURL(payload)
	if candidate == "" {
		return "", errors.New(
			"API JSON did not contain a recognized download URL",
		)
	}

	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", errors.New("API returned an invalid download URL")
	}

	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("API returned an unsupported download URL")
	}

	if parsed.Host == "" {
		return "", errors.New("API returned an empty download URL host")
	}

	return parsed.String(), nil
}

// findDownloadURL recursively checks common URL field names.
func findDownloadURL(value any) string {
	switch item := value.(type) {
	case map[string]any:
		// Prefer common download URL fields.
		keys := []string{
			"download_url",
			"downloadUrl",
			"audio_url",
			"audioUrl",
			"video_url",
			"videoUrl",
			"stream_url",
			"streamUrl",
			"file_url",
			"fileUrl",
			"url",
			"link",
		}

		for _, key := range keys {
			if raw, ok := item[key]; ok {
				if str, ok := raw.(string); ok {
					str = strings.TrimSpace(str)
					if isHTTPURL(str) {
						return str
					}
				}
			}
		}

		for _, nested := range item {
			if found := findDownloadURL(nested); found != "" {
				return found
			}
		}

	case []any:
		for _, nested := range item {
			if found := findDownloadURL(nested); found != "" {
				return found
			}
		}
	}

	return ""
}

func isHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}

	return (parsed.Scheme == "http" || parsed.Scheme == "https") &&
		parsed.Host != ""
}

// downloadMediaURL downloads the media from a URL returned by the API.
func (a *AloneMusicPlatform) downloadMediaURL(
	ctx context.Context,
	mediaURL string,
	path string,
) (string, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		mediaURL,
		nil,
	)
	if err != nil {
		return "", errors.New("failed to create media download request")
	}

	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	)

	client := a.client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		if isDownloadCancelled(err) {
			return "", err
		}

		return "", fmt.Errorf("media download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf(
			"media server returned HTTP %d",
			resp.StatusCode,
		)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") || strings.Contains(contentType, "text/html") || strings.Contains(contentType, "text/plain") {
		return "", fmt.Errorf("media URL returned non-media content type: %s", contentType)
	}

	if err := saveAloneMusicResponse(resp.Body, path); err != nil {
		os.Remove(path)
		return "", err
	}

	if err := validateAloneMusicFile(path); err != nil {
		os.Remove(path)
		return "", err
	}

	logger.Info("AloneMusic: media URL download completed -> " + path)
	return path, nil
}

// saveAloneMusicResponse writes a response body to disk.
func saveAloneMusicResponse(
	reader io.Reader,
	path string,
) error {
	file, err := os.Create(path)
	if err != nil {
		return errors.New("could not create download file")
	}

	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()

	if copyErr != nil {
		os.Remove(path)
		return fmt.Errorf("failed to save media: %w", copyErr)
	}

	if closeErr != nil {
		os.Remove(path)
		return errors.New("failed to close downloaded file")
	}

	return nil
}

// validateAloneMusicFile checks whether the downloaded file is usable.
func validateAloneMusicFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return errors.New("downloaded file was not created")
	}

	if info.Size() < aloneMinValidBytes {
		return errors.New("downloaded file is empty or too small")
	}

	f, err := os.Open(path)
	if err == nil {
		buf := make([]byte, 512)
		n, _ := f.Read(buf)
		_ = f.Close()
		if n > 0 {
			lower := strings.ToLower(string(buf[:n]))
			if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") || strings.Contains(lower, "{\"error\"") {
				return errors.New("alonemusic: downloaded file contains HTML or error payload")
			}
		}
	}

	return nil
}
