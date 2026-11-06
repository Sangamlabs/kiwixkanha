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
- [x] Created persistent context documents (`KIWI_NOWPLAYING_IMPLEMENTATION.md`, `KIWI_PROJECT_CONTEXT.md`, `KIWI_NEXT_STEPS.md`).

## 2. Incomplete Tasks / Remaining Items
- [ ] End-to-end testing with a live Telegram bot instance in voice chat.
- [ ] (Optional) Add a custom TTF font file at `assets/nowplaying/fonts/music.ttf` if the user prefers a distinct custom typeface beyond the default Go Bold typography.

## 3. Current Errors
- **None.** The full production Linux CGO build (`CGO_ENABLED=1 go build -v -trimpath -ldflags="-w -s" -o app ./cmd/app/`) passes completely with exit code 0, successfully linking `libntgcalls.a` and `libtdjson.so.1.8.66`.

## 4. Exact Next Actions for Future AI Sessions
1. First read:
   - `KIWI_PROJECT_CONTEXT.md`
   - `KIWI_NOWPLAYING_IMPLEMENTATION.md`
   - `KIWI_NEXT_STEPS.md`
2. Check `git status` to verify working branch state.
3. If running the bot in Docker, use the commands below.
4. If modifying any Now Playing rendering logic, update coordinates and tests in `kanha/modules/nowplaying.go`.

## 5. Commands to Run

### Format Code:
```bash
gofmt -w kanha/modules/nowplaying.go kanha/modules/play.go kanha/modules/helpers.go kanha/core/models/models.go
```

### Build Docker Container (Production Linux Environment):
```bash
docker build -t kiwi-music .
```

### Run Docker Container:
```bash
docker run --env-file .env kiwi-music
```

### Local Linux Build (if on Linux VPS / Debian / Ubuntu):
```bash
chmod +x install.sh
./install.sh
CGO_ENABLED=1 go build -v -trimpath -ldflags="-w -s" -o app ./cmd/app/
./app
```

## 6. Files to Inspect Before Continuing
- `kanha/modules/nowplaying.go`
- `kanha/modules/helpers.go`
- `kanha/modules/play.go`
- `kanha/core/models/models.go`
- `assets/nowplaying/Neon Kiwi Music Bot Template.png`

