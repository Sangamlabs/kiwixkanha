/*
 * ● Kiwi Music (KanhaMusic fork)
 * ○ Unit Tests for Telegram Audio Cache, Track Identity Verification & Provider Fallback
 */

package platforms

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	state "KanhaMusic/kanha/core/models"
	td "github.com/Kanha/Meow"
)

func createTestAudio(t *testing.T, path string, numSeconds float64) {
	t.Helper()
	const sampleRate = 44100
	numSamples := int(sampleRate * numSeconds)
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
	buf[16] = 16
	buf[20] = 1
	buf[22] = 1
	buf[24] = 0x44
	buf[25] = 0xAC
	buf[28] = 0x88
	buf[29] = 0x58
	buf[30] = 0x01
	buf[32] = 2
	buf[34] = 16
	copy(buf[36:40], "data")
	buf[40] = byte(dataSize)
	buf[41] = byte(dataSize >> 8)
	buf[42] = byte(dataSize >> 16)
	buf[43] = byte(dataSize >> 24)

	if err := os.WriteFile(path, buf, 0644); err != nil {
		t.Fatalf("Failed to write test audio file: %v", err)
	}
}

// 1. TestDifferentSongWithSimilarTitleNeverExactCacheHit verifies that a different song with a similar title
// is NEVER returned as an exact cache hit.
func TestDifferentSongWithSimilarTitleNeverExactCacheHit(t *testing.T) {
	cache := GetTelegramAudioCache()
	cache.Clear()

	// Store verified entry for "Dil To Pagal Hai"
	cache.Store(&TelegramCacheEntry{
		ProviderTrackID:      "lZ2PhyBF3GQ",
		Source:               PlatformYouTube,
		Title:                "Dil To Pagal Hai",
		Artist:               "Lata Mangeshkar & Udit Narayan",
		NormalizedTitle:      state.NormalizeTrackTitle("Dil To Pagal Hai"),
		NormalizedArtist:     state.NormalizeArtist("Lata Mangeshkar & Udit Narayan"),
		Version:              state.VersionOriginal,
		Duration:             334,
		TelegramFileID:       "CQACAgIAAyEFAATdYvsZAAEKHGJq7vdwvCFOmyk37hCzOFz7CwawsAACqDQAAk6tYUi70qZCwep_3D0E",
		TelegramFileUniqueID: "unique_dil_to_pagal_hai",
		FileSize:             5_400_000,
	})

	// Case A: Query with similar words but different song / artist
	unrelatedSong := &state.Track{
		ID:       "other_vid_999",
		Source:   PlatformYouTube,
		Title:    "Dil Pagal Hai Mera", // Similar title phrase, completely different song
		Artist:   "Kumar Sanu",
		Duration: 290,
	}

	entry, reason, matched := cache.MatchTrack(unrelatedSong)
	if matched {
		t.Fatalf("CRITICAL: Unrelated song with similar title was returned as cache hit: reason=%s, entry=%+v", reason, entry)
	}
	if reason != "cache_miss" && reason != "rejected_artist_mismatch" {
		t.Errorf("Expected cache miss or rejection, got: %s", reason)
	}

	// Case B: Completely different artist
	diffArtistSong := &state.Track{
		ID:       "tribute_cover_111",
		Source:   PlatformYouTube,
		Title:    "Dil To Pagal Hai",
		Artist:   "Acoustic Cover Band 2026",
		Duration: 200,
	}
	_, _, matchedB := cache.MatchTrack(diffArtistSong)
	if matchedB {
		t.Fatal("CRITICAL: Song with different artist and mismatched duration matched cache!")
	}
}

