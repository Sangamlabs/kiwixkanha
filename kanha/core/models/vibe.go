/*
 * ● KanhaMusic
 * ○ Vibe & Style Classification Engine for Kiwi Music Autoplay
 */

package models

import (
	"regexp"
	"strings"
)

type VibeType string
type GenreType string
type VersionType string

const (
	// Vibes / Styles
	VibeNone     VibeType = ""
	VibeLofi     VibeType = "Lofi"
	VibeSoft     VibeType = "Soft"
	VibeSad      VibeType = "Sad"
	VibeRomantic VibeType = "Romantic"
	VibeChill    VibeType = "Chill"
	VibeDark     VibeType = "Dark"

	// Genres
	GenreNone   GenreType = ""
	GenrePhonk  GenreType = "Phonk"
	GenreHipHop GenreType = "Hip-Hop"
	GenrePop    GenreType = "Pop"
	GenreEDM    GenreType = "EDM"
	GenreIndie  GenreType = "Indie"

	// Versions
	VersionOriginal     VersionType = "Original"
	VersionRemix        VersionType = "Remix"
	VersionSlowed       VersionType = "Slowed"
	VersionSlowedReverb VersionType = "Slowed + Reverb"
	VersionSpedUp       VersionType = "Sped Up"
	VersionNightcore    VersionType = "Nightcore"
	VersionInstrumental VersionType = "Instrumental"
	VersionAcoustic     VersionType = "Acoustic"
)

// TrackVibeProfile models the musical mood, genre, and version of a song.
type TrackVibeProfile struct {
	Vibe      VibeType
	Genre     GenreType
	Version   VersionType
	Energy    string // "High", "Medium", "Low"
	Mood      string // "Melancholy", "Energetic", "Calm", "Loving", "Ominous", "Neutral"
	BaseQuery string // Clean seed keywords for search
}

var wordSplitRegex = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// ClassifyTrack analyzes a track title and metadata to produce a structured Vibe profile.
func ClassifyTrack(title string) TrackVibeProfile {
	lower := strings.ToLower(title)
	profile := TrackVibeProfile{
		Version: VersionOriginal,
		Energy:  "Medium",
		Mood:    "Neutral",
	}

	// 1. Version Detection
	if containsAny(lower, "slowed + reverb", "slowed & reverb", "slowed and reverb", "slowed reverb", "slowed+reverb") {
		profile.Version = VersionSlowedReverb
	} else if containsWord(lower, "slowed", "slowed down", "ultra slowed") {
		profile.Version = VersionSlowed
	} else if containsAny(lower, "sped up", "speed up", "spedup", "speedup") {
		profile.Version = VersionSpedUp
	} else if containsWord(lower, "nightcore") {
		profile.Version = VersionNightcore
	} else if containsWord(lower, "instrumental", "karaoke", "backing track") {
		profile.Version = VersionInstrumental
	} else if containsWord(lower, "acoustic", "unplugged") {
		profile.Version = VersionAcoustic
	} else if containsWord(lower, "remix", "club mix", "extended mix", "vip mix", "bootleg", "flip") {
		profile.Version = VersionRemix
	}

	// 2. Genre Detection
	if containsAny(lower,
		"phonk", "drift phonk", "brazilian phonk", "kordhell", "dvrst", "interworld",
		"pharmacist", "kaito shoma", "mishashi sensei", "playaphonk", "metamorphosis",
		"montagem", "el control", "schizo", "shadow dance", "dxrk", "rapture",
		"tokyodrift", "murder in my mind", "close eyes", "neon blade", "vendetta", "sahara",
	) {
		profile.Genre = GenrePhonk
	} else if containsAny(lower,
		"lofi", "lo-fi", "lo fi", "chillhop", "study beat", "chilledcow", "lofi girl",
	) {
		// Lofi is often both a vibe and a genre subtype; default genre to Hip-Hop/Indie
		profile.Genre = GenreHipHop
		profile.Vibe = VibeLofi
	} else if containsAny(lower,
		"hip hop", "hiphop", "hip-hop", "rap", "trap", "drill", "freestyle", "cypher", "boombap",
		"eminem", "drake", "2pac", "kendrick", "j. cole", "travis scott", "juice wrld", "xxxtentacion", "lil peep", "post malone",
	) {
		profile.Genre = GenreHipHop
	} else if containsAny(lower,
		"edm", "house", "techno", "trance", "dubstep", "dnb", "drum & bass", "drum and bass", "hardstyle",
		"alan walker", "marshmello", "martin garrix", "tiesto", "avicii", "skrillex", "illenium", "david guetta", "zedd", "calvin harris", "kygo",
	) {
		profile.Genre = GenreEDM
	} else if containsAny(lower,
		"indie", "indie rock", "indie pop", "alternative", "bedroom pop", "mac demarco",
		"clairo", "rex orange county", "boy pablo", "phoebe bridgers", "arctic monkeys",
		"the neighbourhood", "sweater weather", "cigarettes after sex", "wallows", "beach house", "tame impala",
	) {
		profile.Genre = GenreIndie
	} else if containsAny(lower,
		"pop", "synthpop", "electropop", "dance pop", "k-pop", "kpop", "j-pop", "jpop",
		"taylor swift", "ariana", "dua lipa", "billie eilish", "the weeknd", "ed sheeran", "justin bieber", "olivia rodrigo",
	) {
		profile.Genre = GenrePop
	}

	// 3. Vibe / Style Detection (if not already set)
	if profile.Vibe == VibeNone {
		if containsAny(lower, "lofi", "lo-fi", "lo fi", "chillhop") {
			profile.Vibe = VibeLofi
		} else if containsAny(lower, "sad", "depression", "depressed", "crying", "heartbreak", "heartbroken", "broken heart", "pain", "alone", "lonely", "tears", "cry", "melancholy") {
			profile.Vibe = VibeSad
		} else if containsAny(lower, "romantic", "romance", "love", "crush", "in love", "sweetheart", "kiss", "valentine", "soulmate", "forever with you", "lovers") {
			profile.Vibe = VibeRomantic
		} else if containsAny(lower, "dark", "evil", "demon", "shadow", "goth", "gothic", "grim", "nightmare", "rage", "hell", "doom", "devil", "death", "haunted") {
			profile.Vibe = VibeDark
		} else if containsAny(lower, "soft", "gentle", "quiet", "whisper", "delicate", "tender", "lullaby") {
			profile.Vibe = VibeSoft
		} else if containsAny(lower, "chill", "relax", "relaxing", "calm", "mellow", "ambient", "peaceful", "peace", "serene", "meditation", "soothing") {
			profile.Vibe = VibeChill
		}
	}

	// Infer vibe from genre if still undetermined
	if profile.Vibe == VibeNone {
		if profile.Genre == GenrePhonk {
			if profile.Version == VersionSlowed || profile.Version == VersionSlowedReverb {
				profile.Vibe = VibeChill
			} else {
				profile.Vibe = VibeDark
			}
		}
	}

	// 4. Energy & Mood Determination
	if profile.Genre == GenrePhonk || profile.Genre == GenreEDM || profile.Version == VersionSpedUp || profile.Version == VersionNightcore {
		profile.Energy = "High"
	} else if profile.Vibe == VibeLofi || profile.Vibe == VibeSoft || profile.Vibe == VibeChill || profile.Version == VersionSlowed || profile.Version == VersionSlowedReverb || profile.Version == VersionAcoustic {
		profile.Energy = "Low"
	} else {
		profile.Energy = "Medium"
	}

	switch profile.Vibe {
	case VibeSad:
		profile.Mood = "Melancholy"
	case VibeRomantic:
		profile.Mood = "Loving"
	case VibeDark:
		profile.Mood = "Ominous"
	case VibeLofi, VibeChill, VibeSoft:
		profile.Mood = "Calm"
	default:
		if profile.Energy == "High" {
			profile.Mood = "Energetic"
		} else {
			profile.Mood = "Neutral"
		}
	}

	profile.BaseQuery = cleanSongTitle(title)
	return profile
}

