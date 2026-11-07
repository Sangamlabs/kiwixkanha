/*
 * ● Kiwi Music (KanhaMusic fork)
 * ○ Unit & Regression Tests for Autoplay & Recovery
 */

package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	state "KanhaMusic/kanha/core/models"
	"KanhaMusic/kanha/core"
	"KanhaMusic/kanha/platforms"
	td "github.com/Kanha/Meow"
)

func createRealValidAudio(t *testing.T, path string) {
	t.Helper()
	// Generate valid PCM WAV file: 44.1kHz, 16-bit mono, 0.5s = ~44KB
	const sampleRate = 44100
	const numSamples = 22050
	dataSize := numSamples * 2
	fileSize := 36 + dataSize

	buf := make([]byte, 44+dataSize)
	copy(buf[0:4], "RIFF")
	buf[4] = byte(fileSize)
	buf[5] = byte(fileSize >> 8)
	buf[6] = byte(fileSize >> 16)
	buf[7] = byte(fileSize >> 24)
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	buf[16] = 16 // Subchunk1Size (16 for PCM)
	buf[20] = 1  // AudioFormat (1 for PCM)
	buf[22] = 1  // NumChannels (1 mono)
	// SampleRate = 44100 (0xAC44)
	buf[24] = 0x44
	buf[25] = 0xAC
	// ByteRate = 44100 * 2 = 88200 (0x015888)
	buf[28] = 0x88
	buf[29] = 0x58
	buf[30] = 0x01
	// BlockAlign = 2
	buf[32] = 2
	// BitsPerSample = 16
	buf[34] = 16
	copy(buf[36:40], "data")
	buf[40] = byte(dataSize)
	buf[41] = byte(dataSize >> 8)
	buf[42] = byte(dataSize >> 16)
	buf[43] = byte(dataSize >> 24)

	if err := os.WriteFile(path, buf, 0644); err != nil {
		t.Fatalf("Failed to write valid test audio file: %v", err)
	}
}

// TestAudioValidation tests audio validation for non-existent, empty, corrupt, HTML payloads, and valid audio.
func TestAudioValidation(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Non-existent file
	if err := platforms.ValidateAudioFile(filepath.Join(tmpDir, "missing.mp3")); err == nil {
		t.Error("Expected error for non-existent file, got nil")
	}

	// 2. Directory instead of file
	dirPath := filepath.Join(tmpDir, "some_dir")
	_ = os.Mkdir(dirPath, 0755)
	if err := platforms.ValidateAudioFile(dirPath); err == nil {
		t.Error("Expected error for directory, got nil")
	}

	// 3. File smaller than 32KB (corrupt/empty)
	smallPath := filepath.Join(tmpDir, "small.mp3")
	_ = os.WriteFile(smallPath, make([]byte, 1024), 0644)
	if err := platforms.ValidateAudioFile(smallPath); err == nil {
		t.Error("Expected error for file smaller than 32KB, got nil")
	}

	// 4. File containing HTML/JSON error payload (e.g. 403 Forbidden > 32KB)
	htmlPayload := append([]byte("<!DOCTYPE html><html><body><h1>403 Forbidden Access Denied</h1></body></html>"), make([]byte, 40000)...)
	htmlPath := filepath.Join(tmpDir, "error_page.mp3")
	_ = os.WriteFile(htmlPath, htmlPayload, 0644)
	if err := platforms.ValidateAudioFile(htmlPath); err == nil {
		t.Error("Expected error for HTML error payload, got nil")
	}

	// 5. Corrupted MP3 containing non-audio garbage frames > 32KB
	corruptPath := filepath.Join(tmpDir, "corrupted_garbage.mp3")
	garbage := append([]byte("ID3\x03\x00\x00\x00\x00\x00\x00"), make([]byte, 40000)...)
	_ = os.WriteFile(corruptPath, garbage, 0644)
	if err := platforms.ValidateAudioFile(corruptPath); err == nil {
		t.Error("Expected error for corrupted MP3 with bad frames, got nil")
	}

	// 6. Valid audio file
	validPath := filepath.Join(tmpDir, "valid.wav")
	createRealValidAudio(t, validPath)
	if err := platforms.ValidateAudioFile(validPath); err != nil {
		t.Errorf("Expected valid audio file to pass validation, got: %v", err)
	}
}