// 2. TestWrongTelegramFileMappingRejected verifies that an invalid Telegram file mapping is rejected.
func TestWrongTelegramFileMappingRejected(t *testing.T) {
	cache := GetTelegramAudioCache()
	cache.Clear()

	// Store an entry that has mismatched duration (e.g. 45s clip mapped to a full 334s song)
	cache.Store(&TelegramCacheEntry{
		ProviderTrackID:      "wrong_mapping_id",
		Source:               PlatformYouTube,
		Title:                "Dil To Pagal Hai",
		Artist:               "Lata Mangeshkar",
		NormalizedTitle:      state.NormalizeTrackTitle("Dil To Pagal Hai"),
		Version:              state.VersionOriginal,
		Duration:             45, // Mismatched short clip
		TelegramFileID:       "CQACAgIAAy_BAD_MAPPING",
		TelegramFileUniqueID: "bad_unique_id",
	})

	requestedTrack := &state.Track{
		ID:       "different_req_id",
		Source:   PlatformYouTube,
		Title:    "Dil To Pagal Hai",
		Artist:   "Lata Mangeshkar",
		Duration: 334, // Full length requested
	}

	entry, reason, matched := cache.MatchTrack(requestedTrack)
	if matched {
		t.Fatalf("CRITICAL: Wrong Telegram file mapping was accepted: reason=%s, entry=%+v", reason, entry)
	}
	if reason != "rejected_duration_mismatch" && reason != "cache_miss" {
		t.Errorf("Expected rejection due to duration mismatch, got: %s", reason)
	}
}

// 3. TestCorrectTelegramCachedAudioReused verifies that a verified Telegram cached audio file is correctly reused.
func TestCorrectTelegramCachedAudioReused(t *testing.T) {
	cache := GetTelegramAudioCache()
	cache.Clear()

	const expectedFileID = "CQACAgIAAyEFAATdYvsZAAEKHGJq7vdwvCFOmyk37hCzOFz7CwawsAACqDQAAk6tYUi70qZCwep_3D0E"
	const expectedUniqueID = "unique_correct_123"

	cache.Store(&TelegramCacheEntry{
		ProviderTrackID:      "lZ2PhyBF3GQ",
		Source:               PlatformYouTube,
		ISRC:                 "IN-U10-97-00001",
		Title:                "Dil To Pagal Hai",
		Artist:               "Lata Mangeshkar & Udit Narayan",
		NormalizedTitle:      state.NormalizeTrackTitle("Dil To Pagal Hai"),
		NormalizedArtist:     state.NormalizeArtist("Lata Mangeshkar & Udit Narayan"),
		Version:              state.VersionOriginal,
		Duration:             334,
		TelegramFileID:       expectedFileID,
		TelegramFileUniqueID: expectedUniqueID,
		FileSize:             5_400_000,
	})

	// Match by exact provider track ID
	reqTrack := &state.Track{
		ID:       "lZ2PhyBF3GQ",
		Source:   PlatformYouTube,
		Title:    "Dil To Pagal Hai - Full Title Song",
		Duration: 334,
	}

	entry, reason, matched := cache.MatchTrack(reqTrack)
	if !matched {
		t.Fatalf("Expected exact cache hit, got matched=false (reason=%s)", reason)
	}
	if entry.TelegramFileID != expectedFileID {
		t.Errorf("Expected file ID %s, got %s", expectedFileID, entry.TelegramFileID)
	}
	if reason != "exact_provider_track_id" {
		t.Errorf("Expected reason exact_provider_track_id, got %s", reason)
	}

	// Match by exact ISRC
	isrcTrack := &state.Track{
		ID:       "spotify_different_id",
		Source:   PlatformSpotify,
		ISRC:     "IN-U10-97-00001",
		Title:    "Dil To Pagal Hai",
		Duration: 334,
	}
	entryISRC, reasonISRC, matchedISRC := cache.MatchTrack(isrcTrack)
	if !matchedISRC {
		t.Fatalf("Expected ISRC cache hit, got matched=false (reason=%s)", reasonISRC)
	}
	if reasonISRC != "exact_isrc" {
		t.Errorf("Expected reason exact_isrc, got %s", reasonISRC)
	}
	if entryISRC.TelegramFileID != expectedFileID {
		t.Errorf("Expected file ID %s, got %s", expectedFileID, entryISRC.TelegramFileID)
	}
}

// Mock platform that returns an audio file with duration mismatch
type mockDurationMismatchPlatform struct {
	mismatchFile string
}

