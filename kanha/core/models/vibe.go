/*
 * ● KanhaMusic
 * ○ Vibe & Style Classification Engine for Kiwi Music Autoplay
 */

package models

import (
	"math"
	"regexp"
	"strings"
)

type VibeType string
type GenreType string
type VersionType string

const (
	// Vibes / Moods
	VibeNone         VibeType = ""
	VibeRomantic     VibeType = "Romantic"
	VibeSad          VibeType = "Sad"
	VibeEmotional    VibeType = "Emotional"
	VibeChill        VibeType = "Chill"
	VibeRelaxing     VibeType = "Relaxing"
	VibeEnergetic    VibeType = "Energetic"
	VibeParty        VibeType = "Party"
	VibeDark         VibeType = "Dark"
	VibeAggressive   VibeType = "Aggressive"
	VibeMotivational VibeType = "Motivational"
	VibeDreamy       VibeType = "Dreamy"
	VibePeaceful     VibeType = "Peaceful"
	VibeDevotional   VibeType = "Devotional"
	VibeLofi         VibeType = "Lofi"
	VibeSoft         VibeType = "Soft"
	VibeOther        VibeType = "Other"
	VibeUnknown      VibeType = "Unknown"

	// Genres
	GenreNone         GenreType = ""
	GenreBollywood    GenreType = "Bollywood"
	GenrePunjabi      GenreType = "Punjabi"
	GenrePop          GenreType = "Pop"
	GenreHipHop       GenreType = "Hip-Hop"
	GenreRap          GenreType = "Rap"
	GenrePhonk        GenreType = "Phonk"
	GenreEDM          GenreType = "EDM"
	GenreIndie        GenreType = "Indie"
	GenreRock         GenreType = "Rock"
	GenreClassical    GenreType = "Classical"
	GenreElectronic   GenreType = "Electronic"
	GenreLofi         GenreType = "Lo-fi"
	GenreInstrumental GenreType = "Instrumental"
	GenreOther        GenreType = "Other"
	GenreUnknown      GenreType = "Unknown"

	// Versions
	VersionOriginal     VersionType = "Original"
	VersionSlowed       VersionType = "Slowed"
	VersionSlowedReverb VersionType = "Slowed + Reverb"
	VersionSpedUp       VersionType = "Sped Up"
	VersionNightcore    VersionType = "Nightcore"
	VersionRemix        VersionType = "Remix"
	VersionMashup       VersionType = "Mashup"
	VersionInstrumental VersionType = "Instrumental"
	VersionAcoustic     VersionType = "Acoustic"
	VersionLofi         VersionType = "Lofi"
	VersionLive         VersionType = "Live"
)

// TrackVibeProfile models the musical mood, genre, and version of a song.
type TrackVibeProfile struct {
	Genre      GenreType   `json:"genre"`
	Vibe       VibeType    `json:"vibe"`
	Version    VersionType `json:"version"`
	Energy     string      `json:"energy"` // "Low", "Medium", "High", "Very High"
	Mood       string      `json:"mood"`   // "Melancholy", "Energetic", "Calm", "Loving", "Ominous", "Party", "Devotional", "Neutral"
	Confidence float64     `json:"confidence"`
	BaseQuery  string      `json:"base_query"` // Clean seed keywords for search
}

// VibeProfile is an alias for TrackVibeProfile for consistency with external prompts.
type VibeProfile = TrackVibeProfile

// MatchWeights defines the configurable scoring weights for candidate evaluation.
type MatchWeights struct {
	ExactVibe         int
	CompatibleVibe    int
	ExactGenre        int
	RelatedGenre      int
	ExactVersion      int
	CompatibleVersion int
	SimilarEnergy     int
	ArtistDiversity   int
}

// DefaultMatchWeights specifies the standard scoring profile.
var DefaultMatchWeights = MatchWeights{
	ExactVibe:         30,
	CompatibleVibe:    15,
	ExactGenre:        25,
	RelatedGenre:      12,
	ExactVersion:      20,
	CompatibleVersion: 10,
	SimilarEnergy:     15,
	ArtistDiversity:   5,
}

