# Kiwi Music — Persistent Project Context

## 1. Project Architecture Overview
Kiwi Music is a high-performance Telegram music streaming bot (forked from KanhaMusic, repository: `Sangamlabs/kiwixkanha`).
The codebase is written in Go and structured modularly:
- `cmd/app/`: Application entry point (`main.go`, `dependencies.go`).
- `kanha/core/`: Bot core, room management (`RoomState`), assistants, chat states, and call handling.
- `kanha/core/models/`: Core contracts and models (`Track`, `Platform`, `PlatformName`).
- `kanha/modules/`: Bot command handlers, callbacks, and playback helpers (`play.go`, `helpers.go`, `nowplaying.go`, `call.go`, `skip.go`, etc.).
- `kanha/platforms/`: Music source provider plugins (YouTube, Spotify, Deezer, AloneMusic, OneGrab, MeowAPI).
- `kanha/database/`: MongoDB persistence layer (settings, auth users, sudo, language, blacklist, etc.).
- `kanha/utils/`: Formatting, duration helpers, flood control, parsing, and HTML escaping.
- `kanha/locales/`: Multi-language i18n support.
- `ntgcalls/`: Native WebRTC Telegram voice-chat streaming engine bindings (CGO wrapper linking `libntgcalls.a`).
- `kanha/meow/`: TDLib JSON API client wrapper (`github.com/Kanha/Meow` replaced locally with `./kanha/meow`).

## 2. Important Source Files
- `kanha/modules/nowplaying.go`: Custom Now Playing image renderer and asynchronous task coordination.
- `kanha/modules/play.go`: Command handlers (`/play`, `/fplay`, `/vplay`, `/cplay`), queue validation, downloading, and playback trigger.
- `kanha/modules/helpers.go`: Message presentation layer, `sendNowPlaying`, `showTrackMessage` fallback, logging, and permissions.
- `kanha/core/models/models.go`: Definitions of `Track` (with `RequesterID`, `RequesterName`) and `Platform`.
- `kanha/modules/thumbnail.go`: Command handler for `/nothumb` (chat-level thumbnail toggling).
- `kanha/modules/call.go`: Voice chat event listeners and stream transitions (`onStreamEndHandler`, queue pop, autoplay).
- `kanha/modules/skip.go`: Skip and next track orchestration.

## 3. Playback Flow
1. **User Request:** User issues `/play <query>` (or `/fplay`, `/vplay`, `/cplay`).
2. **Permission & Room Setup:** `canUsePlayCommand`, `getEffectiveRoom`, and `ensureVoiceChatReady` verify assistant account availability and supergroup status.
3. **Track Resolution:** `platforms.GetTracks` queries registered platform providers according to priority (Spotify, YouTube, OneGrab, etc.).
4. **Queue Check:** Duplicate track rejection, queue limit checks, and duration limits applied.
5. **Parallel Image Start:** As soon as the main track is identified, `StartNowPlayingPreparation` kicks off background thumbnail rendering.
6. **Audio Download:** In parallel, `downloadFirstTrack` calls `platforms.Download` (using `yt-dlp` or API streaming).
7. **Voice-Chat Playback:** `r.Play(track, filePath, force)` feeds the audio stream to the `ntgcalls` WebRTC engine.
8. **Now Playing Message:** `finalizePlayReply` calls `sendNowPlaying`, which serves the custom image (or old fallback).

## 4. Now Playing Flow & Fallback Architecture
- **Primary:** `trySendCustomNowPlaying` queries `GetActiveNowPlayingTask` or renders on-demand with `GenerateNowPlayingImage`.
- **Photo Delivery:** Sends via `c.SendPhoto` or edits existing status message via `c.EditMessageMedia` using `&td.InputFileLocal{Path: imagePath}`.
- **Safety Fallback:** If custom rendering or upload fails, `showTrackMessage` is invoked unchanged, preserving remote URL thumbnails and text-only formatting.
- **No-Thumb Respect:** If the chat has disabled thumbnails via `/nothumb`, custom images are bypassed and text-only formatting is respected.

## 5. Music Provider Architecture
Music providers register into `platforms.Registry` and implement `models.Platform`:
- `Priority()`: Platform resolution preference order.
- `CanGet(query)` / `Get(query, video)`: Metadata extraction.
- `CanDownload(source)` / `Download(ctx, track, msg)`: Audio acquisition.
Existing providers (OneGrab, AloneMusic, MeowAPI, Spotify, YouTube) remain untouched.

