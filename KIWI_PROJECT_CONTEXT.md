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