var cleanTitleParenRe = regexp.MustCompile(`(?i)\((official|video|audio|lyrics?|full song|hd|4k|visualizer|remastered|version|from "[^"]+")[^)]*\)`)
var cleanTitleBracketRe = regexp.MustCompile(`(?i)\[(official|video|audio|lyrics?|full song|hd|4k|visualizer|remastered)[^\]]*\]`)

// ClassifyTrack analyzes a track title and optional artist to produce a structured Vibe profile.
func ClassifyTrack(title string, optionalArtist ...string) TrackVibeProfile {
	artist := ""
	if len(optionalArtist) > 0 {
		artist = strings.TrimSpace(optionalArtist[0])
	}

	cleanTitle := cleanTitleParenRe.ReplaceAllString(title, "")
	cleanTitle = cleanTitleBracketRe.ReplaceAllString(cleanTitle, "")
	lowerTitle := strings.ToLower(cleanTitle)
	lowerArtist := strings.ToLower(artist)

	// If artist is empty but title is formatted as "Artist - Title", extract candidate artist
	if lowerArtist == "" && strings.Contains(cleanTitle, " - ") {
		parts := strings.SplitN(cleanTitle, " - ", 2)
		if len(parts) == 2 {
			lowerArtist = strings.ToLower(strings.TrimSpace(parts[0]))
		}
	}

	combined := lowerTitle
	if lowerArtist != "" {
		combined = lowerArtist + " " + lowerTitle
	}

	profile := TrackVibeProfile{
		Genre:      GenreUnknown,
		Vibe:       VibeUnknown,
		Version:    VersionOriginal,
		Energy:     "Medium",
		Mood:       "Neutral",
		Confidence: 0.25,
	}

	// 1. Version Detection
	if containsAny(lowerTitle, "slowed + reverb", "slowed & reverb", "slowed and reverb", "slowed reverb", "slowed+reverb") {
		profile.Version = VersionSlowedReverb
	} else if containsWord(lowerTitle, "slowed", "slowed down", "ultra slowed") {
		profile.Version = VersionSlowed
	} else if containsAny(lowerTitle, "sped up", "speed up", "spedup", "speedup") {
		profile.Version = VersionSpedUp
	} else if containsWord(lowerTitle, "nightcore") {
		profile.Version = VersionNightcore
	} else if containsWord(lowerTitle, "mashup", "mash up") {
		profile.Version = VersionMashup
	} else if containsWord(lowerTitle, "instrumental", "karaoke", "backing track", "piano cover", "guitar cover") {
		profile.Version = VersionInstrumental
	} else if containsWord(lowerTitle, "acoustic", "unplugged") {
		profile.Version = VersionAcoustic
	} else if containsWord(lowerTitle, "live", "live at", "live in", "concert", "tour live") {
		profile.Version = VersionLive
	} else if containsWord(lowerTitle, "remix", "club mix", "extended mix", "vip mix", "bootleg", "flip") {
		profile.Version = VersionRemix
	} else if containsWord(lowerTitle, "lofi", "lo-fi", "lo fi") && containsWord(lowerTitle, "version", "flip", "mix", "edit") {
		profile.Version = VersionLofi
	}

	// 2. Genre Detection
	genreDetected := false
	genreConfidenceBonus := 0.0

	// 2A. Phonk
	if containsAny(combined,
		"phonk", "drift phonk", "brazilian phonk", "kordhell", "dvrst", "interworld",
		"pharmacist", "kaito shoma", "mishashi sensei", "playaphonk", "metamorphosis",
		"montagem", "el control", "schizo", "shadow dance", "dxrk", "rapture",
		"tokyodrift", "tokyo drift", "murder in my mind", "close eyes", "neon blade",
		"vendetta", "sahara", "gigachad", "giga chad", "funk do",
	) {
		profile.Genre = GenrePhonk
		genreDetected = true
		genreConfidenceBonus = 0.45
	} else if containsAny(combined, // 2B. Punjabi
		"punjabi", "speed records", "white hill music", "sidhu moosewala", "sidhu moose wala",
		"diljit dosanjh", "karan aujla", "ap dhillon", "gurinder gill", "shubh",
		"amrit maan", "b praak", "jassie gill", "hardy sandhu", "harrdy sandhu",
		"guru randhawa", "amrinder gill", "parmish verma", "babbu maan", "garry sandhu",
		"mankirt aulakh", "prem dhillon", "jordan sandhu", "prophec", "tarsem jassar",
		"sukhe", "kaka", "nimrat khaira", "sunanda sharma", "brown munde", "elevated",
		"cheques", "baller", "we rollin", "295", "the last ride", "so high", "same beef",
		"softly", "tauba tauba", "chitta",
	) {
		profile.Genre = GenrePunjabi
		genreDetected = true
		genreConfidenceBonus = 0.45
	} else if containsAny(combined, // 2C. Bollywood / Hindi
		"bollywood", "t-series", "tseries", "zee music", "yrf", "sony music india",
		"tips official", "dharma productions", "arijit singh", "shreya ghoshal",
		"neha kakkar", "atif aslam", "pritam", "vishal-shekhar", "vishal shekhar",
		"ar rahman", "a.r. rahman", "shankar mahadevan", "alka yagnik", "kumar sanu",
		"udit narayan", "sonu nigam", "sunidhi chauhan", "mohit chauhan", "kishore kumar",
		"lata mangeshkar", "mohd rafi", "mohammed rafi", "mukesh", "asha bhosle",
		"jubin nautiyal", "kk", "badshah", "honey singh", "yo yo honey singh",
		"armaan malik", "amal mallik", "darshan raval", "stebin ben", "mithoon",
		"sachet tandon", "parampara", "dil to pagal hai", "channa mereya", "tum hi ho",
		"kal ho naa ho", "kabira", "kesariya", "shayad", "raataan lambiyan",
		"agar tum saath ho", "tere vaste", "chaleya", "lutt putt gaya", "apna bana le",
		"heeriye", "chaiyya chaiyya", "malhari", "jhoome jo pathaan", "zinda banda",
		"geet", "gaana", "film",
	) {
		profile.Genre = GenreBollywood
		genreDetected = true
		genreConfidenceBonus = 0.45
	} else if containsAny(combined, // 2D. Lo-fi
		"lofi", "lo-fi", "lo fi", "chillhop", "study beat", "chilledcow", "lofi girl",
	) {
		profile.Genre = GenreLofi
		genreDetected = true
		genreConfidenceBonus = 0.40
	} else if containsAny(combined, // 2E. Hip-Hop & Rap
		"hip hop", "hiphop", "hip-hop", "rap", "trap", "drill", "freestyle", "cypher", "boombap",
		"eminem", "drake", "2pac", "tupac", "kendrick", "kendrick lamar", "j. cole", "j cole",
		"travis scott", "juice wrld", "xxxtentacion", "lil peep", "post malone", "lil baby",
		"gunna", "future", "21 savage", "lil wayne", "kanye", "kanye west", "snoop dogg",
		"divine", "emiway", "mc stan", "seedhe maut", "raftaar", "kr$na", "krsna",
	) {
		if containsWord(combined, "rap", "drill", "freestyle", "cypher", "boombap") {
			profile.Genre = GenreRap
		} else {
			profile.Genre = GenreHipHop
		}
		genreDetected = true
		genreConfidenceBonus = 0.40
	} else if containsAny(combined, // 2F. EDM & Electronic
		"edm", "house", "techno", "trance", "dubstep", "dnb", "drum & bass", "drum and bass", "hardstyle",
		"alan walker", "marshmello", "martin garrix", "tiesto", "tiësto", "avicii", "skrillex",
		"illenium", "david guetta", "zedd", "calvin harris", "kygo", "hardwell", "dj snake",
		"chainsmokers", "dimitri vegas", "afrojack", "electronic", "electro",
	) {
		profile.Genre = GenreEDM
		genreDetected = true
		genreConfidenceBonus = 0.40
	} else if containsAny(combined, // 2G. Rock
		"rock", "hard rock", "metal", "heavy metal", "punk rock", "alternative rock",
		"queen", "ac/dc", "acdc", "linkin park", "nirvana", "metallica", "guns n roses", "guns n' roses",
		"pink floyd", "led zeppelin", "coldplay", "imagine dragons", "green day", "bon jovi",
		"aerosmith", "red hot chili peppers", "foo fighters", "slipknot", "evanescence",
	) {
		profile.Genre = GenreRock
		genreDetected = true
		genreConfidenceBonus = 0.40
	} else if containsAny(combined, // 2H. Classical & Devotional
		"bhajan", "aarti", "chalisa", "mantra", "kirtan", "stotram", "krishna", "shiva", "hanuman",
		"ganesha", "waheguru", "qawwali", "sufi", "nusrat fateh ali khan", "rahat fateh ali khan",
		"anup jalota", "anuradha paudwal", "classical", "symphony", "sonata", "concerto", "raga",
		"beethoven", "mozart", "bach", "chopin",
	) {
		if containsAny(combined, "bhajan", "aarti", "chalisa", "mantra", "kirtan", "stotram", "krishna", "shiva", "hanuman", "ganesha", "waheguru", "qawwali", "sufi") {
			profile.Genre = GenreBollywood // Devotional in Indian music space maps appropriately
			profile.Vibe = VibeDevotional
		} else {
			profile.Genre = GenreClassical
		}
		genreDetected = true
		genreConfidenceBonus = 0.40
	} else if containsAny(combined, // 2I. Indie
		"indie", "indie rock", "indie pop", "alternative", "bedroom pop", "mac demarco",
		"clairo", "rex orange county", "boy pablo", "phoebe bridgers", "arctic monkeys",
		"the neighbourhood", "sweater weather", "cigarettes after sex", "wallows", "beach house", "tame impala",
		"prateek kuhad", "anuv jain",
	) {
		profile.Genre = GenreIndie
		genreDetected = true
		genreConfidenceBonus = 0.40
	} else if containsAny(combined, // 2J. Pop
		"pop", "synthpop", "electropop", "dance pop", "k-pop", "kpop", "j-pop", "jpop",
		"taylor swift", "ariana", "ariana grande", "dua lipa", "billie eilish", "the weeknd",
		"ed sheeran", "justin bieber", "olivia rodrigo", "bruno mars", "katy perry", "rihanna",
		"shawn mendes", "selena gomez", "charlie puth", "maroon 5", "bts", "blackpink",
	) {
		profile.Genre = GenrePop
		genreDetected = true
		genreConfidenceBonus = 0.35
	}

	// 3. Vibe / Mood Detection
	vibeDetected := false
	vibeConfidenceBonus := 0.0

	if profile.Vibe == VibeNone || profile.Vibe == VibeUnknown {
		if containsAny(combined, "party", "dance", "club", "disco", "nightclub", "nacho", "nachle", "dhol", "dhamaka", "bhangra", "kala chashma", "hookah bar", "malhari", "celebration", "festival") {
			profile.Vibe = VibeParty
			vibeDetected = true
			vibeConfidenceBonus = 0.35
		} else if containsAny(combined, "romantic", "romance", "love", "crush", "in love", "sweetheart", "kiss", "valentine", "soulmate", "lovers", "mohabbat", "ishq", "pyar", "dil", "sanam", "humsafar", "tum hi ho", "pehle bhi main", "raataan lambiyan", "kesariya", "zara zara", "dil to pagal hai") {
			profile.Vibe = VibeRomantic
			vibeDetected = true
			vibeConfidenceBonus = 0.35
		} else if containsAny(combined, "sad", "depression", "depressed", "crying", "heartbreak", "heartbroken", "broken heart", "pain", "alone", "lonely", "tears", "cry", "melancholy", "dard", "judaai", "tanhai", "channa mereya", "bekhayali", "tujhe bhula diya") {
			profile.Vibe = VibeSad
			vibeDetected = true
			vibeConfidenceBonus = 0.35
		} else if containsAny(combined, "emotional", "feelings", "soulful", "heart touching", "yaad", "yaadein", "unplugged") {
			profile.Vibe = VibeEmotional
			vibeDetected = true
			vibeConfidenceBonus = 0.30
		} else if containsAny(combined, "lofi", "lo-fi", "lo fi", "chillhop") {
			profile.Vibe = VibeLofi
			vibeDetected = true
			vibeConfidenceBonus = 0.30
		} else if containsAny(combined, "chill", "relax", "relaxing", "calm", "mellow", "ambient", "peaceful", "peace", "serene", "meditation", "soothing", "sukoon") {
			profile.Vibe = VibeChill
			vibeDetected = true
			vibeConfidenceBonus = 0.30
		} else if containsAny(combined, "dark", "evil", "demon", "shadow", "goth", "gothic", "grim", "nightmare", "hell", "doom", "devil", "death", "haunted", "murder") {
			profile.Vibe = VibeDark
			vibeDetected = true
			vibeConfidenceBonus = 0.35
		} else if containsAny(combined, "aggressive", "hardcore", "rage", "anger", "fight", "violent", "combat", "raw phonk") {
			profile.Vibe = VibeAggressive
			vibeDetected = true
			vibeConfidenceBonus = 0.35
		} else if containsAny(combined, "motivational", "motivation", "inspire", "inspiring", "success", "warrior", "champion", "rise", "believer", "unstoppable", "legend") {
			profile.Vibe = VibeMotivational
			vibeDetected = true
			vibeConfidenceBonus = 0.30
		} else if containsAny(combined, "dreamy", "dreams", "dreaming", "celestial", "starlight", "clouds", "heaven", "floating", "euphoric") {
			profile.Vibe = VibeDreamy
			vibeDetected = true
			vibeConfidenceBonus = 0.30
		} else if containsAny(combined, "peaceful", "peace", "tranquil", "stillness", "serenity", "zen") {
			profile.Vibe = VibePeaceful
			vibeDetected = true
			vibeConfidenceBonus = 0.30
		} else if containsAny(combined, "bhakti", "prayer", "temple", "spiritual", "divine", "blessing", "devotion") {
			profile.Vibe = VibeDevotional
			vibeDetected = true
			vibeConfidenceBonus = 0.35
		} else if containsAny(combined, "soft", "gentle", "quiet", "whisper", "delicate", "tender", "lullaby") {
			profile.Vibe = VibeSoft
			vibeDetected = true
			vibeConfidenceBonus = 0.30
		} else if containsAny(combined, "energetic", "high energy", "boost", "power", "workout", "gym", "fitness", "beast mode", "hype", "chaiyya chaiyya", "animals") {
			profile.Vibe = VibeEnergetic
			vibeDetected = true
			vibeConfidenceBonus = 0.30
		}
	} else {
		vibeDetected = true
		vibeConfidenceBonus = 0.35
	}

	// 4. Default Vibe inferences if genre is known
	if profile.Vibe == VibeUnknown || profile.Vibe == VibeNone {
		switch profile.Genre {
		case GenrePhonk:
			if profile.Version == VersionSlowed || profile.Version == VersionSlowedReverb {
				profile.Vibe = VibeChill
			} else {
				profile.Vibe = VibeDark
			}
			vibeDetected = true
		case GenreEDM:
			profile.Vibe = VibeEnergetic
			vibeDetected = true
		case GenreLofi:
			profile.Vibe = VibeChill
			vibeDetected = true
		case GenreClassical:
			profile.Vibe = VibePeaceful
			vibeDetected = true
		case GenreInstrumental:
			profile.Vibe = VibeRelaxing
			vibeDetected = true
		default:
			profile.Vibe = VibeUnknown
		}
	}

	// 5. Energy and Mood Determination
	if profile.Version == VersionSlowed || profile.Version == VersionSlowedReverb || profile.Version == VersionAcoustic || profile.Vibe == VibeSad || profile.Vibe == VibeChill || profile.Vibe == VibeRelaxing || profile.Vibe == VibePeaceful || profile.Vibe == VibeDreamy || profile.Vibe == VibeLofi || profile.Vibe == VibeSoft {
		profile.Energy = "Low"
	} else if profile.Genre == GenrePhonk || profile.Vibe == VibeAggressive || profile.Vibe == VibeParty {
		profile.Energy = "Very High"
	} else if profile.Genre == GenreEDM || profile.Vibe == VibeEnergetic || profile.Genre == GenreHipHop || profile.Genre == GenreRap || profile.Version == VersionSpedUp || profile.Version == VersionNightcore {
		profile.Energy = "High"
	} else {
		profile.Energy = "Medium"
	}

	switch profile.Vibe {
	case VibeSad:
		profile.Mood = "Melancholy"
	case VibeRomantic:
		profile.Mood = "Loving"
	case VibeEmotional:
		profile.Mood = "Emotional"
	case VibeDark, VibeAggressive:
		profile.Mood = "Ominous"
	case VibeParty, VibeEnergetic:
		profile.Mood = "Energetic"
	case VibeDevotional:
		profile.Mood = "Devotional"
	case VibeLofi, VibeChill, VibeRelaxing, VibePeaceful, VibeSoft:
		profile.Mood = "Calm"
	default:
		if profile.Energy == "Very High" || profile.Energy == "High" {
			profile.Mood = "Energetic"
		} else {
			profile.Mood = "Neutral"
		}
	}

	// 6. Final Confidence Calculation
	if genreDetected && vibeDetected {
		profile.Confidence = math.Min(1.0, 0.25+genreConfidenceBonus+vibeConfidenceBonus)
		if profile.Confidence < 0.85 {
			profile.Confidence = 0.88
		}
	} else if genreDetected || vibeDetected {
		profile.Confidence = math.Min(1.0, 0.25+genreConfidenceBonus+vibeConfidenceBonus)
		if profile.Confidence < 0.60 {
			profile.Confidence = 0.65
		}
	} else {
		profile.Confidence = 0.25
	}

	profile.BaseQuery = cleanSongTitle(title)
	return profile
}