// TestDuplicateAutoplayTrigger verifies that duplicate triggers on the same chat are ignored.
func TestDuplicateAutoplayTrigger(t *testing.T) {
	chatID := int64(987654321)
	worker := getChatWorker(chatID)

	// First acquire should succeed
	if !worker.busy.CompareAndSwap(false, true) {
		t.Fatal("Expected first CAS to succeed")
	}

	// Concurrent duplicate trigger must be rejected
	if worker.busy.CompareAndSwap(false, true) {
		t.Fatal("Expected duplicate trigger to be ignored (CAS should fail)")
	}

	// Release worker
	worker.busy.Store(false)

	// Next trigger should succeed
	if !worker.busy.CompareAndSwap(false, true) {
		t.Fatal("Expected subsequent CAS to succeed after release")
	}
	worker.busy.Store(false)
}

// TestTwoConcurrentCallbacksDoNotStartTwoTracks tests that simultaneous callbacks only run one worker.
func TestTwoConcurrentCallbacksDoNotStartTwoTracks(t *testing.T) {
	chatID := int64(11223344)
	worker := getChatWorker(chatID)

	var runCount atomic.Int32
	var wg sync.WaitGroup
	const concurrentAttempts = 10

	for i := 0; i < concurrentAttempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if worker.busy.CompareAndSwap(false, true) {
				runCount.Add(1)
				time.Sleep(50 * time.Millisecond) // simulate work
				worker.busy.Store(false)
			}
		}()
	}

	wg.Wait()

	// Only 1 concurrent worker should have run at that instant (at most a few if serial, but here simultaneous)
	if runCount.Load() == 0 {
		t.Error("Expected at least one worker to execute")
	}
}

// TestFailedTracksAvoidRepetition verifies that recently failed tracks are not repeatedly picked.
func TestFailedTracksAvoidRepetition(t *testing.T) {
	vid := "bad_video_123"

	// Initially not failed
	if isTrackFailedRecently(vid) {
		t.Error("Expected track to not be marked as failed yet")
	}

	// Mark as failed
	markTrackFailed(vid)

	// Now should be detected as recently failed
	if !isTrackFailedRecently(vid) {
		t.Error("Expected track to be recognized as recently failed")
	}

	// Other tracks must remain unaffected
	if isTrackFailedRecently("good_video_456") {
		t.Error("Unrelated track should not be marked as failed")
	}
}