// ScoreCandidate calculates a compatibility score (0 to 100) of a candidate track against the session profile.
// A negative score indicates conflicting genres or versions that should be strictly avoided.
func (p TrackVibeProfile) ScoreCandidate(c TrackVibeProfile) int {
	score := 20 // baseline candidate score

	// 1. Genre matching
	if p.Genre != GenreNone {
		if c.Genre == p.Genre {
			score += 45 // strongly reward matching genre
		} else if c.Genre != GenreNone {
			score -= 60 // strictly penalize completely different genres (e.g. Phonk vs Pop)
		}
	}

	// 2. Version matching
	if p.Version != VersionNone() && p.Version != VersionOriginal {
		if c.Version == p.Version {
			score += 35 // reward exact version match (e.g. Slowed Phonk -> Slowed Phonk)
		} else if (p.Version == VersionSlowed && c.Version == VersionSlowedReverb) || (p.Version == VersionSlowedReverb && c.Version == VersionSlowed) {
			score += 20 // related version match
		} else if c.Version == VersionSpedUp || c.Version == VersionNightcore {
			score -= 30 // opposite tempo/style penalty
		}
	} else if c.Version == p.Version {
		score += 10
	}

	// 3. Vibe matching
	if p.Vibe != VibeNone {
		if c.Vibe == p.Vibe {
			score += 25 // reward matching vibe (e.g. Lofi -> Lofi)
		} else if c.Vibe != VibeNone && c.Vibe != p.Vibe {
			score -= 20 // penalize incompatible vibe
		}
	}

	// 4. Energy matching
	if p.Energy != "" && c.Energy == p.Energy {
		score += 10
	}

	return score
}

func VersionNone() VersionType {
	return VersionType("")
}

func containsAny(s string, sub ...string) bool {
	for _, item := range sub {
		if strings.Contains(s, item) {
			return true
		}
	}
	return false
}

func containsWord(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func cleanSongTitle(title string) string {
	title = strings.Split(title, "|")[0]
	title = strings.Split(title, "(")[0]
	title = strings.Split(title, "[")[0]
	return strings.TrimSpace(title)
}