// ScoreCandidateDetails calculates a compatibility score (0 to 100) and detailed matching reasons
// for a candidate track against the session profile.
func (p TrackVibeProfile) ScoreCandidateDetails(c TrackVibeProfile, currentArtist, candidateArtist string) (int, []string) {
	weights := DefaultMatchWeights
	var reasons []string

	// If session profile has no verified genre and vibe, use broad metadata fallback
	if (p.Genre == GenreUnknown || p.Genre == GenreNone) && (p.Vibe == VibeUnknown || p.Vibe == VibeNone) {
		reasons = append(reasons, "Broad metadata/search fallback (unknown session profile)")
		return 20, reasons
	}

	score := 20 // baseline candidate score
	reasons = append(reasons, "Baseline candidate inclusion (+20)")

	// 1. Genre matching
	if p.Genre != GenreUnknown && p.Genre != GenreNone {
		if c.Genre == p.Genre {
			score += weights.ExactGenre
			reasons = append(reasons, "Exact genre ("+string(p.Genre)+") (+"+string(rune('0'+weights.ExactGenre/10))+string(rune('0'+weights.ExactGenre%10))+")")
		} else if isRelatedGenre(p.Genre, c.Genre) {
			score += weights.RelatedGenre
			reasons = append(reasons, "Compatible genre ("+string(c.Genre)+" related to "+string(p.Genre)+") (+"+string(rune('0'+weights.RelatedGenre/10))+string(rune('0'+weights.RelatedGenre%10))+")")
		} else if c.Genre != GenreUnknown && c.Genre != GenreNone {
			score -= 60 // strictly penalize completely different genres (e.g. Phonk vs Classical)
			reasons = append(reasons, "Conflicting genre ("+string(c.Genre)+" vs "+string(p.Genre)+") (-60)")
		}
	}

	// 2. Vibe matching
	if p.Vibe != VibeUnknown && p.Vibe != VibeNone {
		if c.Vibe == p.Vibe {
			score += weights.ExactVibe
			reasons = append(reasons, "Exact vibe ("+string(p.Vibe)+") (+"+string(rune('0'+weights.ExactVibe/10))+string(rune('0'+weights.ExactVibe%10))+")")
		} else if isCompatibleVibe(p.Vibe, c.Vibe) {
			score += weights.CompatibleVibe
			reasons = append(reasons, "Similar vibe ("+string(c.Vibe)+" compatible with "+string(p.Vibe)+") (+"+string(rune('0'+weights.CompatibleVibe/10))+string(rune('0'+weights.CompatibleVibe%10))+")")
		} else if c.Vibe != VibeUnknown && c.Vibe != VibeNone {
			score -= 25
			reasons = append(reasons, "Incompatible vibe ("+string(c.Vibe)+" vs "+string(p.Vibe)+") (-25)")
		}
	}

	// 3. Version matching
	if p.Version != VersionNone() && p.Version != VersionOriginal {
		if c.Version == p.Version {
			score += weights.ExactVersion
			reasons = append(reasons, "Exact version ("+string(p.Version)+") (+"+string(rune('0'+weights.ExactVersion/10))+string(rune('0'+weights.ExactVersion%10))+")")
		} else if isCompatibleVersion(p.Version, c.Version) {
			score += weights.CompatibleVersion
			reasons = append(reasons, "Compatible version ("+string(c.Version)+") (+"+string(rune('0'+weights.CompatibleVersion/10))+string(rune('0'+weights.CompatibleVersion%10))+")")
		} else if (p.Version == VersionSlowed || p.Version == VersionSlowedReverb) && (c.Version == VersionSpedUp || c.Version == VersionNightcore) {
			score -= 30
			reasons = append(reasons, "Opposite tempo/style penalty (-30)")
		}
	} else if c.Version == p.Version {
		score += 10
		reasons = append(reasons, "Compatible version (Original) (+10)")
	}

	// 4. Energy matching
	if p.Energy != "" && c.Energy != "" {
		if c.Energy == p.Energy {
			score += weights.SimilarEnergy
			reasons = append(reasons, "Exact energy ("+p.Energy+") (+"+string(rune('0'+weights.SimilarEnergy/10))+string(rune('0'+weights.SimilarEnergy%10))+")")
		} else if isSimilarEnergy(p.Energy, c.Energy) {
			score += 8
			reasons = append(reasons, "Similar energy ("+c.Energy+" ~ "+p.Energy+") (+8)")
		}
	}

	// 5. Artist diversity
	cArt := strings.TrimSpace(candidateArtist)
	curArt := strings.TrimSpace(currentArtist)
	if cArt != "" && curArt != "" && !strings.EqualFold(cArt, curArt) {
		score += weights.ArtistDiversity
		reasons = append(reasons, "Artist diversity (+5)")
	}

	return score, reasons
}

