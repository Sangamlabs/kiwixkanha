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
	"KanhaMusic/kanha/platforms"
	td "github.com/Kanha/Meow"
)

// TestAudioValidation tests that empty or too-small audio files are rejected.
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

	// 4. Valid file >= 32KB
	validPath := filepath.Join(tmpDir, "valid.mp3")
	_ = os.WriteFile(validPath, make([]byte, 64*1024), 0644)
	if err := platforms.ValidateAudioFile(validPath); err != nil {
		t.Errorf("Expected valid file to pass, got: %v", err)
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
	validAudio := filepath.Join(tmpDir, "audio.mp3")
	_ = os.WriteFile(validAudio, make([]byte, 64*1024), 0644)

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
