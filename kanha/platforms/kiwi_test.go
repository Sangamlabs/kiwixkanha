/*
 * ● Kiwi Music (KanhaMusic fork)
 * ○ Unit Tests for Kiwi Music API Provider Platform
 */

package platforms

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"KanhaMusic/config"
	state "KanhaMusic/kanha/core/models"
)

// 1. TestKiwiProviderRegistration verifies provider registration.
func TestKiwiProviderRegistration(t *testing.T) {
	p, ok := GetPlatform(PlatformKiwiMusic)
	if !ok || p == nil {
		t.Fatal("KiwiMusicPlatform is not registered in the platforms registry")
	}
	if p.Name() != PlatformKiwiMusic {
		t.Errorf("Expected platform name %q, got %q", PlatformKiwiMusic, p.Name())
	}
}

// 2. TestKiwiPriorityEquals100 verifies priority equals 100.
func TestKiwiPriorityEquals100(t *testing.T) {
	p, ok := GetPlatform(PlatformKiwiMusic)
	if !ok || p == nil {
		t.Fatal("KiwiMusicPlatform not found")
	}
	if p.Priority() != 100 {
		t.Errorf("Expected KiwiMusic priority 100, got %d", p.Priority())
	}
}

// 3. TestKiwiMissingConfigDisablesProvider verifies missing configuration disables the provider.
func TestKiwiMissingConfigDisablesProvider(t *testing.T) {
	origURL, origKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origURL, origKey
	}()

	config.KiwiMusicAPIURL = ""
	config.KiwiMusicAPIKey = ""

	p, ok := GetPlatform(PlatformKiwiMusic)
	if !ok || p == nil {
		t.Fatal("KiwiMusicPlatform not found")
	}

	if p.CanDownload(PlatformYouTube) {
		t.Error("CanDownload(PlatformYouTube) should be false when config is missing")
	}
	if p.CanDownload(PlatformKiwiMusic) {
		t.Error("CanDownload(PlatformKiwiMusic) should be false when config is missing")
	}
	if p.CanGet("kiwi:test") {
		t.Error("CanGet should be false when config is missing")
	}

	if _, err := p.Get("test", false); err == nil {
		t.Error("Get() should return an error when config is missing")
	}

	track := &state.Track{
		ID:     "test_id",
		Title:  "Test Song",
		Source: PlatformYouTube,
	}
	if _, err := p.Download(context.Background(), track, nil); err == nil {
		t.Error("Download() should return an error when config is missing")
	}
}

// 4. TestKiwiSearchSuccess verifies search success.
func TestKiwiSearchSuccess(t *testing.T) {
	origURL, origKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origURL, origKey
	}()

	const expectedAPIKey = "secret_kiwi_test_key_4"
	var receivedAPIKey, receivedAccept, receivedQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/search" {
			receivedAPIKey = r.Header.Get("X-API-Key")
			receivedAccept = r.Header.Get("Accept")
			receivedQuery = r.URL.Query().Get("q")

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"results": [
					{
						"id": "kiwi_track_001",
						"title": "Believer",
						"artist": "Imagine Dragons",
						"duration": 204,
						"thumbnail": "https://cdn.example.com/art.jpg"
					}
				]
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	config.KiwiMusicAPIURL = server.URL
	config.KiwiMusicAPIKey = expectedAPIKey

	p, ok := GetPlatform(PlatformKiwiMusic)
	if !ok || p == nil {
		t.Fatal("KiwiMusicPlatform not found")
	}

	tracks, err := p.Get("Believer", false)
	if err != nil {
		t.Fatalf("Search failed unexpectedly: %v", err)
	}

	if receivedAPIKey != expectedAPIKey {
		t.Errorf("Expected X-API-Key %q, got %q", expectedAPIKey, receivedAPIKey)
	}
	if !strings.Contains(receivedAccept, "application/json") {
		t.Errorf("Expected Accept header to contain application/json, got %q", receivedAccept)
	}
	if receivedQuery != "Believer" {
		t.Errorf("Expected search query %q, got %q", "Believer", receivedQuery)
	}

	if len(tracks) != 1 {
		t.Fatalf("Expected 1 track, got %d", len(tracks))
	}

	tr := tracks[0]
	if tr.ID != "kiwi_track_001" {
		t.Errorf("Expected ID kiwi_track_001, got %q", tr.ID)
	}
	if tr.Title != "Believer" {
		t.Errorf("Expected Title Believer, got %q", tr.Title)
	}
	if tr.Artist != "Imagine Dragons" {
		t.Errorf("Expected Artist Imagine Dragons, got %q", tr.Artist)
	}
	if tr.Duration != 204 {
		t.Errorf("Expected Duration 204, got %d", tr.Duration)
	}
	if tr.Artwork != "https://cdn.example.com/art.jpg" {
		t.Errorf("Expected Artwork URL, got %q", tr.Artwork)
	}
	if tr.Source != PlatformKiwiMusic {
		t.Errorf("Expected Source %q, got %q", PlatformKiwiMusic, tr.Source)
	}
	if tr.Video {
		t.Errorf("Expected Video false, got %v", tr.Video)
	}
}