// ScoreCandidate calculates a compatibility score (0 to 100) of a candidate track against the session profile.
func (p TrackVibeProfile) ScoreCandidate(c TrackVibeProfile) int {
	score, _ := p.ScoreCandidateDetails(c, "", "")
	return score
}

func isRelatedGenre(g1, g2 GenreType) bool {
	if g1 == g2 {
		return true
	}
	switch g1 {
	case GenreBollywood:
		return g2 == GenrePunjabi || g2 == GenrePop || g2 == GenreClassical
	case GenrePunjabi:
		return g2 == GenreBollywood || g2 == GenrePop || g2 == GenreHipHop
	case GenreHipHop:
		return g2 == GenreRap || g2 == GenreLofi || g2 == GenrePhonk || g2 == GenrePunjabi
	case GenreRap:
		return g2 == GenreHipHop
	case GenrePhonk:
		return g2 == GenreElectronic
	case GenreEDM:
		return g2 == GenreElectronic || g2 == GenrePop
	case GenreElectronic:
		return g2 == GenreEDM || g2 == GenrePhonk
	case GenreIndie:
		return g2 == GenreRock || g2 == GenrePop || g2 == GenreLofi
	case GenreRock:
		return g2 == GenreIndie || g2 == GenrePop
	case GenreLofi:
		return g2 == GenreHipHop || g2 == GenreIndie
	case GenreClassical:
		return g2 == GenreBollywood
	}
	return false
}