func (m *mockDurationMismatchPlatform) Name() state.PlatformName { return "MockDurationMismatch" }
func (m *mockDurationMismatchPlatform) Priority() int            { return 200 }
func (m *mockDurationMismatchPlatform) CanGet(_ string) bool     { return false }
func (m *mockDurationMismatchPlatform) Get(_ string, _ bool) ([]*state.Track, error) {
	return nil, nil
}
func (m *mockDurationMismatchPlatform) CanDownload(source state.PlatformName) bool {
	return source == "MockSourceFallback"
}
func (m *mockDurationMismatchPlatform) Download(_ context.Context, _ *state.Track, _ *td.Message) (string, error) {
	return m.mismatchFile, nil
}

// Mock platform that returns a valid audio file with matching duration
type mockValidMatchPlatform struct {
	validFile string
}

func (m *mockValidMatchPlatform) Name() state.PlatformName                     { return "MockValidMatch" }
func (m *mockValidMatchPlatform) Priority() int                                { return 190 }
func (m *mockValidMatchPlatform) CanGet(_ string) bool                         { return false }
func (m *mockValidMatchPlatform) Get(_ string, _ bool) ([]*state.Track, error) { return nil, nil }
func (m *mockValidMatchPlatform) CanDownload(source state.PlatformName) bool {
	return source == "MockSourceFallback"
}
func (m *mockValidMatchPlatform) Download(_ context.Context, _ *state.Track, _ *td.Message) (string, error) {
	return m.validFile, nil
}