// 5. TestKiwiSearchEmptyResults verifies search with empty results.
func TestKiwiSearchEmptyResults(t *testing.T) {
	origURL, origKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origURL, origKey
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results": []}`))
	}))
	defer server.Close()

	config.KiwiMusicAPIURL = server.URL
	config.KiwiMusicAPIKey = "dummy_key"

	p, _ := GetPlatform(PlatformKiwiMusic)
	tracks, err := p.Get("NonexistentSong12345", false)
	if err != nil {
		t.Fatalf("Expected no error for empty search, got: %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("Expected 0 tracks, got %d", len(tracks))
	}
}

// 6. TestKiwiSearchHTTPError verifies search with HTTP error.
func TestKiwiSearchHTTPError(t *testing.T) {
	origURL, origKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origURL, origKey
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`Backend service unavailable`))
	}))
	defer server.Close()

	config.KiwiMusicAPIURL = server.URL
	config.KiwiMusicAPIKey = "dummy_key"

	p, _ := GetPlatform(PlatformKiwiMusic)
	_, err := p.Get("AnySong", false)
	if err == nil {
		t.Fatal("Expected error on HTTP 503 response, got nil")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("Expected error to mention 503 status code, got: %v", err)
	}
}

// 7. TestKiwiSearchInvalidJSON verifies search handling of invalid JSON.
func TestKiwiSearchInvalidJSON(t *testing.T) {
	origURL, origKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origURL, origKey
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not valid json!!!`))
	}))
	defer server.Close()

	config.KiwiMusicAPIURL = server.URL
	config.KiwiMusicAPIKey = "dummy_key"

	p, _ := GetPlatform(PlatformKiwiMusic)
	_, err := p.Get("AnySong", false)
	if err == nil {
		t.Fatal("Expected error for invalid JSON response, got nil")
	}
}

// 8. TestKiwiTrackIDValidation verifies track ID validation.
func TestKiwiTrackIDValidation(t *testing.T) {
	origURL, origKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origURL, origKey
	}()

	config.KiwiMusicAPIURL = "http://127.0.0.1:9999"
	config.KiwiMusicAPIKey = "dummy_key"

	p, _ := GetPlatform(PlatformKiwiMusic)

	invalidCases := []string{
		"",
		"   ",
		".",
		"..",
		"../../etc/passwd",
		"dir\\file",
		"foo/bar",
	}

	for _, invalidID := range invalidCases {
		track := &state.Track{
			ID:     invalidID,
			Title:  "Track With Bad ID",
			Source: PlatformKiwiMusic,
		}
		_, err := p.Download(context.Background(), track, nil)
		if err == nil {
			t.Errorf("Expected error for invalid track ID %q, got nil", invalidID)
		}
	}
}

// 9. TestKiwiResolveSuccess verifies resolve endpoint success and media download.
func TestKiwiResolveSuccess(t *testing.T) {
	origURL, origKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origURL, origKey
	}()

	tempDir := t.TempDir()
	validAudioPath := filepath.Join(tempDir, "test_audio.wav")
	createTestAudio(t, validAudioPath, 180.0)
	validAudioBytes, err := os.ReadFile(validAudioPath)
	if err != nil {
		t.Fatalf("Failed to read test audio: %v", err)
	}

	var mediaServed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/resolve/track_res_001":
			if r.Header.Get("X-API-Key") != "valid_key" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			// Return hosted URL pointing to media endpoint
			_, _ = fmt.Fprintf(w, `{"url": "http://%s/media/stream_audio.mp3"}`, r.Host)
		case "/media/stream_audio.mp3":
			mediaServed = true
			w.Header().Set("Content-Type", "audio/mpeg")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(validAudioBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	config.KiwiMusicAPIURL = server.URL
	config.KiwiMusicAPIKey = "valid_key"

	p, _ := GetPlatform(PlatformKiwiMusic)
	track := &state.Track{
		ID:       "track_res_001",
		Title:    "Resolve Success Track",
		Duration: 180,
		Source:   PlatformKiwiMusic,
	}

	outPath, err := p.Download(context.Background(), track, nil)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	defer os.Remove(outPath)

	if !mediaServed {
		t.Error("Hosted media was not served")
	}

	if !fileExists(outPath) {
		t.Errorf("Downloaded audio file does not exist at %s", outPath)
	}
}

// 10. TestKiwiStreamRedirectHandling verifies safe handling of stream redirects.
func TestKiwiStreamRedirectHandling(t *testing.T) {
	origURL, origKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origURL, origKey
	}()

	tempDir := t.TempDir()
	validAudioPath := filepath.Join(tempDir, "test_audio.wav")
	createTestAudio(t, validAudioPath, 180.0)
	validAudioBytes, err := os.ReadFile(validAudioPath)
	if err != nil {
		t.Fatalf("Failed to read test audio: %v", err)
	}

	var redirected bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/resolve/track_redir_002":
			// Simulate resolve endpoint not returning URL, triggering stream fallback
			http.NotFound(w, r)
		case "/v1/stream/track_redir_002":
			// Responds with 302 Found redirect to hosted audio
			redirected = true
			http.Redirect(w, r, "/cdn/audio_redirected.mp3", http.StatusFound)
		case "/cdn/audio_redirected.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(validAudioBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	config.KiwiMusicAPIURL = server.URL
	config.KiwiMusicAPIKey = "valid_key"

	p, _ := GetPlatform(PlatformKiwiMusic)
	track := &state.Track{
		ID:       "track_redir_002",
		Title:    "Redirect Track",
		Duration: 180,
		Source:   PlatformYouTube,
	}

	outPath, err := p.Download(context.Background(), track, nil)
	if err != nil {
		t.Fatalf("Download via stream redirect failed: %v", err)
	}
	defer os.Remove(outPath)

	if !redirected {
		t.Error("Redirect was not requested/followed")
	}

	if !fileExists(outPath) {
		t.Errorf("Downloaded file does not exist at %s", outPath)
	}
}

// 11. TestKiwiInvalidHostedURL verifies rejection of unsafe or invalid hosted URLs.
func TestKiwiInvalidHostedURL(t *testing.T) {
	origURL, origKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origURL, origKey
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Returns an unsafe / loopback URL on a foreign host (SSRF vulnerability attempt)
		_, _ = w.Write([]byte(`{"url": "http://10.0.0.1/private.mp3"}`))
	}))
	defer server.Close()

	config.KiwiMusicAPIURL = server.URL
	config.KiwiMusicAPIKey = "valid_key"

	p, _ := GetPlatform(PlatformKiwiMusic)
	track := &state.Track{
		ID:       "track_unsafe_003",
		Title:    "Unsafe URL Track",
		Duration: 180,
		Source:   PlatformKiwiMusic,
	}

	_, err := p.Download(context.Background(), track, nil)
	if err == nil {
		t.Fatal("Expected error when backend returns an unsafe hosted URL, got nil")
	}
}

// 12. TestKiwiContextCancellation verifies context cancellation.
func TestKiwiContextCancellation(t *testing.T) {
	origURL, origKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origURL, origKey
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hang until context is cancelled
		time.Sleep(2 * time.Second)
	}))
	defer server.Close()

	config.KiwiMusicAPIURL = server.URL
	config.KiwiMusicAPIKey = "valid_key"

	p, _ := GetPlatform(PlatformKiwiMusic)
	track := &state.Track{
		ID:       "track_cancel_004",
		Title:    "Cancelled Track",
		Duration: 180,
		Source:   PlatformKiwiMusic,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := p.Download(ctx, track, nil)
	if err == nil {
		t.Fatal("Expected error on cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) && !isDownloadCancelled(err) {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}
}

// 13. TestKiwiFailedDownloadCleanup verifies partial/corrupt files are cleaned up on failure.
func TestKiwiFailedDownloadCleanup(t *testing.T) {
	origURL, origKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origURL, origKey
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/resolve/track_corrupt_005":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"url": "http://%s/media/error_page.mp3"}`, r.Host)
		case "/media/error_page.mp3":
			// Return HTML error payload instead of valid audio
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><body><h1>502 Bad Gateway</h1></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	config.KiwiMusicAPIURL = server.URL
	config.KiwiMusicAPIKey = "valid_key"

	p, _ := GetPlatform(PlatformKiwiMusic)
	track := &state.Track{
		ID:       "track_corrupt_005",
		Title:    "Corrupt File Track",
		Duration: 180,
		Source:   PlatformKiwiMusic,
	}

	targetPath := getPath(track, ".mp3")
	_ = os.Remove(targetPath)

	_, err := p.Download(context.Background(), track, nil)
	if err == nil {
		t.Fatal("Expected download error for HTML payload, got nil")
	}

	if fileExists(targetPath) {
		_ = os.Remove(targetPath)
		t.Errorf("Partial/corrupt file was not cleaned up at %s", targetPath)
	}
}