func isCompatibleVibe(v1, v2 VibeType) bool {
	if v1 == v2 {
		return true
	}
	switch v1 {
	case VibeRomantic:
		return v2 == VibeEmotional || v2 == VibePeaceful || v2 == VibeDreamy || v2 == VibeChill
	case VibeSad:
		return v2 == VibeEmotional || v2 == VibeDark || v2 == VibeChill
	case VibeEmotional:
		return v2 == VibeSad || v2 == VibeRomantic || v2 == VibePeaceful
	case VibeChill:
		return v2 == VibeRelaxing || v2 == VibePeaceful || v2 == VibeLofi || v2 == VibeSoft || v2 == VibeDreamy || v2 == VibeDark
	case VibeRelaxing:
		return v2 == VibeChill || v2 == VibePeaceful || v2 == VibeDreamy || v2 == VibeSoft
	case VibeEnergetic:
		return v2 == VibeParty || v2 == VibeMotivational
	case VibeParty:
		return v2 == VibeEnergetic
	case VibeDark:
		return v2 == VibeAggressive || v2 == VibeSad || v2 == VibeChill
	case VibeAggressive:
		return v2 == VibeDark || v2 == VibeEnergetic
	case VibeMotivational:
		return v2 == VibeEnergetic
	case VibeDreamy:
		return v2 == VibePeaceful || v2 == VibeChill || v2 == VibeRomantic
	case VibePeaceful:
		return v2 == VibeRelaxing || v2 == VibeChill || v2 == VibeDevotional
	case VibeDevotional:
		return v2 == VibePeaceful || v2 == VibeEmotional
	case VibeSoft:
		return v2 == VibeChill || v2 == VibeRelaxing
	case VibeLofi:
		return v2 == VibeChill || v2 == VibeRelaxing
	}
	return false
}

func isCompatibleVersion(v1, v2 VersionType) bool {
	if v1 == v2 {
		return true
	}
	if (v1 == VersionSlowed && v2 == VersionSlowedReverb) || (v1 == VersionSlowedReverb && v2 == VersionSlowed) {
		return true
	}
	if (v1 == VersionSpedUp && v2 == VersionNightcore) || (v1 == VersionNightcore && v2 == VersionSpedUp) {
		return true
	}
	if (v1 == VersionInstrumental && v2 == VersionAcoustic) || (v1 == VersionAcoustic && v2 == VersionInstrumental) {
		return true
	}
	return false
}

func isSimilarEnergy(e1, e2 string) bool {
	if e1 == e2 {
		return true
	}
	val := func(e string) int {
		switch e {
		case "Low":
			return 1
		case "Medium":
			return 2
		case "High":
			return 3
		case "Very High":
			return 4
		default:
			return 2
		}
	}
	diff := val(e1) - val(e2)
	return diff == 1 || diff == -1
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