## 6. Project Constraints & Target Environment
- **Go Version:** `1.26.0` (as specified in `go.mod`).
- **Target OS:** Linux (Debian Bookworm) in Docker / Koyeb / Heroku / VPS.
- **CGO Requirement:** `ntgcalls` requires `CGO_ENABLED=1` on Linux with GCC to link `libntgcalls.a` and `libtdjson.so.1.8.66`. Note that Windows hosts do not have the Linux ELF static libraries for CGO, but pure Go packages and cross-compilation compile cleanly.
- **Dependencies:**
  - `github.com/Kanha/Meow` (local replace `./kanha/meow`)
  - `github.com/amarnathcjd/gogram v1.7.71`
  - `golang.org/x/image v0.47.0` (for image composition and font rendering)
  - `resty.dev/v3`
  - `go.mongodb.org/mongo-driver/v2`

## 7. Important Decisions Made
1. **Zero Playback Disruption:** Image generation runs completely in background goroutines. Audio download and playback start without waiting for image completion.
2. **Untouched Fallback:** `showTrackMessage()` was kept 100% intact so any image failure transparently falls back to the original message format.
3. **Template Preservation:** Fixed `assets/nowplaying/Neon Kiwi Music Bot Template.png` is preserved without modifying any template artwork or branding.
4. **Resampling Quality:** High-quality `CatmullRom` scaling was chosen for album artwork and profile photos to avoid nearest-neighbor pixelation on high-res displays.
5. **Bundled Typography:** Built-in Go TrueType fonts (`Go Bold`, `Go Regular`) are used with an optional override to `assets/nowplaying/fonts/music.ttf`, ensuring zero system font dependencies on minimal Docker/Linux images.
6. **Vibe Detection & Session-Based Autoplay:**
   - Tracks are classified into:
     - Genre: `Phonk`, `Hip-Hop`, `Pop`, `EDM`, `Indie`
     - Version: `Original`, `Remix`, `Slowed`, `Slowed + Reverb`, `Sped Up`, `Nightcore`, `Instrumental`, `Acoustic`
     - Vibe: `Lofi`, `Soft`, `Sad`, `Romantic`, `Chill`, `Dark`
     - Energy: `High`, `Medium`, `Low`
     - Mood: `Melancholy`, `Energetic`, `Calm`, `Loving`, `Ominous`, `Neutral`
   - Session Vibe Retention: Room session profile is maintained across autoplay iterations so that e.g. "Slowed Phonk" always continues playing "Slowed Phonk", without genre drift.
   - Candidate Filtering & Scoring: Positive scores (+45 genre, +35 version, +25 vibe) reward exact matches; negative score (-60) strictly filters out conflicting genres.
7. **Reliable `/skip` & Queue Priority:**
   - `/skip` skips the current track and immediately dequeues the next manual queued track.
   - If the queue is empty, `/skip` stops playback (`core.DropRoom(r.ID)`) and **never** triggers autoplay.
   - Explicit `/askip` retains on-demand autoplay recommendation skipping.
   - Natural stream completion checks the manual queue first and only invokes autoplay when the manual queue is empty.
   - Playback race conditions (stale workers from previous generations or manual `/play`/`/playforce` overrides) are guarded with per-room generation tokens and cancellable contexts.