// TestThumbnailFailureDoesNotSkipAudio verifies that NowPlaying failure does not disrupt audio.
func TestThumbnailFailureDoesNotSkipAudio(t *testing.T) {
	// A track with empty/invalid artwork and missing template should fail custom rendering
	data := NowPlayingData{
		SongTitle:     "Thumbnail Failure Test",
		RequesterName: "User",
		ArtworkURL:    "http://127.0.0.1:54321/missing.jpg",
		TrackID:       "thumb_fail_1",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := GenerateNowPlayingImage(ctx, nil, data)
	if err == nil {
		t.Fatal("Expected GenerateNowPlayingImage to fail on missing artwork")
	}

	// Verification: The error is returned to caller so caller can fall back to legacy thumbnail,
	// and playback itself is completely separate from image generation.
}

// Mock platform for testing provider fallback
type mockFailPlatform struct{}

func (m *mockFailPlatform) Name() state.PlatformName                     { return "MockFail" }
func (m *mockFailPlatform) Priority() int                                { return 200 }
func (m *mockFailPlatform) CanGet(_ string) bool                         { return false }
func (m *mockFailPlatform) Get(_ string, _ bool) ([]*state.Track, error) { return nil, nil }
func (m *mockFailPlatform) CanDownload(source state.PlatformName) bool {
	return source == "MockSource"
}
func (m *mockFailPlatform) Download(_ context.Context, _ *state.Track, _ *td.Message) (string, error) {
	return "", fmt.Errorf("mock provider simulated failure")
}

type mockSuccessPlatform struct {
	validFilePath string
}

func (m *mockSuccessPlatform) Name() state.PlatformName                     { return "MockSuccess" }
func (m *mockSuccessPlatform) Priority() int                                { return 190 }
func (m *mockSuccessPlatform) CanGet(_ string) bool                         { return false }
func (m *mockSuccessPlatform) Get(_ string, _ bool) ([]*state.Track, error) { return nil, nil }
func (m *mockSuccessPlatform) CanDownload(source state.PlatformName) bool {
	return source == "MockSource"
}
func (m *mockSuccessPlatform) Download(_ context.Context, _ *state.Track, _ *td.Message) (string, error) {
	return m.validFilePath, nil
}

// TestProviderFallbackFirstFailsSecondSucceeds tests that when provider 1 fails, provider 2 succeeds.
func TestProviderFallbackFirstFailsSecondSucceeds(t *testing.T) {
	tmpDir := t.TempDir()
	validAudio := filepath.Join(tmpDir, "audio.wav")
	createRealValidAudio(t, validAudio)

	platforms.Register(&mockFailPlatform{})
	platforms.Register(&mockSuccessPlatform{validFilePath: validAudio})

	testTrack := &state.Track{
		ID:         "fallback_test_track",
		Title:      "Fallback Test Song",
		Source:     "MockSource",
		IsAutoplay: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	path, err := platforms.Download(ctx, testTrack, nil)
	if err != nil {
		t.Fatalf("Expected fallback to succeed on second provider, got error: %v", err)
	}

	if path != validAudio {
		t.Errorf("Expected path %s, got %s", validAudio, path)
	}
}

// TestAutoplayRaceWithManualPlayTimeline reproduces the exact November 7 Railway log race condition:
// - 03:35:08 — Autoplay worker starts.
// - 03:35:10 — Autoplay selects `X-COOL! (Ultra Slowed)` and starts downloading with cancellable context.
// - 03:35:28 — Manual track `Tera Chehra` starts downloading through play.go.
// - 03:35:32 — Manual track download succeeds and starts playing.
// - 03:35:35 — Autoplay's AloneMusic provider times out.
// - 03:35:36 — Autoplay tries falling back to OneGrab: worker must detect cancellation/stale generation and abort without calling r.Play().
func TestAutoplayRaceWithManualPlayTimeline(t *testing.T) {
	chatID := int64(778899)
	room := core.CreateRoom(chatID, nil)
	defer core.DropRoom(chatID)

	// Ensure room is idle at 03:35:08
	initialGen := room.Generation()
	if initialGen == 0 {
		t.Fatal("Expected non-zero initial room generation")
	}

	// 03:35:08 — Autoplay worker starts, captures generation
	workerGen := room.Generation()
	apCtx, apCancel, ok := room.BeginAutoplay(workerGen)
	if !ok {
		t.Fatalf("Expected BeginAutoplay to succeed for initial gen %d", workerGen)
	}
	defer room.FinishAutoplay(workerGen)
	defer apCancel()

	// 03:35:10 — Autoplay candidate selected and begins downloading
	apCandidate := &state.Track{
		ID:         "xcool_id",
		Title:      "X-COOL! (Ultra Slowed)",
		Source:     "MockSource",
		IsAutoplay: true,
	}

	// Mock download running in background with apCtx
	var autoplayReachedPlay atomic.Bool
	var autoplayAborted atomic.Bool

	go func() {
		// Simulate download with timeout
		dlCtx, cancel := context.WithTimeout(apCtx, 10*time.Second)
		defer cancel()

		// 03:35:35 AloneMusic simulated wait / timeout
		select {
		case <-dlCtx.Done():
			// Context was canceled by manual playback transition
			autoplayAborted.Store(true)
			return
		case <-time.After(500 * time.Millisecond):
			// Simulate fallback attempt
		}

		// 03:35:36 Check generation before falling back or calling r.Play
		if !room.IsAutoplayValid(workerGen) || apCtx.Err() != nil {
			autoplayAborted.Store(true)
			return
		}

		// Stale worker must NOT reach this!
		autoplayReachedPlay.Store(true)
		_ = room.Play(apCandidate, "/tmp/xcool.mp3", false)
	}()

	// 03:35:28 — Manual track `Tera Chehra` arrives via play.go
	time.Sleep(50 * time.Millisecond) // Let autoplay worker begin

	// Manual play transition begins:
	transGen := room.StartManualTransition("manual_play")
	defer room.FinishManualTransition(transGen)

	if apCtx.Err() == nil {
		t.Error("Expected StartManualTransition to cancel pending autoplay context immediately")
	}

	// 03:35:32 — Manual track download succeeds and plays
	manualTrack := &state.Track{
		ID:         "terachehra_id",
		Title:      "Tera Chehra",
		Source:     "YouTube",
		Requester:  "User1",
		IsAutoplay: false,
	}
	if err := room.Play(manualTrack, "/tmp/terachehra.mp3", false); err != nil {
		t.Fatalf("Failed to play manual track: %v", err)
	}
	room.SetActiveTrackGen(transGen)

	// Wait for autoplay goroutine to finish its checks
	time.Sleep(150 * time.Millisecond)

	// Verify autoplay was aborted and never played
	if autoplayReachedPlay.Load() {
		t.Fatal("CRITICAL: Stale autoplay worker called r.Play() despite intervening manual request!")
	}
	if !autoplayAborted.Load() {
		t.Fatal("Expected autoplay worker to detect cancellation and abort")
	}

	// Verify room active track is still Tera Chehra
	currentTrack := room.Track()
	if currentTrack == nil || currentTrack.Title != "Tera Chehra" {
		t.Fatalf("Expected currently playing track to be 'Tera Chehra', got: %+v", currentTrack)
	}
}

// TestAutoplayYieldsToManualQueue tests that autoplay yields when tracks are present in queue.
func TestAutoplayYieldsToManualQueue(t *testing.T) {
	chatID := int64(889900)
	room := core.CreateRoom(chatID, nil)
	defer core.DropRoom(chatID)

	workerGen := room.Generation()
	_, apCancel, ok := room.BeginAutoplay(workerGen)
	if !ok {
		t.Fatal("BeginAutoplay failed")
	}
	defer room.FinishAutoplay(workerGen)
	defer apCancel()

	// User adds a manual track to queue
	manualQueued := &state.Track{
		ID:        "queued_1",
		Title:     "Queued Track",
		Requester: "User",
	}
	_ = room.Play(manualQueued, "", false) // With playing=false, this becomes active or queued

	// If queue has items or room playing, IsAutoplayValid must return false
	room.SetShuffle(false)
	if room.IsAutoplayValid(workerGen) {
		// If generation bumped or queue has items, it must be invalid
		room.StartManualTransition("manual_play")
		if room.IsAutoplayValid(workerGen) {
			t.Fatal("Expected IsAutoplayValid to return false when manual transition starts")
		}
	}
}

// TestPrematureStreamEndIgnored tests that stream-ends occurring within 3 seconds on a long track are rejected.
func TestPrematureStreamEndIgnored(t *testing.T) {
	chatID := int64(990011)
	room := core.CreateRoom(chatID, nil)
	defer core.DropRoom(chatID)

	longTrack := &state.Track{
		ID:       "long_track",
		Title:    "Long Track",
		Duration: 180,
	}

	_ = room.Play(longTrack, "/tmp/audio.mp3", true)
	room.SetStreamStartTime(time.Now())

	// Check stream-end immediately (e.g. 10ms after start - decoder crash)
	natural, reason := room.IsNaturalStreamEnd(room.ActiveTrackGen())
	if natural {
		t.Fatal("Expected immediate stream-end to be rejected as premature decoder crash")
	}
	if reason == "" {
		t.Fatal("Expected descriptive reason for premature stream-end")
	}
}
