# Kiwi Music — Custom Now Playing Thumbnail System Implementation

## 1. Project Objective
Implement a high-performance, dynamic image generation system for Kiwi Music (a production Telegram music bot forked from KanhaMusic, repository: `Sangamlabs/kiwixkanha`). The system dynamically overlays song metadata, center-cropped square album artwork, circular requester profile photos, and duration onto a fixed, pre-existing Kiwi Music neon theme template without disrupting playback speed or existing functionality.

## 2. Current Implementation Status
- **Status:** Completed & Integrated.
- **Renderer:** Implemented in `kanha/modules/nowplaying.go`.
- **Parallel Pipeline:** Integrated in `kanha/modules/play.go` (starts background rendering concurrently with audio downloading).
- **Fallback Guarantee:** Integrated in `kanha/modules/helpers.go` (`sendNowPlaying` seamlessly attempts custom image and automatically falls back to `showTrackMessage` upon any failure).
- **Model Enhancements:** `RequesterID` and `RequesterName` added to `kanha/core/models/models.go:Track` with full backward compatibility.
- **Verification:** Image generation pipeline tested end-to-end, producing verified high-quality 1672x941 PNGs in ~200ms.

## 3. Template Image Specifications
- **Template File Path:** `assets/nowplaying/Neon Kiwi Music Bot Template.png`
- **Template Dimensions:** `1672 x 941` pixels (PNG format, 24-bit/32-bit color).
- **Design Elements Present on Template:**
  - Mascot on left (X: 0 to ~650) with glowing headphones, jewelry, and red saree.
  - "Kiwi Music" neon logo & subtitle at top right (Y: 100 to ~400).
  - "▶ Now Playing ♫" neon badge at X: 820..1080, Y: 480..530.
  - Decorative audio waveform at Y: 670..750 (preserved as decorative art).
  - Playback progress slider bar at Y: 760..770 (preserved).
  - Player control buttons at Y: 810..880 (preserved).
  - Cherry blossom floral accents on frame corners.

## 4. Exact Image Placement Coordinates
All coordinates were measured via border peak luminance detection and verified visually:

### A. Square Song Artwork Frame
- **Outer Glow / Border Bounds:** X: 679 to 841, Y: 529 to 690 (Size: 162 x 161 px)
- **Inner Frame Placement (Target Box):**
  - Top-Left: `X = 684`, `Y = 534`
  - Dimensions: `152 x 152` px (center: X=760, Y=610)
  - Processing: 1:1 center-cropped square, resized using `golang.org/x/image/draw.CatmullRom`, with antialiased rounded corners (radius `r = 16px`).

### B. Circular Requester Profile Photo Frame
- **Outer Neon Ring Center:** `X = 1506`, `Y = 608`
- **Target Dimensions:** `232 x 232` px (radius `r = 116px`, outer neon ring diameter ~245px)
- **Top-Left Drawing Position:** `X = 1390`, `Y = 492`
- **Processing:** 1:1 center-cropped, resized using `golang.org/x/image/draw.CatmullRom`, with smooth antialiased circular mask.
- **Fallback Avatar:** When user has no profile photo, privacy restricts it, or TDLib download fails, a stylized gradient avatar matching the Kiwi neon aesthetic is dynamically generated with the user's initial or "K".

### C. Text Placement Coordinates
- **Base X Position:** `X = 860`
- **Maximum Text Width:** `480` px (text exceeding this is safely truncated with ellipsis `...`)
- **Song Title:**
  - Baseline Y: `572`
  - Font: `Go Bold` (30pt)
  - Color: Pure White (`#FFFFFF`)
- **Requester Name:**
  - Baseline Y: `615`
  - Text: `Requested by: {Name}` (HTML tags stripped)
  - Font: `Go Bold` (21pt)
  - Color: Neon Soft Pink (`#FFAFD8`)
- **Duration:**
  - Baseline Y: `650`
  - Text: `Duration: {MM:SS}`
  - Font: `Go Regular` (19pt)
  - Color: Pale Pink (`#FFE0EE`)

## 5. Files Created & Modified

### Created:
1. `kanha/modules/nowplaying.go`: Core image generation engine, caching layer, typography, and async task orchestration.
2. `kanha/modules/nowplaying_test.go`: Unit tests for template loading, font parsing, sanitization, image rendering, and concurrency safety.
3. `KIWI_NOWPLAYING_IMPLEMENTATION.md`: Implementation documentation.
4. `KIWI_PROJECT_CONTEXT.md`: High-level architectural context for future sessions.
5. `KIWI_NEXT_STEPS.md`: Action items and session continuation guide.

### Modified:
1. `kanha/core/models/models.go`:
   - Added `RequesterID int64` and `RequesterName string` to `Track`.
   - Updated `MarkAutoplay` to initialize autoplay requester fields.
2. `kanha/modules/play.go`:
   - Populated `track.RequesterID` and `track.RequesterName` from message and sender.
   - Added `StartNowPlayingPreparation(...)` in `playTracksAndRespond` before `downloadFirstTrack`.
