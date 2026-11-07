/*
 * ● KanhaMusic
 * ○ Unit Tests for Vibe Classification & Scoring Engine
 */

package models

import (
	"testing"
)

func TestClassifyTrackVersions(t *testing.T) {
	cases := []struct {
		title           string
		expectedVersion VersionType
	}{
		{"DVRST - Close Eyes (Slowed + Reverb)", VersionSlowedReverb},
		{"Kordhell - Murder In My Mind (Slowed & Reverb)", VersionSlowedReverb},
		{"Song Title (slowed)", VersionSlowed},
		{"Song Title (Ultra Slowed)", VersionSlowed},
		{"Song Title (Sped Up)", VersionSpedUp},
		{"Song Title (Speed Up)", VersionSpedUp},
		{"Rockefeller Street (Nightcore)", VersionNightcore},
		{"Adele - Hello (Instrumental)", VersionInstrumental},
		{"Nirvana - About a Girl (Acoustic)", VersionAcoustic},
		{"Coldplay - Yellow (Remix)", VersionRemix},
		{"Coldplay - Yellow", VersionOriginal},
	}

	for _, tc := range cases {
		profile := ClassifyTrack(tc.title)
		if profile.Version != tc.expectedVersion {
			t.Errorf("For %q expected version %s, got %s", tc.title, tc.expectedVersion, profile.Version)
		}
	}
}

func TestClassifyTrackGenres(t *testing.T) {
	cases := []struct {
		title         string
		expectedGenre GenreType
	}{
		{"Kordhell - Live Another Day", GenrePhonk},
		{"DVRST - Close Eyes", GenrePhonk},
		{"Metamorphosis Phonk", GenrePhonk},
		{"M4GN - EL CONTROL", GenrePhonk},
		{"Eminem - Without Me", GenreHipHop},
		{"Drake - God's Plan", GenreHipHop},
		{"Martin Garrix - Animals", GenreEDM},
		{"Alan Walker - Faded", GenreEDM},
		{"Arctic Monkeys - Do I Wanna Know", GenreIndie},
		{"The Neighbourhood - Sweater Weather", GenreIndie},
		{"Taylor Swift - Blank Space", GenrePop},
		{"Billie Eilish - Bad Guy", GenrePop},
	}

	for _, tc := range cases {
		profile := ClassifyTrack(tc.title)
		if profile.Genre != tc.expectedGenre {
			t.Errorf("For %q expected genre %s, got %s", tc.title, tc.expectedGenre, profile.Genre)
		}
	}
}

func TestClassifyTrackVibes(t *testing.T) {
	cases := []struct {
		title        string
		expectedVibe VibeType
	}{
		{"Lofi Hip Hop Beat to Relax/Study to", VibeLofi},
		{"Sad Broken Heart Tears Crying Alone", VibeSad},
		{"Romantic Love Story Forever With You", VibeRomantic},
		{"Dark Demons Shadow Grim Doom", VibeDark},
		{"Soft Gentle Whisper Delicate Lullaby", VibeSoft},
		{"Chill Relaxing Peaceful Meditation", VibeChill},
	}

	for _, tc := range cases {
		profile := ClassifyTrack(tc.title)
		if profile.Vibe != tc.expectedVibe {
			t.Errorf("For %q expected vibe %s, got %s", tc.title, tc.expectedVibe, profile.Vibe)
		}
	}
}

func TestScoreCandidateMatchesAndPenalties(t *testing.T) {
	// Base session: Slowed Phonk
	session := ClassifyTrack("Kordhell - Murder In My Mind (Slowed)")

	// 1. Exact match candidate
	candExact := ClassifyTrack("DVRST - Close Eyes (Slowed)")
	scoreExact := session.ScoreCandidate(candExact)
	if scoreExact < 60 {
		t.Errorf("Expected score >= 60 for exact match, got %d", scoreExact)
	}

	// 2. Related version (Slowed + Reverb)
	candSlowedReverb := ClassifyTrack("Interworld - Metamorphosis (Slowed + Reverb)")
	scoreSlowedReverb := session.ScoreCandidate(candSlowedReverb)
	if scoreSlowedReverb < 50 {
		t.Errorf("Expected score >= 50 for related version, got %d", scoreSlowedReverb)
	}

	// 3. Opposite version (Sped Up)
	candSpedUp := ClassifyTrack("Phonk Drift (Sped Up)")
	scoreSpedUp := session.ScoreCandidate(candSpedUp)
	if scoreSpedUp >= scoreExact {
		t.Errorf("Expected Sped Up (%d) to score lower than Slowed (%d)", scoreSpedUp, scoreExact)
	}

	// 4. Conflicting genre (Pop)
	candPop := ClassifyTrack("Taylor Swift - Shake It Off")
	scorePop := session.ScoreCandidate(candPop)
	if scorePop >= 0 {
		t.Errorf("Expected negative score for conflicting Pop genre, got %d", scorePop)
	}

	// 5. Conflicting genre (EDM)
	candEDM := ClassifyTrack("David Guetta - Titanium (EDM Remix)")
	scoreEDM := session.ScoreCandidate(candEDM)
	if scoreEDM >= 0 {
		t.Errorf("Expected negative score for conflicting EDM genre, got %d", scoreEDM)
	}
}