// 14. TestKiwiAPIKeyNeverLogged verifies the API key is never logged or exposed in errors.
func TestKiwiAPIKeyNeverLogged(t *testing.T) {
	origURL, origKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origURL, origKey
	}()

	const secretKey = "super_secret_kiwi_key_xyz_777"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		// Server returns an error mentioning the secret key
		_, _ = fmt.Fprintf(w, "Access denied for token %s", secretKey)
	}))
	defer server.Close()

	config.KiwiMusicAPIURL = server.URL
	config.KiwiMusicAPIKey = secretKey

	p, _ := GetPlatform(PlatformKiwiMusic)
	_, err := p.Get("TestSecretQuery", false)
	if err == nil {
		t.Fatal("Expected error on 403 response, got nil")
	}

	errMsg := err.Error()
	if strings.Contains(errMsg, secretKey) {
		t.Fatalf("CRITICAL SECURITY FLAW: API key %q leaked in error message: %s", secretKey, errMsg)
	}
	if !strings.Contains(errMsg, "***REDACTED***") {
		t.Errorf("Expected error to contain redacted token indicator, got: %s", errMsg)
	}
}

// 15. TestKiwiAttemptedBeforeAloneMusic verifies Kiwi is ordered before AloneMusic.
func TestKiwiAttemptedBeforeAloneMusic(t *testing.T) {
	platforms := ordered()

	t.Logf("=== Registered Platforms in Priority Order ===")
	for idx, p := range platforms {
		t.Logf("[%d] Name: %-15s | Priority: %d", idx, p.Name(), p.Priority())
	}

	kiwiIndex := -1
	aloneIndex := -1
	oneGrabIndex := -1

	for idx, p := range platforms {
		if p.Name() == PlatformKiwiMusic {
			kiwiIndex = idx
		} else if p.Name() == PlatformAloneMusic {
			aloneIndex = idx
		} else if p.Name() == PlatformOneGrab {
			oneGrabIndex = idx
		}
	}

	if kiwiIndex == -1 {
		t.Fatal("PlatformKiwiMusic not found in ordered() platforms")
	}
	if aloneIndex == -1 {
		t.Fatal("PlatformAloneMusic not found in ordered() platforms")
	}
	if oneGrabIndex == -1 {
		t.Fatal("PlatformOneGrab not found in ordered() platforms")
	}

	if kiwiIndex >= aloneIndex {
		t.Errorf("KiwiMusic (idx=%d) must be ordered before AloneMusic (idx=%d)", kiwiIndex, aloneIndex)
	}

	// Deterministic ordering check: AloneMusic must be ordered before OneGrab
	if aloneIndex >= oneGrabIndex {
		t.Errorf("AloneMusic (idx=%d) must be ordered before OneGrab (idx=%d)", aloneIndex, oneGrabIndex)
	}
}

