/*
 * OneGrab API downloader for KanhaMusic.
 *
 * API documentation:
 * https://api.onegrab.fun
 *
 * Endpoints:
 *   GET /api/track?url=<TRACK_URL>&video=false
 *   GET /api/track?url=<TRACK_URL>&video=true
 *
 * Authentication:
 *   X-API-Key: <ONEGRAB_API_KEY>
 *
 * The API returns:
 *   cdnurl, id, is_video, key, platform, url
 */

package platforms

import (
	"KanhaMusic/config"
	state "KanhaMusic/kanha/core/models"
	"KanhaMusic/kanha/logger"

	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	td "github.com/Kanha/Meow"
)

const (
	PlatformOneGrab state.PlatformName = "OneGrab"

	oneGrabAPIURL = "https://api.onegrab.fun"

	oneGrabAPITimeout   = 20 * time.Second
	oneGrabAudioTimeout = 300 * time.Second
	oneGrabVideoTimeout = 600 * time.Second

	oneGrabMinFileSize  = 10_000
	oneGrabMaxJSONSize  = 2 << 20
	oneGrabMaxErrorSize = 4096
)

var (
	oneGrabAPIClient = &http.Client{
		Timeout: oneGrabAPITimeout,
	}

	// CDN downloads use request-context timeouts instead of a fixed
	// client timeout because audio/video files may be large.
	oneGrabMediaClient = &http.Client{
		Timeout: 0,
	}

	oneGrabSpotifyIV = []byte{
		0x72, 0xe0, 0x67, 0xfb,
		0xdd, 0xcb, 0xcf, 0x77,
		0xeb, 0xe8, 0xbc, 0x64,
		0x3f, 0x63, 0x0d, 0x93,
	}
)

type OneGrabPlatform struct{}

type oneGrabTrackResponse struct {
	CDNURL   string `json:"cdnurl"`
	ID       string `json:"id"`
	IsVideo  bool   `json:"is_video"`
	Key      string `json:"key"`
	Platform string `json:"platform"`
	URL      string `json:"url"`
}

func init() {
	Register(&OneGrabPlatform{})
}

func (o *OneGrabPlatform) Name() state.PlatformName {
	return PlatformOneGrab
}

func (o *OneGrabPlatform) Priority() int {
	// AloneMusic: 100
	// OneGrab:    95
	// Meow:       85
	return 95
}

// OneGrab is used as a downloader.
// Existing YouTube, Spotify and Deezer metadata handling stays unchanged.
func (o *OneGrabPlatform) CanGet(_ string) bool {
	return false
}

func (o *OneGrabPlatform) Get(
	_ string,
	_ bool,
) ([]*state.Track, error) {
	return nil, errors.New("onegrab: search is not enabled")
}

func (o *OneGrabPlatform) CanDownload(
	source state.PlatformName,
) bool {
	if strings.TrimSpace(config.OneGrabAPIKey) == "" {
		return false
	}

	switch source {
	case PlatformYouTube, PlatformSpotify, PlatformDeezer:
		return true
	default:
		return false
	}
}

