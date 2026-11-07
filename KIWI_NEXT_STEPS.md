# Kiwi Music — Next Steps & Continuation Guide

## 1. Completed Tasks
- [x] Inspected entire project structure, playback flow (`play.go`), message sending (`helpers.go`), models, and dependencies.
- [x] Inspected fixed Kiwi Music template image (`assets/nowplaying/Neon Kiwi Music Bot Template.png`) and verified dimensions (1672 x 941).
- [x] Measured exact pixel coordinates for square artwork, circular requester photo, song title, requester name, and duration.
- [x] Added `golang.org/x/image v0.47.0` to `go.mod` for high-quality CatmullRom resampling and typography.
- [x] Extended `state.Track` with `RequesterID int64` and `RequesterName string` in `kanha/core/models/models.go` with full backward compatibility.
- [x] Implemented dedicated renderer in `kanha/modules/nowplaying.go` with template caching, artwork downloading, circular masking, monogram avatar fallback, and safe text truncation.
- [x] Implemented parallel background preparation (`StartNowPlayingPreparation`) in `kanha/modules/play.go` overlapping image rendering with audio download.
- [x] Integrated custom Now Playing image sending in `kanha/modules/helpers.go` with 100% reliable fallback to `showTrackMessage`.
- [x] Respects chat-level `/nothumb` configuration.
- [x] Added automated temp PNG file cleanup (older than 30 minutes).
- [x] Created unit tests in `kanha/modules/nowplaying_test.go`.
- [x] Fixed Railway/Docker build error (`NowPlayingData.Requester` field declaration) and verified full Linux CGO compilation with code 0.
- [x] Implemented Vibe-Based Autoplay Engine V2:
  - Overhauled `kanha/core/models/vibe.go`: expanded genres (Bollywood, Punjabi, Pop, Hip-Hop, Rap, Phonk, EDM, Indie, Rock, Classical, Electronic, Lo-fi, Instrumental, Other, Unknown) and vibes (Romantic, Sad, Emotional, Chill, Relaxing, Energetic, Party, Dark, Aggressive, Motivational, Dreamy, Peaceful, Devotional, Soft, Lofi, Other, Unknown).
  - Enriched dictionaries covering Indian/Bollywood (Arijit Singh, Lata Mangeshkar, Udit Narayan, Pritam, Shreya Ghoshal, T-Series, etc.), Punjabi (Sidhu Moosewala, Diljit Dosanjh, Karan Aujla, AP Dhillon, etc.), alongside Phonk, EDM, Rock, and Hip-Hop.
  - Implemented `MatchWeights` and `ScoreCandidateDetails` with clear scoring reasons, artist diversity bonuses, and strict conflict penalties (-60 conflicting genre, -30 opposite tempo).
  - Implemented weighted session profile computation with linear decay ($w_i = 1.0 + 0.75 \times i$) in `kanha/core/room.go` over a sliding ring buffer of 8 tracks.
  - Resolved empty `Genre=` and `Vibe=` production bug and prevented unknown metadata from fabricating "Moderate Vibe Match" scores (routed to labeled `fallbackMatches`).
  - Added session vibe update on successful autoplay playback (`r.Play()`) in `kanha/modules/call.go`.
  - Added structured diagnostic logging for analysis, candidate matching, and rejections.
  - Hardened provider download pipelines (`OneGrab` and `AloneMusic`) against HTML error pages and JSON error payloads.
- [x] Verified full Linux test suite passes:
  - `kanha/platforms`: 7/7 PASSED (1.05s)
  - `kanha/core/models`: 6/6 PASSED (0.02s)
  - `kanha/modules`: 26/26 PASSED (10.08s)
- [x] Verified production Linux CGO build passes with code 0 (`CGO_ENABLED=1 go build -v -trimpath -ldflags="-w -s" -o app ./cmd/app/`), generating 80MB stripped ELF binary.
- [x] Resolved Railway build failure & duplicate symbol issue:
  - Removed `ntgcalls/resolver_compat.c` (commit `a0705e9`).
  - Hardened `cmd/app/glibc_compatibility.h` with `__attribute__((weak))` on all resolver functions (`__dn_expand`, `__res_nquery`, `__res_nclose`, `__res_ninit`).
  - Added `-lresolv` to `#cgo linux LDFLAGS` in `cmd/app/main.go`.
  - Replaced `RUN go mod tidy` with `RUN go mod download` in `Dockerfile`.
  - Verified clean GOCACHE build with exit code 0.
- [x] Maintained persistent context documents (`KIWI_PROJECT_CONTEXT.md`, `KIWI_NEXT_STEPS.md`).

## 2. Incomplete Tasks / Remaining Items
- [ ] End-to-end testing with a live Telegram bot instance in voice chat.
- [ ] (Optional) Add a custom TTF font file at `assets/nowplaying/fonts/music.ttf` if the user prefers a distinct custom typeface beyond the default Go Bold typography.

## 3. Current Errors
- **None.** The full production Linux CGO build passes completely with exit code 0, linking `libntgcalls.a` and `libtdjson.so.1.8.66`.

## 4. Exact Next Actions for Future AI Sessions
1. First read:
   - `KIWI_PROJECT_CONTEXT.md`
   - `KIWI_NEXT_STEPS.md`
2. Check `git status` to verify working branch state.
3. If running tests on Linux/WSL:
   ```bash
   go test -v ./kanha/core/models
   CGO_LDFLAGS="-L/home/dell/build -lntgcalls -lm -lz -lresolv" go test -v ./kanha/platforms
   CGO_LDFLAGS="-L/home/dell/build -lntgcalls -lm -lz -lresolv" go test -v ./kanha/modules
   ```
4. If building production binary:
   ```bash
   CGO_ENABLED=1 go build -v -trimpath -ldflags="-w -s" -o app ./cmd/app/
   ```

## 5. Commands to Run

### Format Code:
```bash
gofmt -w kanha/core/models/vibe.go kanha/core/room.go kanha/modules/call.go kanha/platforms/onegrab.go kanha/platforms/alonemusic.go
```

### Build Docker Container (Production Linux Environment):
```bash
docker build -t kiwi-music .
```

### Run Docker Container:
```bash
docker run --env-file .env kiwi-music
```

## 6. Files to Inspect Before Continuing
- `kanha/core/models/vibe.go`
- `kanha/core/models/vibe_test.go`
- `kanha/core/room.go`
- `kanha/modules/call.go`
- `kanha/modules/autoplay_test.go`
- `kanha/platforms/onegrab.go`
- `kanha/platforms/alonemusic.go`