// 16. TestKiwiFailedDownloadFallsBackToAloneMusic verifies that when Kiwi fails,
// registry Download seamlessly falls back to AloneMusic.
func TestKiwiFailedDownloadFallsBackToAloneMusic(t *testing.T) {
	origKiwiURL, origKiwiKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	origAloneURL, origAloneKey := config.AloneMusicAPIURL, config.AloneMusicAPIKey
	origProbe := os.Getenv("SKIP_AUDIO_CODEC_PROBE")
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origKiwiURL, origKiwiKey
		config.AloneMusicAPIURL, config.AloneMusicAPIKey = origAloneURL, origAloneKey
		_ = os.Setenv("SKIP_AUDIO_CODEC_PROBE", origProbe)
	}()

	_ = os.Setenv("SKIP_AUDIO_CODEC_PROBE", "1")

	tempDir := t.TempDir()
	validAudioPath := filepath.Join(tempDir, "alone_audio.wav")
	createTestAudio(t, validAudioPath, 180.0)
	validAudioBytes, err := os.ReadFile(validAudioPath)
	if err != nil {
		t.Fatalf("Failed to read test audio: %v", err)
	}

	// Kiwi server: always fails (HTTP 500)
	kiwiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`Kiwi backend internal failure`))
	}))
	defer kiwiServer.Close()

	// AloneMusic server: succeeds and serves valid audio
	var aloneCalled bool
	aloneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/download") {
			aloneCalled = true
			w.Header().Set("Content-Type", "audio/mpeg")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(validAudioBytes)
			return
		}
		http.NotFound(w, r)
	}))
	defer aloneServer.Close()

	config.KiwiMusicAPIURL = kiwiServer.URL
	config.KiwiMusicAPIKey = "kiwi_test_key"
	config.AloneMusicAPIURL = aloneServer.URL
	config.AloneMusicAPIKey = "alone_test_key"

	track := &state.Track{
		ID:       "fallback_test_track_016",
		Title:    "Track Testing Kiwi Fallback",
		Duration: 180,
		Source:   PlatformYouTube,
	}

	targetPath := getPath(track, ".mp3")
	_ = os.Remove(targetPath)

	outPath, err := Download(context.Background(), track, nil)
	if err != nil {
		t.Fatalf("Download failed unexpectedly during fallback: %v", err)
	}
	defer os.Remove(outPath)

	if !aloneCalled {
		t.Error("AloneMusic was not called as fallback when KiwiMusic failed")
	}

	if !fileExists(outPath) {
		t.Errorf("Downloaded fallback file does not exist at %s", outPath)
	}
}

