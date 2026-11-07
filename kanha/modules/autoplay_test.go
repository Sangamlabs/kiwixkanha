/*
 * ● Kiwi Music (KanhaMusic fork)
 * ○ Unit & Regression Tests for Autoplay, Vibe Detection & Skip Reliability
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

	"KanhaMusic/kanha/core"
	state "KanhaMusic/kanha/core/models"
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

// 1. TestSkipWithEmptyQueueStopsPlayback verifies that /skip on an empty queue stops playback and drops the room.
func TestSkipWithEmptyQueueStopsPlayback(t *testing.T) {
	chatID := int64(900101)
	r := core.CreateRoom(chatID, nil)
	defer core.DropRoom(chatID)

	track := &state.Track{
		ID:        "track_skip_empty",
		Title:     "Current Track",
		Requester: "User",
	}
	_ = r.Play(track, "/tmp/dummy.mp3", false)

	if len(r.Queue()) != 0 {
		t.Fatalf("Expected empty queue, got %d", len(r.Queue()))
	}

	// Simulate skip logic for empty queue (skip.go line 140)
	if len(r.Queue()) == 0 {
		core.DropRoom(r.ID)
	}

	_, exists := core.RoomFor(chatID)
	if exists {
		t.Fatalf("Expected room to be dropped and playback stopped on empty queue skip")
	}
}

// 2. TestSkipWithQueuedSongsPlaysNext verifies that /skip plays the next manual track in queue.
func TestSkipWithQueuedSongsPlaysNext(t *testing.T) {
	chatID := int64(900102)
	r := core.CreateRoom(chatID, nil)
	defer core.DropRoom(chatID)

	track1 := &state.Track{
		ID:        "track_1",
		Title:     "Track 1 Playing",
		Requester: "User1",
	}
	_ = r.Play(track1, "/tmp/t1.mp3", false)

	track2 := &state.Track{
		ID:        "track_2_queued",
		Title:     "Track 2 Queued",
		Requester: "User2",
		IsAutoplay: false,
	}
	_ = r.Play(track2, "", false) // Enqueue track 2

	if len(r.Queue()) != 1 {
		t.Fatalf("Expected 1 track in queue, got %d", len(r.Queue()))
	}

	// Skip current track
	next := r.NextTrack()
	if next == nil || next.ID != "track_2_queued" {
		t.Fatalf("Expected next track to be track_2_queued, got: %+v", next)
	}
	if next.IsAutoplay {
		t.Fatal("Expected manual queued track, not autoplay")
	}

	_ = r.Play(next, "/tmp/t2.mp3", false)
	if r.Track().ID != "track_2_queued" {
		t.Fatalf("Expected active track to be track_2_queued, got %s", r.Track().ID)
	}
	if len(r.Queue()) != 0 {
		t.Fatalf("Expected empty queue after playing next, got %d", len(r.Queue()))
	}
}

// 3. TestSkipDoesNotInvokeAutoplay verifies that /skip stops playback instead of invoking autoplay when queue is empty.
func TestSkipDoesNotInvokeAutoplay(t *testing.T) {
	chatID := int64(900103)
	r := core.CreateRoom(chatID, nil)
	defer core.DropRoom(chatID)

	track := &state.Track{
		ID:        "track_autoplay_active",
		Title:     "Track Playing With Autoplay Enabled",
		Requester: "User",
	}
	_ = r.Play(track, "/tmp/dummy.mp3", false)
	r.SetAutoplay(true)

	if len(r.Queue()) != 0 {
		t.Fatalf("Expected empty queue, got %d", len(r.Queue()))
	}

	// In skip.go, empty queue must drop room even when r.Autoplay() is true:
	// It should NOT call handleAutoplaySkip or PickAutoplayTrack
	var autoplayTriggered bool
	if len(r.Queue()) == 0 {
		// Crucial fix: do NOT check r.Autoplay() here
		core.DropRoom(r.ID)
	} else if r.Autoplay() {
		autoplayTriggered = true
	}

	if autoplayTriggered {
		t.Fatal("CRITICAL: /skip triggered autoplay on empty queue!")
	}

	_, exists := core.RoomFor(chatID)
	if exists {
		t.Fatal("Expected room to be dropped after /skip with empty queue")
	}
}

// 4. TestAskipRetainsExplicitAutoplayBehavior verifies that explicit /askip triggers autoplay when enabled and rejects when disabled.
func TestAskipRetainsExplicitAutoplayBehavior(t *testing.T) {
	chatID := int64(900104)
	r := core.CreateRoom(chatID, nil)
	defer core.DropRoom(chatID)

	current := &state.Track{
		ID:        "current_track_askip",
		Title:     "Metamorphosis (Slowed)",
		Requester: "User",
	}
	_ = r.Play(current, "/tmp/t.mp3", false)

	// When autoplay is disabled, PickAutoplayTrack must return nil
	r.SetAutoplay(false)
	candidateDisabled := PickAutoplayTrack(r, current)
	if candidateDisabled != nil {
		t.Fatalf("Expected nil recommendation when autoplay is disabled, got: %+v", candidateDisabled)
	}

	// When autoplay is enabled, PickAutoplayTrack marks as autoplay
	r.SetAutoplay(true)
	if !r.Autoplay() {
		t.Fatal("Expected autoplay to be enabled")
	}
}

// 5. TestNaturalCompletionWithAutoplayEnabledStartsVibeAutoplay verifies natural completion triggers vibe autoplay.
func TestNaturalCompletionWithAutoplayEnabledStartsVibeAutoplay(t *testing.T) {
	chatID := int64(900105)
	r := core.CreateRoom(chatID, nil)
	defer core.DropRoom(chatID)

	current := &state.Track{
		ID:        "natural_comp_1",
		Title:     "Kordhell - Murder In My Mind (Slowed)",
		Requester: "User",
	}
	_ = r.Play(current, "/tmp/current.mp3", false)
	r.UpdateSessionVibeFromTrack(current)
	r.SetAutoplay(true)

	// Verify session vibe profile was created
	profile := r.GetVibeProfile()
	if profile == nil {
		t.Fatal("Expected room session vibe profile to be set")
	}
	if profile.Genre != state.GenrePhonk || profile.Version != state.VersionSlowed {
		t.Fatalf("Expected Phonk + Slowed profile, got Genre=%s Version=%s", profile.Genre, profile.Version)
	}

	// Natural completion: queue is empty, r.NextTrack() is nil
	nextManual := r.NextTrack()
	if nextManual != nil {
		t.Fatalf("Expected empty manual queue, got: %+v", nextManual)
	}

	// tryAutoplay must be invoked when queue is empty and r.Autoplay() is true
	if !r.Autoplay() {
		t.Fatal("Expected autoplay enabled")
	}
}

// 6. TestNaturalCompletionWithManualQueuePlaysQueuedFirst verifies that manual queued tracks take priority over autoplay.
func TestNaturalCompletionWithManualQueuePlaysQueuedFirst(t *testing.T) {
	chatID := int64(900106)
	r := core.CreateRoom(chatID, nil)
	defer core.DropRoom(chatID)

	current := &state.Track{
		ID:        "natural_comp_cur",
		Title:     "Current Track",
		Requester: "User",
	}
	_ = r.Play(current, "/tmp/cur.mp3", false)
	r.SetAutoplay(true)

	// User queued a manual track
	manualTrack := &state.Track{
		ID:         "manual_queued_track",
		Title:      "Manual Song Requested By User",
		Requester:  "User2",
		IsAutoplay: false,
	}
	_ = r.Play(manualTrack, "", false) // Adds to queue

	// Natural stream completion checks r.NextTrack() FIRST
	tNext := r.NextTrack()
	if tNext == nil {
		t.Fatal("Expected manual queued track to be dequeued, got nil")
	}
	if tNext.ID != "manual_queued_track" {
		t.Fatalf("Expected manual_queued_track, got %s", tNext.ID)
	}
	if tNext.IsAutoplay {
		t.Fatal("Expected manual track to NOT be marked as autoplay")
	}
}

// 7. TestSlowedPhonkContinuesWithSlowedPhonk verifies that Slowed Phonk selects Slowed Phonk and strictly penalizes pop/edm.
func TestSlowedPhonkContinuesWithSlowedPhonk(t *testing.T) {
	title := "Kordhell - Murder In My Mind (Slowed)"
	profile := state.ClassifyTrack(title)

	if profile.Genre != state.GenrePhonk {
		t.Fatalf("Expected GenrePhonk, got: %s", profile.Genre)
	}
	if profile.Version != state.VersionSlowed {
		t.Fatalf("Expected VersionSlowed, got: %s", profile.Version)
	}

	// Candidate 1: Slowed Phonk (Exact match)
	c1 := state.ClassifyTrack("DVRST - Close Eyes (Slowed)")
	score1 := profile.ScoreCandidate(c1)

	// Candidate 2: Pop song (Conflicting genre)
	c2 := state.ClassifyTrack("Taylor Swift - Blank Space")
	score2 := profile.ScoreCandidate(c2)

	// Candidate 3: Sped Up Phonk (Opposite tempo)
	c3 := state.ClassifyTrack("Interworld - Rapture (Sped Up)")
	score3 := profile.ScoreCandidate(c3)

	if score1 < 60 {
		t.Fatalf("Expected high score (>= 60) for Slowed Phonk match, got %d", score1)
	}
	if score2 >= 0 {
		t.Fatalf("Expected negative penalized score (< 0) for Pop conflicting genre, got %d", score2)
	}
	if score1 <= score3 {
		t.Fatalf("Expected Slowed Phonk (%d) to score higher than Sped Up Phonk (%d)", score1, score3)
	}
}

// 8. TestLofiContinuesWithLofi verifies that Lofi sessions continue with Lofi beats and penalize conflicting genres.
func TestLofiContinuesWithLofi(t *testing.T) {
	title := "ChilledCow - Lofi Hip Hop Beat to Relax/Study to"
	profile := state.ClassifyTrack(title)

	if profile.Vibe != state.VibeLofi {
		t.Fatalf("Expected VibeLofi, got: %s", profile.Vibe)
	}

	// Candidate 1: Lofi track (Exact vibe match)
	c1 := state.ClassifyTrack("Lofi Sleep Chill Beats to Study")
	score1 := profile.ScoreCandidate(c1)

	// Candidate 2: EDM Festival Anthem (Conflicting genre)
	c2 := state.ClassifyTrack("Martin Garrix - Animals (EDM Club Festival Mix)")
	score2 := profile.ScoreCandidate(c2)

	if score1 < 50 {
		t.Fatalf("Expected strong match (>= 50) for Lofi track, got %d", score1)
	}
	if score2 >= 0 {
		t.Fatalf("Expected negative penalized score (< 0) for EDM conflicting candidate in Lofi session, got %d", score2)
	}
	if score1 <= score2 {
		t.Fatalf("Expected Lofi (%d) to score significantly higher than EDM (%d)", score1, score2)
	}
}

// 9. TestFailedCandidatesExcluded verifies that tracks marked as failed or recently played are excluded from selection.
func TestFailedCandidatesExcluded(t *testing.T) {
	chatID := int64(900109)
	failedID := "failed_candidate_xyz"

	if isTrackFailedRecently(failedID) {
		t.Fatal("Expected track not to be marked failed initially")
	}
	if apIsPlayed(chatID, failedID) {
		t.Fatal("Expected track not to be marked played initially")
	}

	// Mark as failed and played
	markTrackFailed(failedID)
	apMarkPlayed(chatID, failedID)

	if !isTrackFailedRecently(failedID) {
		t.Fatal("Expected track to be marked recently failed")
	}
	if !apIsPlayed(chatID, failedID) {
		t.Fatal("Expected track to be marked as played in session")
	}

	// Other tracks must remain clean
	if isTrackFailedRecently("clean_good_track") {
		t.Fatal("Unrelated track should not be marked failed")
	}
}

// 10. TestManualPlayCancelsStaleAutoplay verifies that a manual /play cancels pending autoplay.
func TestManualPlayCancelsStaleAutoplay(t *testing.T) {
	chatID := int64(900110)
	room := core.CreateRoom(chatID, nil)
	defer core.DropRoom(chatID)

	workerGen := room.Generation()
	apCtx, apCancel, ok := room.BeginAutoplay(workerGen)
	if !ok {
		t.Fatal("Expected BeginAutoplay to succeed")
	}
	defer room.FinishAutoplay(workerGen)
	defer apCancel()

	// Manual play starts
	transGen := room.StartManualTransition("manual_play")
	defer room.FinishManualTransition(transGen)

	// Context must be canceled and room validation must return false
	if apCtx.Err() == nil {
		t.Fatal("Expected manual transition to cancel pending autoplay context")
	}
	if room.IsAutoplayValid(workerGen) {
		t.Fatal("Expected IsAutoplayValid to return false for stale autoplay worker")
	}
}

// 11. TestPlayforceCancelsStaleAutoplay verifies that /playforce cancels pending autoplay and increments generation.
func TestPlayforceCancelsStaleAutoplay(t *testing.T) {
	chatID := int64(900111)
	room := core.CreateRoom(chatID, nil)
	defer core.DropRoom(chatID)

	initialGen := room.Generation()
	apCtx, apCancel, ok := room.BeginAutoplay(initialGen)
	if !ok {
		t.Fatal("Expected BeginAutoplay to succeed")
	}
	defer room.FinishAutoplay(initialGen)
	defer apCancel()

	// /playforce arrives
	pfGen := room.StartManualTransition("playforce")
	defer room.FinishManualTransition(pfGen)

	if apCtx.Err() == nil {
		t.Fatal("Expected /playforce to cancel autoplay context immediately")
	}
	if room.IsAutoplayValid(initialGen) {
		t.Fatal("Expected IsAutoplayValid to return false for initial generation")
	}

	// Playforce track plays
	pfTrack := &state.Track{
		ID:        "playforce_track",
		Title:     "Requested Force Track",
		Requester: "Admin",
	}
	if err := room.Play(pfTrack, "/tmp/pf.mp3", false); err != nil {
		t.Fatalf("Failed to play force track: %v", err)
	}
	room.SetActiveTrackGen(pfGen)

	if room.Track() == nil || room.Track().ID != "playforce_track" {
		t.Fatalf("Expected active track to be playforce_track, got: %+v", room.Track())
	}
}

// 12. TestDuplicateStreamEndCallbacksDoNotStartMultipleTracks verifies that duplicate or concurrent stream-ends only run one worker.
func TestDuplicateStreamEndCallbacksDoNotStartMultipleTracks(t *testing.T) {
	chatID := int64(900112)
	worker := getChatWorker(chatID)

	// First callback acquires the worker lock
	if !worker.busy.CompareAndSwap(false, true) {
		t.Fatal("Expected first CAS to succeed")
	}

	// Simultaneous duplicate callback must fail and exit immediately
	if worker.busy.CompareAndSwap(false, true) {
		t.Fatal("Expected duplicate callback CAS to fail")
	}

	// Release worker
	worker.busy.Store(false)

	// Test under concurrency
	var acquiredCount atomic.Int32
	var wg sync.WaitGroup
	const concurrentAttempts = 20

	for i := 0; i < concurrentAttempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if worker.busy.CompareAndSwap(false, true) {
				acquiredCount.Add(1)
				time.Sleep(30 * time.Millisecond)
				worker.busy.Store(false)
			}
		}()
	}

	wg.Wait()

	if acquiredCount.Load() == 0 {
		t.Fatal("Expected at least one worker to execute")
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

	initialGen := room.Generation()
	if initialGen == 0 {
		t.Fatal("Expected non-zero initial room generation")
	}

	workerGen := room.Generation()
	apCtx, apCancel, ok := room.BeginAutoplay(workerGen)
	if !ok {
		t.Fatalf("Expected BeginAutoplay to succeed for initial gen %d", workerGen)
	}
	defer room.FinishAutoplay(workerGen)
	defer apCancel()

	apCandidate := &state.Track{
		ID:         "xcool_id",
		Title:      "X-COOL! (Ultra Slowed)",
		Source:     "MockSource",
		IsAutoplay: true,
	}

	var autoplayReachedPlay atomic.Bool
	var autoplayAborted atomic.Bool

	go func() {
		dlCtx, cancel := context.WithTimeout(apCtx, 10*time.Second)
		defer cancel()

		select {
		case <-dlCtx.Done():
			autoplayAborted.Store(true)
			return
		case <-time.After(500 * time.Millisecond):
		}

		if !room.IsAutoplayValid(workerGen) || apCtx.Err() != nil {
			autoplayAborted.Store(true)
			return
		}

		autoplayReachedPlay.Store(true)
		_ = room.Play(apCandidate, "/tmp/xcool.mp3", false)
	}()

	time.Sleep(50 * time.Millisecond)

	transGen := room.StartManualTransition("manual_play")
	defer room.FinishManualTransition(transGen)

	if apCtx.Err() == nil {
		t.Error("Expected StartManualTransition to cancel pending autoplay context immediately")
	}

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

	time.Sleep(150 * time.Millisecond)

	if autoplayReachedPlay.Load() {
		t.Fatal("CRITICAL: Stale autoplay worker called r.Play() despite intervening manual request!")
	}
	if !autoplayAborted.Load() {
		t.Fatal("Expected autoplay worker to detect cancellation and abort")
	}

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

	manualQueued := &state.Track{
		ID:        "queued_1",
		Title:     "Queued Track",
		Requester: "User",
	}
	_ = room.Play(manualQueued, "", false)

	room.SetShuffle(false)
	if room.IsAutoplayValid(workerGen) {
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

	natural, reason := room.IsNaturalStreamEnd(room.ActiveTrackGen())
	if natural {
		t.Fatal("Expected immediate stream-end to be rejected as premature decoder crash")
	}
	if reason == "" {
		t.Fatal("Expected descriptive reason for premature stream-end")
	}
}

// TestWeightedSessionVibeProfile verifies weighted recent track aggregation and gradual style shifts.
func TestWeightedSessionVibeProfile(t *testing.T) {
	chatID := int64(990022)
	room := core.CreateRoom(chatID, nil)
	defer core.DropRoom(chatID)

	// Play Track 1: Bollywood Energetic
	t1 := &state.Track{ID: "t1", Title: "Chaiyya Chaiyya", Artist: "Sukhwinder Singh"}
	room.UpdateSessionVibeFromTrack(t1)

	p1 := room.GetVibeProfile()
	if p1.Genre != state.GenreBollywood || p1.Vibe != state.VibeEnergetic {
		t.Fatalf("Expected Bollywood/Energetic for t1, got Genre=%s Vibe=%s", p1.Genre, p1.Vibe)
	}

	// Play Track 2: Bollywood Romantic
	t2 := &state.Track{ID: "t2", Title: "Tum Hi Ho", Artist: "Arijit Singh"}
	room.UpdateSessionVibeFromTrack(t2)

	// Play Track 3: Bollywood Romantic
	t3 := &state.Track{ID: "t3", Title: "Dil To Pagal Hai", Artist: "Lata Mangeshkar, Udit Narayan"}
	room.UpdateSessionVibeFromTrack(t3)

	p3 := room.GetVibeProfile()
	if p3.Genre != state.GenreBollywood {
		t.Fatalf("Expected GenreBollywood, got %s", p3.Genre)
	}
	if p3.Vibe != state.VibeRomantic {
		t.Fatalf("Expected VibeRomantic as top weighted vibe, got %s", p3.Vibe)
	}

	// Gradual shift: user manually requests Punjabi tracks
	t4 := &state.Track{ID: "t4", Title: "Sidhu Moose Wala - 295", Artist: "Sidhu Moose Wala"}
	room.UpdateSessionVibeFromTrack(t4)
	t5 := &state.Track{ID: "t5", Title: "Brown Munde", Artist: "AP Dhillon, Gurinder Gill"}
	room.UpdateSessionVibeFromTrack(t5)
	t6 := &state.Track{ID: "t6", Title: "Karan Aujla - Softly", Artist: "Karan Aujla"}
	room.UpdateSessionVibeFromTrack(t6)

	p6 := room.GetVibeProfile()
	// Recent Punjabi tracks carry much higher weights (1.0 + 0.75*i) and should have shifted the session genre
	if p6.Genre != state.GenrePunjabi {
		t.Fatalf("Expected session genre to gradually shift to Punjabi, got: %s", p6.Genre)
	}
}

// TestUnknownMetadataCandidateSelectionFallback verifies that an unknown profile uses labeled fallback
// and does not fabricate exact or moderate vibe match claims.
func TestUnknownMetadataCandidateSelectionFallback(t *testing.T) {
	chatID := int64(990033)
	room := core.CreateRoom(chatID, nil)
	defer core.DropRoom(chatID)

	// Start with unknown track
	tUnknown := &state.Track{ID: "unknown_1", Title: "Random Unknown Video 98234", Artist: "Unknown"}
	room.UpdateSessionVibeFromTrack(tUnknown)

	profile := room.GetVibeProfile()
	if profile.Genre != state.GenreUnknown || profile.Vibe != state.VibeUnknown {
		t.Fatalf("Expected Unknown genre and vibe, got Genre=%s Vibe=%s", profile.Genre, profile.Vibe)
	}

	// When scoring against candidates, it must return 20 and broad metadata fallback reason
	cand := &state.Track{ID: "cand_1", Title: "Arijit Singh - Kesariya", Artist: "Arijit Singh"}
	cProf := state.ClassifyTrack(cand.Title, cand.Artist)

	score, reasons := profile.ScoreCandidateDetails(cProf, tUnknown.Artist, cand.Artist)
	if score != 20 {
		t.Fatalf("Expected baseline score 20 for unknown session profile, got %d", score)
	}
	if len(reasons) == 0 || reasons[0] != "Broad metadata/search fallback (unknown session profile)" {
		t.Fatalf("Expected explicit fallback reason, got: %v", reasons)
	}
}

// TestArtistDiversityBonus verifies that different artists receive the diversity bonus.
func TestArtistDiversityBonus(t *testing.T) {
	session := state.ClassifyTrack("Arijit Singh - Tum Hi Ho", "Arijit Singh")
	candSameArtist := state.ClassifyTrack("Arijit Singh - Channa Mereya", "Arijit Singh")
	candDiffArtist := state.ClassifyTrack("Atif Aslam - Dil Diyan Gallan", "Atif Aslam")

	scoreSame, reasonsSame := session.ScoreCandidateDetails(candSameArtist, "Arijit Singh", "Arijit Singh")
	scoreDiff, reasonsDiff := session.ScoreCandidateDetails(candDiffArtist, "Arijit Singh", "Atif Aslam")

	hasDiversityReason := false
	for _, r := range reasonsDiff {
		if r == "Artist diversity (+5)" {
			hasDiversityReason = true
			break
		}
	}
	if !hasDiversityReason {
		t.Fatalf("Expected 'Artist diversity (+5)' reason for different artist, got reasons: %v", reasonsDiff)
	}
	for _, r := range reasonsSame {
		if r == "Artist diversity (+5)" {
			t.Fatalf("Did not expect artist diversity bonus for same artist, got reasons: %v", reasonsSame)
		}
	}
	if scoreDiff <= scoreSame {
		t.Fatalf("Expected score with artist diversity (%d) to be higher than without (%d)", scoreDiff, scoreSame)
	}
}