8. **Track Identity Verification & Telegram Audio Cache Engine:**
   - **Root Cause of Wrong Song Playing:**
     - Query resolution fallback in `platforms.GetTracks` previously fell through to replied Telegram media (`fromReply`) when `searchQuery` failed, silently hijacking manual requests like `/playforce Dil To Pagal Hai` with unrelated files.
     - Unverified title similarity allowed Telegram cache and disk cache to map unrelated audio files (e.g., returning a 45s audio clip for a 334s song).
     - Disk cache in `findFile(track)` returned existing poisoned files without duration/integrity verification.
     - Title normalization previously regex-stripped version tags (e.g., "remix"), collapsing "Dil To Pagal Hai (Remix)" and "Dil To Pagal Hai" into the same query.
   - **Strict Canonical Track Identity:**
     - `CanonicalRecordingKey(title, artist)` creates strict keys: `"<artist> - <title> [<version>]"`.
     - Preserves distinction between Original, Remix, Slowed, Sped Up, and Live recordings.
     - Added `Artist`, `ISRC`, `FileUniqueID`, `CachedFileID`, `CacheMatchReason` to `models.Track`.
   - **Telegram Audio Cache (`kanha/platforms/telegram_cache.go`):**
     - Matches on exact `ProviderTrackID` + `Source` (Priority 1) or `ISRC` (Priority 2).
     - Matches canonical recording key only if version, artist, and duration (within 6s or 5% tolerance) match (Priority 3).
     - Strictly rejects title similarity alone without verified identity.
   - **Pre-Playback Audio Stream Validation (`ValidateTrackAudio`):**
     - Uses `ffprobe` (`ProbeAudioStream`) to inspect audio codec, sample rate, channels, tags, and exact duration.
     - Rejects non-audio, corrupt headers, HTML error payloads, and files smaller than 32KB.
     - Validates actual decoding of first 1.5 seconds via `ffmpeg -v error -t 1.5 -f null -`.
     - Strictly validates probed duration against track metadata (within 20% or 30s tolerance).
   - **Intelligent Provider Fallback:**
     - Iterates registered providers in order; if a provider returns an audio file that fails duration or decoding validation, it logs a warning, purges the invalid file, and seamlessly falls back to the next provider.
   - **Diagnostic Logging:**
     - Comprehensive log lines emitted at every stage: Query, SelectedProvider, ProviderTrackID, ReturnedTitle, ReturnedArtist, TelegramFileUniqueID, CacheMatchReason, ActualSize, ActualDuration, Validation Result, and FinalPlaybackTrackID.


9. **Vibe-Based Autoplay Engine V2:**
   - **Expanded Classification Dictionaries:**
     - Added comprehensive genres: `Bollywood`, `Punjabi`, `Pop`, `Hip-Hop`, `Rap`, `Phonk`, `EDM`, `Indie`, `Rock`, `Classical`, `Electronic`, `Lo-fi`, `Instrumental`, `Other`, `Unknown`.
     - Added comprehensive vibes: `Romantic`, `Sad`, `Emotional`, `Chill`, `Relaxing`, `Energetic`, `Party`, `Dark`, `Aggressive`, `Motivational`, `Dreamy`, `Peaceful`, `Devotional`, `Soft`, `Lofi`, `Other`, `Unknown`.
     - Added comprehensive versions: `Original`, `Slowed`, `Slowed + Reverb`, `Sped Up`, `Nightcore`, `Remix`, `Mashup`, `Instrumental`, `Acoustic`, `Lofi`, `Live`.
     - Enriched dictionaries covering Indian/Bollywood (Arijit Singh, Lata Mangeshkar, Udit Narayan, Pritam, Shreya Ghoshal, T-Series, etc.), Punjabi (Sidhu Moosewala, Diljit Dosanjh, Karan Aujla, AP Dhillon, etc.), Devotional/Bhajan/Sufi, alongside Western Phonk, EDM, Rock, and Hip-Hop.
     - Clean YouTube clutter stripping while strictly preserving musical version tokens.
     - Explicit `Unknown` enum fallback instead of empty strings `""` when evidence is below confidence threshold ($0.40$).
   - **Weighted Session Vibe Profile:**
     - Session profile is computed over a sliding history buffer of up to 8 tracks per room.
     - Linear decay weighting ($w_i = 1.0 + 0.75 \times i$) grants recent tracks highest influence while smoothing out single-track anomalies.
     - Autoplay playback successes update session vibe upon `r.Play()`, guaranteeing continuous session evolution without manual intervention.
   - **Candidate Scoring & Fallback Pools:**
     - Differentiates `exactMatches` ($\ge 70$), `moderateMatches` ($\ge 45$), and labeled `fallbackMatches`.
     - Rejects fabricating high match scores when profile is unknown (routes explicitly to fallback pool).
     - Scores include exact/compatible vibe, genre, version, energy, and artist diversity bonus.
     - Heavy penalties (-60 conflicting genre, -30 opposite tempo) eliminate jarring switches.
   - **Provider Payload Sanitization:**
     - `OneGrab` and `AloneMusic` download pipelines verify `Content-Type` headers (rejecting `text/html`, `text/plain`, `application/json`).
     - File inspection checks initial 512 bytes for HTML doctype, tags, or JSON error messages, failing early before corrupt audio reaches ffmpeg/ntgcalls.
