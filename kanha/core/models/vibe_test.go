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
		{"Arijit Singh - Live in Concert", VersionLive},
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
		artist        string
		expectedGenre GenreType
	}{
		{"Kordhell - Live Another Day", "", GenrePhonk},
		{"DVRST - Close Eyes", "", GenrePhonk},
		{"Metamorphosis Phonk", "", GenrePhonk},
		{"M4GN - EL CONTROL", "", GenrePhonk},
		{"Eminem - Without Me", "", GenreHipHop},
		{"Drake - God's Plan", "", GenreHipHop},
		{"Martin Garrix - Animals", "", GenreEDM},
		{"Alan Walker - Faded", "", GenreEDM},
		{"Arctic Monkeys - Do I Wanna Know", "", GenreIndie},
		{"The Neighbourhood - Sweater Weather", "", GenreIndie},
		{"Taylor Swift - Blank Space", "", GenrePop},
		{"Billie Eilish - Bad Guy", "", GenrePop},
		// Indian & Regional Categories (previously returned empty "")
		{"Dil To Pagal Hai - Full Song", "Lata Mangeshkar, Udit Narayan", GenreBollywood},
		{"Channa Mereya", "Pritam, Arijit Singh", GenreBollywood},
		{"Tum Hi Ho", "Arijit Singh", GenreBollywood},
		{"Sidhu Moose Wala - 295", "", GenrePunjabi},
		{"Brown Munde", "AP Dhillon, Gurinder Gill", GenrePunjabi},
		{"Karan Aujla - Softly", "", GenrePunjabi},
		{"Hanuman Chalisa", "Hariharan", GenreBollywood},
		{"Nirvana - Smells Like Teen Spirit", "", GenreRock},
		{"Lofi Girl - Study Beats", "", GenreLofi},
	}

	for _, tc := range cases {
		profile := ClassifyTrack(tc.title, tc.artist)
		if profile.Genre != tc.expectedGenre {
			t.Errorf("For %q (artist %q) expected genre %s, got %s", tc.title, tc.artist, tc.expectedGenre, profile.Genre)
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
		{"Party All Night Club Dhol Dhamaka", VibeParty},
		{"Workout Gym Beast Mode High Energy", VibeEnergetic},
		{"Hanuman Chalisa Bhakti", VibeDevotional},
		{"Motivational Rise Legend Warrior Champion", VibeMotivational},
	}

	for _, tc := range cases {
		profile := ClassifyTrack(tc.title)
		if profile.Vibe != tc.expectedVibe {
			t.Errorf("For %q expected vibe %s, got %s", tc.title, tc.expectedVibe, profile.Vibe)
		}
	}
}

func TestClassifyRealWorldIndianTitles(t *testing.T) {
	// Real-world YouTube titles that previously produced Genre=, Vibe=
	t1 := ClassifyTrack("Dil To Pagal Hai - Full Title Song | Shah Rukh Khan | Madhuri Dixit")
	if t1.Genre != GenreBollywood || t1.Vibe != VibeRomantic {
		t.Errorf("Expected Bollywood/Romantic for Dil To Pagal Hai, got Genre=%s, Vibe=%s", t1.Genre, t1.Vibe)
	}

	t2 := ClassifyTrack("Chaiyya Chaiyya (From \"Dil Se..\")")
	if t2.Genre != GenreBollywood || t2.Vibe != VibeEnergetic {
		t.Errorf("Expected Bollywood/Energetic for Chaiyya Chaiyya, got Genre=%s, Vibe=%s", t2.Genre, t2.Vibe)
	}

	t3 := ClassifyTrack("Kala Chashma | Baar Baar Dekho | Sidharth M Katrina K | Prem & Hardeep")
	if t3.Vibe != VibeParty {
		t.Errorf("Expected Party vibe for Kala Chashma, got %s", t3.Vibe)
	}

	t4 := ClassifyTrack("Sidhu Moose Wala - 295 (Official Audio)")
	if t4.Genre != GenrePunjabi {
		t.Errorf("Expected Punjabi for 295, got %s", t4.Genre)
	}
}

func TestUnknownMetadataHandling(t *testing.T) {
	// Completely unknown random string with no musical signals
	profile := ClassifyTrack("X893 QZW 772109")
	if profile.Genre != GenreUnknown {
		t.Errorf("Expected GenreUnknown, got %s", profile.Genre)
	}
	if profile.Vibe != VibeUnknown {
		t.Errorf("Expected VibeUnknown, got %s", profile.Vibe)
	}
	if profile.Confidence > 0.40 {
		t.Errorf("Expected low confidence (<=0.40) for unknown track, got %.2f", profile.Confidence)
	}

	// Unknown session profile candidate scoring
	cand := ClassifyTrack("Arijit Singh - Tum Hi Ho")
	score, reasons := profile.ScoreCandidateDetails(cand, "", "")
	if score != 20 {
		t.Errorf("Expected baseline score 20 for unknown session profile, got %d", score)
	}
	if len(reasons) == 0 || reasons[0] != "Broad metadata/search fallback (unknown session profile)" {
		t.Errorf("Expected explicit broad metadata fallback reason, got %v", reasons)
	}
}

func TestScoreCandidateMatchesAndPenalties(t *testing.T) {
	// Base session: Slowed Phonk
	session := ClassifyTrack("Kordhell - Murder In My Mind (Slowed)")

	// 1. Exact match candidate
	candExact := ClassifyTrack("DVRST - Close Eyes (Slowed)")
	scoreExact, reasonsExact := session.ScoreCandidateDetails(candExact, "", "")
	t.Logf("session=%+v, candExact=%+v, scoreExact=%d, reasons=%v", session, candExact, scoreExact, reasonsExact)
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