3. `kanha/modules/helpers.go`:
   - Enhanced `sendNowPlaying` to prioritize custom Kiwi Music image via `trySendCustomNowPlaying`.
   - Maintained `showTrackMessage` as the 100% untouched fallback.
4. `go.mod` / `go.sum`:
   - Added `golang.org/x/image v0.47.0` for high-quality CatmullRom resampling and TrueType/OpenType typography.

## 6. Functions Added & Modified

### Functions Added (`kanha/modules/nowplaying.go`):
- `GenerateNowPlayingImage(ctx context.Context, c *td.Client, data NowPlayingData) (string, error)`
- `StartNowPlayingPreparation(parentCtx context.Context, c *td.Client, chatID int64, track *state.Track, sender *td.User) *NowPlayingTask`
- `GetActiveNowPlayingTask(chatID int64, trackID string) *NowPlayingTask`
- `(t *NowPlayingTask) Wait(timeout time.Duration) (string, error)`
- `loadCachedTemplate() (*image.RGBA, error)`
- `locateTemplate() (string, error)`
- `initFonts() error`
- `fetchArtwork(ctx context.Context, artworkURL string) (image.Image, error)`
- `fetchOrGenerateProfilePhoto(ctx context.Context, c *td.Client, userID int64, userName string) image.Image`
- `createInitialsAvatar(size int, initial string, face font.Face) image.Image`
- `cropCenterAndResize(src image.Image, targetW, targetH int) image.Image`
- `applyRoundedCorners(src image.Image, r int) image.Image`
- `applyCircularMask(src image.Image) image.Image`
- `drawText(dst draw.Image, face font.Face, text string, x, y, maxWidth int, col color.Color)`
- `sanitizeRequesterName(raw string) string`
- `generateOutputPath(trackID string) string`
- `cleanOldTempFiles()`

### Functions Modified (`kanha/modules/helpers.go`):
- `sendNowPlaying`: Checks `/nothumb` setting, tries `trySendCustomNowPlaying`, and falls back to `showTrackMessage`.
- `trySendCustomNowPlaying`: Coordinates task retrieval, on-demand generation, `EditMessageMedia`, and `SendPhoto` with local image file.

### Functions Modified (`kanha/modules/play.go`):
- `playTracksAndRespond`: Assigns `RequesterID` and `RequesterName` to tracks and triggers background preparation before starting audio download.

### Functions Modified (`kanha/core/models/models.go`):
- `Track`: Extended with `RequesterID int64` and `RequesterName string`.
- `MarkAutoplay`: Sets `RequesterName` and `RequesterID`.

## 7. Parallel Processing Architecture
1. User sends `/play <query>`.
2. Metadata is resolved and track is selected (`tracks[0]`).
3. If track will start playing immediately:
   - `StartNowPlayingPreparation(ctx, c, chatID, track, sender)` is invoked in a non-blocking background goroutine.
   - Concurrently, `downloadFirstTrack(c, replyMsg, chatID, mention, track)` starts downloading the audio file via platform engines (`yt-dlp`, `ffmpeg`, etc.).
4. Audio download and voice chat playback start completely independent of image generation.
5. When playback starts, `finalizePlayReply` calls `sendNowPlaying`.
6. `sendNowPlaying` checks if the image task is ready (waiting up to 2 seconds if still in flight):
   - If ready: sends custom image via `c.SendPhoto` / `c.EditMessageMedia`.
   - If timed out or failed: immediately calls the old fallback `showTrackMessage`.
7. Voice chat playback is never blocked, delayed, or interrupted.

## 8. Fallback Architecture
The existing `showTrackMessage` function remains completely functional and acts as the safety net:
- If template PNG is missing: fallback to `showTrackMessage`.
- If artwork URL fails or times out: fallback to `showTrackMessage`.
- If profile photo fails: fallback initials avatar is used; if rendering fails: fallback to `showTrackMessage`.
- If PNG generation fails: fallback to `showTrackMessage`.
- If Telegram image upload fails: fallback to `showTrackMessage`.
- If `/nothumb` is enabled in chat: immediately uses `showTrackMessage` (text-only).

## 9. Caching & Resource Management
1. **Template:** Loaded and decoded into `*image.RGBA` once via `sync.Once`. Each render draws from this read-only buffer into an independent canvas.
2. **Artwork:** Cached by URL in memory (`sync.Map`) with a 15-minute TTL to prevent duplicate downloads.
3. **Profile Photos:** Cached by Telegram UserID in memory with a 10-minute TTL.
4. **Temporary Images:** Saved in `os.TempDir()/kiwi_nowplaying/`. Cleaned automatically every 10 minutes (files older than 30 minutes are purged).

## 10. Telegram Profile Photo Retrieval
- TDLib client `c.GetUser(userID)` checks `user.ProfilePhoto.Small` (or `.Big`).
- If already downloaded locally, uses `Local.Path`.
- If not yet downloaded, calls `c.DownloadFile(fileID, 0, 0, 32, &td.DownloadFileOpts{Synchronous: true})`.
- If user has no photo or privacy blocks access, smoothly falls back to dynamically rendering a stylized monogram avatar.