// 17. TestKiwiControlledIntegrationAuthorizedTrack runs a controlled integration test
// validating search, track resolution, download, and ValidateTrackAudio using an authorized track.
func TestKiwiControlledIntegrationAuthorizedTrack(t *testing.T) {
	origKiwiURL, origKiwiKey := config.KiwiMusicAPIURL, config.KiwiMusicAPIKey
	origProbe := os.Getenv("SKIP_AUDIO_CODEC_PROBE")
	defer func() {
		config.KiwiMusicAPIURL, config.KiwiMusicAPIKey = origKiwiURL, origKiwiKey
		_ = os.Setenv("SKIP_AUDIO_CODEC_PROBE", origProbe)
	}()

	_ = os.Setenv("SKIP_AUDIO_CODEC_PROBE", "1")

	tempDir := t.TempDir()
	validAudioPath := filepath.Join(tempDir, "authorized_track.wav")
	createTestAudio(t, validAudioPath, 180.0)
	validAudioBytes, err := os.ReadFile(validAudioPath)
	if err != nil {
		t.Fatalf("Failed to read test audio: %v", err)
	}

	const authorizedAPIKey = "authorized_kiwi_jwt_token_2026"
	const authorizedTrackID = "kiwi_auth_trk_888"
	var searchCalled, resolveCalled, mediaDownloaded bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify API Key on Kiwi backend API endpoints
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			if r.Header.Get("X-API-Key") != authorizedAPIKey {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
				return
			}
		}

		switch r.URL.Path {
		case "/v1/search":
			searchCalled = true
			if r.URL.Query().Get("q") != "Test Authorized Song" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"results": [
					{
						"id": "` + authorizedTrackID + `",
						"title": "Test Authorized Song",
						"artist": "Verified Artist",
						"duration": 180,
						"thumbnail": "https://cdn.yukysbox.site/thumb.jpg"
					}
				]
			}`))

		case "/v1/resolve/" + authorizedTrackID:
			resolveCalled = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{"url": "http://%s/storage/yukysbox/%s.mp3"}`, r.Host, authorizedTrackID)

		case "/storage/yukysbox/" + authorizedTrackID + ".mp3":
			mediaDownloaded = true
			w.Header().Set("Content-Type", "audio/mpeg")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(validAudioBytes)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	config.KiwiMusicAPIURL = server.URL
	config.KiwiMusicAPIKey = authorizedAPIKey

	p, ok := GetPlatform(PlatformKiwiMusic)
	if !ok || p == nil {
		t.Fatal("PlatformKiwiMusic not found")
	}

	// Step A: Search for authorized query
	tracks, err := p.Get("Test Authorized Song", false)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(tracks) != 1 || tracks[0].ID != authorizedTrackID {
		t.Fatalf("Expected 1 track with ID %s, got %+v", authorizedTrackID, tracks)
	}
	if !searchCalled {
		t.Error("Search endpoint was not called")
	}

	// Step B: Controlled download & validation of the authorized track
	downloadTrack := tracks[0]
	targetPath := getPath(downloadTrack, ".mp3")
	_ = os.Remove(targetPath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	downloadedPath, err := p.Download(ctx, downloadTrack, nil)
	if err != nil {
		t.Fatalf("Controlled download failed: %v", err)
	}
	defer os.Remove(downloadedPath)

	if !resolveCalled {
		t.Error("Resolve endpoint was not called")
	}
	if !mediaDownloaded {
		t.Error("Media storage was not downloaded")
	}

	// Step C: Validate audio file existence and integrity
	if !fileExists(downloadedPath) {
		t.Fatalf("Downloaded file does not exist at %s", downloadedPath)
	}

	if err := ValidateTrackAudio(downloadedPath, downloadTrack); err != nil {
		t.Fatalf("ValidateTrackAudio failed on authorized audio: %v", err)
	}

	t.Logf("Controlled integration test PASSED: audio downloaded and validated at %s", downloadedPath)
}