func (o *OneGrabPlatform) Download(
	ctx context.Context,
	track *state.Track,
	_ *td.Message,
) (string, error) {
	if track == nil {
		return "", errors.New("onegrab: nil track")
	}

	if strings.TrimSpace(config.OneGrabAPIKey) == "" {
		return "", errors.New("onegrab: API key is not configured")
	}

	if strings.TrimSpace(track.URL) == "" {
		return "", errors.New("onegrab: track URL is empty")
	}

	if strings.TrimSpace(track.ID) == "" {
		return "", errors.New("onegrab: track ID is empty")
	}

	// Prevent path traversal through the track ID.
	if strings.ContainsAny(track.ID, `/\`) ||
		track.ID == "." ||
		track.ID == ".." {
		return "", errors.New("onegrab: invalid track ID")
	}

	// Reuse a previously downloaded file.
	if cached := findFile(track); cached != "" {
		logger.Debug("OneGrab: cache hit " + cached)
		return cached, nil
	}

	isSpotify := track.Source == PlatformSpotify

	dlType := "audio"
	extension := ".mp3"
	timeout := oneGrabAudioTimeout

	if track.Video {
		dlType = "video"
		extension = ".mp4"
		timeout = oneGrabVideoTimeout
	}

	// Spotify encrypted audio is converted to a final OGG file.
	if isSpotify && !track.Video {
		extension = ".ogg"
	}

	downloadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Get the media URL and optional decryption key.
	info, err := o.getTrackInfo(downloadCtx, track.URL, track.Video)
	if err != nil {
		return "", err
	}

	if info.CDNURL == "" {
		return "", errors.New("onegrab: API returned an empty CDN URL")
	}

	// Validate the returned CDN URL before making another request.
	cdnURL, err := sanitizeMediaURL(info.CDNURL)
	if err != nil {
		return "", errors.New("onegrab: API returned an unsafe CDN URL")
	}

	path := getPath(track, extension)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("onegrab: create download directory: %w", err)
	}

	logger.Infof(
		"OneGrab: downloading %s track %q",
		dlType,
		track.Title,
	)

	// Spotify response with a key means the CDN audio is encrypted.
	if isSpotify && info.Key != "" && !track.Video {
		if err := o.downloadSpotify(
			downloadCtx,
			cdnURL,
			info.Key,
			path,
		); err != nil {
			os.Remove(path)

			if isDownloadCancelled(err) {
				return "", err
			}

			return "", fmt.Errorf("onegrab spotify processing failed: %w", err)
		}

		if err := validateOneGrabFile(path); err != nil {
			os.Remove(path)
			return "", err
		}

		logger.Info("OneGrab: Spotify download and decryption completed -> " + path)
		return path, nil
	}

	// Normal audio/video CDN download.
	if err := o.downloadMedia(
		downloadCtx,
		cdnURL,
		path,
	); err != nil {
		os.Remove(path)

		if isDownloadCancelled(err) {
			return "", err
		}

		return "", fmt.Errorf("onegrab media download failed: %w", err)
	}

	if err := validateOneGrabFile(path); err != nil {
		os.Remove(path)
		return "", err
	}

	logger.Info("OneGrab: download completed -> " + path)
	return path, nil
}

// getTrackInfo calls OneGrab /api/track.
func (o *OneGrabPlatform) getTrackInfo(
	ctx context.Context,
	trackURL string,
	video bool,
) (*oneGrabTrackResponse, error) {
	safeTrackURL, err := sanitizeMediaURL(trackURL)
	if err != nil {
		return nil, errors.New("onegrab: invalid track URL")
	}

	endpoint, err := url.Parse(oneGrabAPIURL + "/api/track")
	if err != nil {
		return nil, errors.New("onegrab: invalid API endpoint")
	}

	params := endpoint.Query()
	params.Set("url", safeTrackURL)
	params.Set("video", fmt.Sprintf("%t", video))
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	)
	if err != nil {
		return nil, errors.New("onegrab: failed to create API request")
	}

	req.Header.Set("X-API-Key", config.OneGrabAPIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	)

	resp, err := oneGrabAPIClient.Do(req)
	if err != nil {
		if isDownloadCancelled(err) {
			return nil, err
		}

		return nil, sanitizeAPIError(
			fmt.Errorf("onegrab API request failed: %w", err),
			config.OneGrabAPIKey,
		)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(
			io.LimitReader(resp.Body, oneGrabMaxErrorSize),
		)

		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}

		return nil, sanitizeAPIError(
			fmt.Errorf(
				"onegrab API returned HTTP %d: %s",
				resp.StatusCode,
				message,
			),
			config.OneGrabAPIKey,
		)
	}

	var result oneGrabTrackResponse

	decoder := json.NewDecoder(
		io.LimitReader(resp.Body, oneGrabMaxJSONSize),
	)

	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf(
			"onegrab: failed to decode track response: %w",
			err,
		)
	}

	if strings.TrimSpace(result.CDNURL) == "" {
		return nil, errors.New(
			"onegrab: response does not contain cdnurl",
		)
	}

	return &result, nil
}

// downloadMedia downloads a normal audio/video CDN URL.
func (o *OneGrabPlatform) downloadMedia(
	ctx context.Context,
	mediaURL string,
	path string,
) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		mediaURL,
		nil,
	)
	if err != nil {
		return errors.New("failed to create CDN download request")
	}

	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	)
	req.Header.Set("Accept", "*/*")

	resp, err := oneGrabMediaClient.Do(req)
	if err != nil {
		if isDownloadCancelled(err) {
			return err
		}
		return fmt.Errorf("CDN request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf(
			"CDN returned HTTP %d",
			resp.StatusCode,
		)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") || strings.Contains(contentType, "text/html") || strings.Contains(contentType, "text/plain") {
		return fmt.Errorf("CDN returned non-media content type (%s) instead of media", contentType)
	}

	if err := saveOneGrabResponse(resp.Body, path); err != nil {
		return err
	}

	return validateOneGrabFile(path)
}

// downloadSpotify downloads encrypted Spotify audio, decrypts it,
// rebuilds the OGG header and prepares the final OGG with FFmpeg.
func (o *OneGrabPlatform) downloadSpotify(
	ctx context.Context,
	cdnURL string,
	hexKey string,
	outputPath string,
) error {
	key, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil {
		return errors.New("invalid Spotify decryption key")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("create Spotify AES cipher: %w", err)
	}

	encryptedFile, err := os.CreateTemp(
		filepath.Dir(outputPath),
		"onegrab-*.encrypted",
	)
	if err != nil {
		return fmt.Errorf("create temporary encrypted file: %w", err)
	}

	encryptedPath := encryptedFile.Name()
	defer os.Remove(encryptedPath)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		cdnURL,
		nil,
	)
	if err != nil {
		encryptedFile.Close()
		return errors.New("create Spotify CDN request failed")
	}

	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	)

	resp, err := oneGrabMediaClient.Do(req)
	if err != nil {
		encryptedFile.Close()

		if isDownloadCancelled(err) {
			return err
		}

		return fmt.Errorf("Spotify CDN request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		encryptedFile.Close()
		return fmt.Errorf(
			"Spotify CDN returned HTTP %d",
			resp.StatusCode,
		)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") {
		encryptedFile.Close()
		return errors.New("Spotify CDN returned JSON instead of media")
	}

	if _, err := io.Copy(encryptedFile, resp.Body); err != nil {
		encryptedFile.Close()
		return fmt.Errorf("save encrypted Spotify audio: %w", err)
	}

	if err := encryptedFile.Close(); err != nil {
		return errors.New("failed to close encrypted Spotify file")
	}

	encryptedInfo, err := os.Stat(encryptedPath)
	if err != nil || encryptedInfo.Size() < oneGrabMinFileSize {
		return errors.New("Spotify encrypted audio is empty or too small")
	}

	encryptedData, err := os.ReadFile(encryptedPath)
	if err != nil {
		return fmt.Errorf("read encrypted Spotify audio: %w", err)
	}

	decryptedData := make([]byte, len(encryptedData))

	stream := cipher.NewCTR(block, oneGrabSpotifyIV)
	stream.XORKeyStream(decryptedData, encryptedData)

	decryptedFile, err := os.CreateTemp(
		filepath.Dir(outputPath),
		"onegrab-*.ogg",
	)
	if err != nil {
		return fmt.Errorf("create temporary OGG file: %w", err)
	}

	decryptedPath := decryptedFile.Name()
	defer os.Remove(decryptedPath)

	if _, err := decryptedFile.Write(decryptedData); err != nil {
		decryptedFile.Close()
		return fmt.Errorf("write decrypted Spotify audio: %w", err)
	}

	if err := decryptedFile.Close(); err != nil {
		return errors.New("failed to close decrypted Spotify audio")
	}

	if err := rebuildOneGrabOGG(decryptedPath); err != nil {
		return err
	}

	// Convert/rebuild the final OGG using FFmpeg.
	ffmpegCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(
		ffmpegCtx,
		"ffmpeg",
		"-y",
		"-i", decryptedPath,
		"-c", "copy",
		outputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(outputPath)

		if isDownloadCancelled(err) {
			return err
		}

		return fmt.Errorf(
			"FFmpeg Spotify OGG processing failed: %w: %s",
			err,
			strings.TrimSpace(string(output)),
		)
	}

	return validateOneGrabFile(outputPath)
}

// rebuildOneGrabOGG repairs the OGG headers used by the encrypted
// Spotify audio format before FFmpeg processes the file.
func rebuildOneGrabOGG(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open decrypted OGG file: %w", err)
	}
	defer file.Close()

	patches := map[int64][]byte{
		0:  []byte("OggS"),
		6:  []byte("\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"),
		26: []byte("\x01\x1E\x01vorbis"),
		39: []byte("\x02"),
		40: []byte("\x44\xAC\x00\x00"),
		48: []byte("\x00\xE2\x04\x00"),
		56: []byte("\xB8\x01"),
		58: []byte("OggS"),
		62: []byte("\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"),
	}

	for offset, data := range patches {
		if _, err := file.WriteAt(data, offset); err != nil {
			return fmt.Errorf(
				"repair OGG header at offset %d: %w",
				offset,
				err,
			)
		}
	}

	return nil
}

func saveOneGrabResponse(
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

func validateOneGrabFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return errors.New("onegrab: downloaded file was not created")
	}

	if info.Size() < oneGrabMinFileSize {
		return errors.New("onegrab: downloaded file is empty or too small")
	}

	f, err := os.Open(path)
	if err == nil {
		buf := make([]byte, 512)
		n, _ := f.Read(buf)
		_ = f.Close()
		if n > 0 {
			lower := strings.ToLower(string(buf[:n]))
			if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") || strings.Contains(lower, "{\"error\"") {
				return errors.New("onegrab: downloaded file contains HTML or error payload")
			}
		}
	}

	return nil
}