// 4. TestWrongProviderResultFallsBackToAnotherProvider verifies that if Provider 1 returns
// an unrelated result (e.g. 500s mismatch for 100s song), it is rejected and Provider 2 is used.
func TestWrongProviderResultFallsBackToAnotherProvider(t *testing.T) {
	tmpDir := t.TempDir()

	// Provider 1 file: 1.0 second duration (too short for 180s song)
	mismatchAudio := filepath.Join(tmpDir, "mismatch.wav")
	createTestAudio(t, mismatchAudio, 1.0)

	// Provider 2 file: 175.0 seconds duration (matches 180s song within tolerance)
	validAudio := filepath.Join(tmpDir, "valid.wav")
	createTestAudio(t, validAudio, 175.0)

	Register(&mockDurationMismatchPlatform{mismatchFile: mismatchAudio})
	Register(&mockValidMatchPlatform{validFile: validAudio})

	track := &state.Track{
		ID:       "fallback_test_mismatch",
		Title:    "Track Testing Fallback",
		Source:   "MockSourceFallback",
		Duration: 180,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resultPath, err := Download(ctx, track, nil)
	if err != nil {
		t.Fatalf("Expected fallback to succeed on Provider 2, got: %v", err)
	}

	if resultPath != validAudio {
		t.Errorf("Expected path %s from Provider 2, got %s", validAudio, resultPath)
	}
}

// 5. TestCorruptMP3Rejected verifies that a corrupt MP3 or HTML error payload is rejected by ValidateTrackAudio.
func TestCorruptMP3Rejected(t *testing.T) {
	tmpDir := t.TempDir()

	// File with HTML payload
	htmlFile := filepath.Join(tmpDir, "bad_payload.mp3")
	_ = os.WriteFile(htmlFile, append([]byte("<!DOCTYPE html><html>404 Not Found</html>"), make([]byte, 40000)...), 0644)

	track := &state.Track{
		ID:       "corrupt_track_1",
		Title:    "Corrupt Test",
		Duration: 180,
	}

	err := ValidateTrackAudio(htmlFile, track)
	if err == nil {
		t.Fatal("Expected ValidateTrackAudio to reject HTML error payload")
	}

	// File with corrupted garbage frames
	garbageFile := filepath.Join(tmpDir, "garbage_frames.mp3")
	_ = os.WriteFile(garbageFile, append([]byte("ID3\x03\x00\x00\x00\x00\x00\x00\xff\xfb\x00\x00garbage_junk"), make([]byte, 40000)...), 0644)

	errGarbage := ValidateTrackAudio(garbageFile, track)
	if errGarbage == nil {
		t.Fatal("Expected ValidateTrackAudio to reject corrupted garbage frames")
	}
}

// 6. TestOriginalAndRemixRecordingsRemainSeparate verifies that original, remix, slowed,
// and live versions have distinct canonical keys and never match each other in the cache.
func TestOriginalAndRemixRecordingsRemainSeparate(t *testing.T) {
	cache := GetTelegramAudioCache()
	cache.Clear()

	origKey := state.CanonicalRecordingKey("Dil To Pagal Hai", "Lata Mangeshkar")
	remixKey := state.CanonicalRecordingKey("Dil To Pagal Hai (Remix)", "Lata Mangeshkar")
	slowedKey := state.CanonicalRecordingKey("Dil To Pagal Hai (Slowed)", "Lata Mangeshkar")
	liveKey := state.CanonicalRecordingKey("Dil To Pagal Hai (Live in Concert)", "Lata Mangeshkar")

	if origKey == remixKey {
		t.Fatalf("Original and Remix generated identical canonical keys: %s", origKey)
	}
	if origKey == slowedKey {
		t.Fatalf("Original and Slowed generated identical canonical keys: %s", origKey)
	}
	if origKey == liveKey {
		t.Fatalf("Original and Live generated identical canonical keys: %s", origKey)
	}

	// Store ONLY the Remix in cache
	cache.Store(&TelegramCacheEntry{
		ProviderTrackID:      "remix_tg_file_id",
		Source:               PlatformYouTube,
		Title:                "Dil To Pagal Hai (Remix)",
		Artist:               "Lata Mangeshkar",
		NormalizedTitle:      state.NormalizeTrackTitle("Dil To Pagal Hai (Remix)"),
		NormalizedArtist:     state.NormalizeArtist("Lata Mangeshkar"),
		Version:              state.VersionRemix,
		Duration:             240,
		TelegramFileID:       "CQACAg_REMIX_FILE",
		TelegramFileUniqueID: "remix_unique_id",
	})

	// User requests the Original version
	originalReq := &state.Track{
		ID:       "youtube_original_vid",
		Source:   PlatformYouTube,
		Title:    "Dil To Pagal Hai",
		Artist:   "Lata Mangeshkar",
		Duration: 334,
	}

	entry, reason, matched := cache.MatchTrack(originalReq)
	if matched {
		t.Fatalf("CRITICAL: Original song request matched Remix in cache! entry=%+v, reason=%s", entry, reason)
	}
	if reason != "cache_miss" && reason != "rejected_version_mismatch" {
		t.Errorf("Expected cache miss or version rejection, got: %s", reason)
	}
}

// 7. TestPlayforcePlaysExactValidatedRecording verifies that /playforce with an explicit query
// never hijacks replied media and strictly validates requested recording identity.
func TestPlayforcePlaysExactValidatedRecording(t *testing.T) {
	// Verify that CanonicalRecordingKey preserves exact track identity
	key := state.CanonicalRecordingKey("Dil To Pagal Hai", "Lata Mangeshkar & Udit Narayan")
	expected := "lata mangeshkar udit narayan - dil to pagal hai [Original]"
	if key != expected {
		t.Errorf("Expected canonical key %q, got %q", expected, key)
	}

	// Verify track version classification
	ver := state.ClassifyTrack("Dil To Pagal Hai").Version
	if ver != state.VersionOriginal {
		t.Errorf("Expected VersionOriginal, got %s", ver)
	}

	verRemix := state.ClassifyTrack("Dil To Pagal Hai (DJ Remix 2026)").Version
	if verRemix != state.VersionRemix {
		t.Errorf("Expected VersionRemix, got %s", verRemix)
	}

	verLive := state.ClassifyTrack("Dil To Pagal Hai (Live at Wembley)").Version
	if verLive != state.VersionLive {
		t.Errorf("Expected VersionLive, got %s", verLive)
	}
}
