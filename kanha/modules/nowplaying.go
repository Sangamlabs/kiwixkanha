/*
 * ● Kiwi Music (KanhaMusic fork)
 * ○ Custom Now Playing Thumbnail System
 *
 * Repository: Sangamlabs/kiwixkanha
 */

package modules

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	state "KanhaMusic/kanha/core/models"
	"KanhaMusic/kanha/logger"
	"KanhaMusic/kanha/utils"
	td "github.com/Kanha/Meow"
)

// NowPlayingData holds the necessary metadata for generating the Now Playing thumbnail.
type NowPlayingData struct {
	SongTitle     string
	RequesterName string
	RequesterID   int64
	ArtworkURL    string
	Duration      int
	TrackID       string
}

// NowPlayingTask represents a background image rendering task.
type NowPlayingTask struct {
	TrackID   string
	ChatID    int64
	ImagePath string
	Err       error
	Done      chan struct{}
	cancel    context.CancelFunc
}

// Internal coordinates on the 1672 x 941 Kiwi Music template
const (
	templateWidth  = 1672
	templateHeight = 941

	// Square artwork frame
	artworkTargetSize = 152
	artworkCornerR    = 16
	artworkPosX       = 684
	artworkPosY       = 534

	// Circular requester profile frame
	profileTargetSize = 232
	profileCenterRelX = 1506
	profileCenterRelY = 608

	// Text area bounds
	textStartX    = 860
	textMaxWidth  = 480
	titlePosY     = 572
	requesterPosY = 615
	durationPosY  = 650
)

var (
	htmlTagRegex = regexp.MustCompile(`<[^>]*>`)

	// Template cache
	templateOnce   sync.Once
	cachedTemplate *image.RGBA
	templateErr    error

	// Font cache
	fontOnce       sync.Once
	titleFontFace  font.Face
	reqFontFace    font.Face
	durFontFace    font.Face
	avatarFontFace font.Face
	fontsErr       error

	// HTTP client with strict timeout for artwork fetching
	artHttpClient = &http.Client{
		Timeout: 5 * time.Second,
	}

	// Memory cache for artwork images (URL -> cacheEntry)
	artworkCache sync.Map

	// Memory cache for user profile photo files (UserID -> cacheEntry)
	profileCache sync.Map

	// Active rendering tasks registry to prevent duplicate work and race conditions
	activeTasksMu sync.Mutex
	activeTasks   = make(map[string]*NowPlayingTask)

	// Temp directory for generated images
	tempDirOnce sync.Once
	tempDir     string
)

type artworkCacheEntry struct {
	img       image.Image
	timestamp time.Time
}

type profileCacheEntry struct {
	path      string
	timestamp time.Time
}

func getTempDir() string {
	tempDirOnce.Do(func() {
		tempDir = filepath.Join(os.TempDir(), "kiwi_nowplaying")
		_ = os.MkdirAll(tempDir, 0755)

		// Start periodic cleaner for old temporary PNGs
		go cleanOldTempFiles()
	})
	return tempDir
}

func cleanOldTempFiles() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		dir := tempDir
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		now := time.Now()
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".png") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			// Delete images older than 30 minutes
			if now.Sub(info.ModTime()) > 30*time.Minute {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
}

// locateTemplate finds the Kiwi Music template PNG file.
func locateTemplate() (string, error) {
	candidates := []string{
		filepath.Join("assets", "nowplaying", "Neon Kiwi Music Bot Template.png"),
		filepath.Join("assets", "nowplaying", "template.png"),
		filepath.Join("..", "assets", "nowplaying", "Neon Kiwi Music Bot Template.png"),
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("kiwi music template image not found in assets/nowplaying")
}

// loadCachedTemplate loads and decodes the template PNG once into memory.
func loadCachedTemplate() (*image.RGBA, error) {
	templateOnce.Do(func() {
		path, err := locateTemplate()
		if err != nil {
			templateErr = err
			return
		}

		f, err := os.Open(path)
		if err != nil {
			templateErr = fmt.Errorf("failed to open template: %w", err)
			return
		}
		defer f.Close()

		decoded, err := png.Decode(f)
		if err != nil {
			templateErr = fmt.Errorf("failed to decode template PNG: %w", err)
			return
		}

		b := decoded.Bounds()
		cachedTemplate = image.NewRGBA(b)
		draw.Draw(cachedTemplate, b, decoded, b.Min, draw.Src)
		logger.Infof("[NowPlaying] Loaded and cached Kiwi Music template (%dx%d)", b.Dx(), b.Dy())
	})

	if templateErr != nil {
		return nil, templateErr
	}
	return cachedTemplate, nil
}

// initFonts loads the typography faces for rendering text.
func initFonts() error {
	fontOnce.Do(func() {
		// Check for optional custom font file
		customFontPath := filepath.Join("assets", "nowplaying", "fonts", "music.ttf")
		var fontBytes []byte
		if data, err := os.ReadFile(customFontPath); err == nil && len(data) > 0 {
			fontBytes = data
			logger.Infof("[NowPlaying] Using custom font from %s", customFontPath)
		} else {
			fontBytes = gobold.TTF
		}

		titleFace, err := parseFontFace(fontBytes, 30)
		if err != nil {
			fontsErr = fmt.Errorf("failed to parse title font: %w", err)
			return
		}
		titleFontFace = titleFace

		reqFace, err := parseFontFace(fontBytes, 21)
		if err != nil {
			fontsErr = fmt.Errorf("failed to parse requester font: %w", err)
			return
		}
		reqFontFace = reqFace

		durFace, err := parseFontFace(goregular.TTF, 19)
		if err != nil {
			fontsErr = fmt.Errorf("failed to parse duration font: %w", err)
			return
		}
		durFontFace = durFace

		avatarFace, err := parseFontFace(gobold.TTF, 72)
		if err != nil {
			fontsErr = fmt.Errorf("failed to parse avatar font: %w", err)
			return
		}
		avatarFontFace = avatarFace
	})

	return fontsErr
}

func parseFontFace(fontBytes []byte, points float64) (font.Face, error) {
	otFont, err := opentype.Parse(fontBytes)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(otFont, &opentype.FaceOptions{
		Size:    points,
		DPI:     72,
		Hinting: font.HintingFull,
	})
}

// GenerateNowPlayingImage creates a custom Now Playing image on top of the fixed Kiwi Music template.
func GenerateNowPlayingImage(
	ctx context.Context,
	c *td.Client,
	data NowPlayingData,
) (string, error) {
	// 1. Load cached template
	tpl, err := loadCachedTemplate()
	if err != nil {
		return "", fmt.Errorf("template unavailable: %w", err)
	}

	// 2. Initialize fonts
	if err := initFonts(); err != nil {
		return "", fmt.Errorf("fonts unavailable: %w", err)
	}

	// 3. Make a fresh canvas copy for this render to ensure concurrency safety
	bounds := tpl.Bounds()
	canvas := image.NewRGBA(bounds)
	draw.Draw(canvas, bounds, tpl, bounds.Min, draw.Src)

	// 4. Download and draw song artwork in square frame
	artImg, err := fetchArtwork(ctx, data.ArtworkURL)
	if err != nil {
		logger.Warnf("[NowPlaying] Failed to fetch artwork %q: %v", data.ArtworkURL, err)
		// Artwork failure will cause caller to fallback to old Now Playing system
		return "", fmt.Errorf("failed to fetch song artwork: %w", err)
	}

	squareArt := cropCenterAndResize(artImg, artworkTargetSize, artworkTargetSize)
	maskedArt := applyRoundedCorners(squareArt, artworkCornerR)
	draw.Draw(
		canvas,
		image.Rect(artworkPosX, artworkPosY, artworkPosX+artworkTargetSize, artworkPosY+artworkTargetSize),
		maskedArt,
		image.Point{},
		draw.Over,
	)

	// 5. Fetch or generate requester profile photo in circular frame
	profileImg := fetchOrGenerateProfilePhoto(ctx, c, data.RequesterID, data.RequesterName)
	maskedProfile := applyCircularMask(profileImg)
	profX := profileCenterRelX - profileTargetSize/2
	profY := profileCenterRelY - profileTargetSize/2
	draw.Draw(
		canvas,
		image.Rect(profX, profY, profX+profileTargetSize, profY+profileTargetSize),
		maskedProfile,
		image.Point{},
		draw.Over,
	)

	// 6. Draw song title (bold white, with safe truncation)
	titleText := strings.TrimSpace(data.SongTitle)
	if titleText == "" {
		titleText = "Unknown Track"
	}
	drawText(canvas, titleFontFace, titleText, textStartX, titlePosY, textMaxWidth, color.White)

	// 7. Draw requester name (soft pink, with safe truncation)
	cleanRequester := sanitizeRequesterName(data.RequesterName)
	if cleanRequester == "" {
		cleanRequester = sanitizeRequesterName(data.Requester)
	}
	if cleanRequester == "" {
		cleanRequester = "Music Lover"
	}
	reqDisplay := "Requested by: " + cleanRequester
	drawText(canvas, reqFontFace, reqDisplay, textStartX, requesterPosY, textMaxWidth, color.RGBA{R: 255, G: 180, B: 220, A: 255})

	// 8. Draw formatted duration
	durDisplay := "Duration: " + utils.FormatDuration(data.Duration)
	drawText(canvas, durFontFace, durDisplay, textStartX, durationPosY, textMaxWidth, color.RGBA{R: 255, G: 215, B: 235, A: 255})

	// 9. Save generated image to disk
	outPath := generateOutputPath(data.TrackID)
	outF, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("failed to create output file: %w", err)
	}
	defer outF.Close()

	if err := png.Encode(outF, canvas); err != nil {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("failed to encode output PNG: %w", err)
	}

	return outPath, nil
}

// sanitizeRequesterName strips HTML tags, control chars, and excess spaces.
func sanitizeRequesterName(raw string) string {
	if raw == "" {
		return ""
	}
	stripped := htmlTagRegex.ReplaceAllString(raw, "")
	stripped = strings.TrimSpace(stripped)
	return stripped
}

// generateOutputPath generates a collision-free temporary file path.
func generateOutputPath(trackID string) string {
	dir := getTempDir()
	var randomBytes [4]byte
	_, _ = rand.Read(randomBytes[:])
	randomStr := hex.EncodeToString(randomBytes[:])

	safeID := trackID
	if safeID == "" {
		safeID = "track"
	}
	safeID = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, safeID)
	if len(safeID) > 16 {
		safeID = safeID[:16]
	}

	filename := fmt.Sprintf("kiwi_np_%s_%d_%s.png", safeID, time.Now().UnixNano(), randomStr)
	return filepath.Join(dir, filename)
}

// fetchArtwork retrieves and decodes the song artwork with caching and size limits.
func fetchArtwork(ctx context.Context, artworkURL string) (image.Image, error) {
	cleanURL := utils.CleanURL(artworkURL)
	if cleanURL == "" {
		return nil, errors.New("empty artwork url")
	}

	// Check in-memory cache (cached for 15 minutes)
	if entry, ok := artworkCache.Load(cleanURL); ok {
		cEntry := entry.(artworkCacheEntry)
		if time.Since(cEntry.timestamp) < 15*time.Minute {
			return cEntry.img, nil
		}
		artworkCache.Delete(cleanURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cleanURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; KiwiMusicBot/1.0)")

	resp, err := artHttpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	// Protect against oversized files (max 10MB)
	limitedReader := io.LimitReader(resp.Body, 10*1024*1024)
	imgData, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, err
	}

	decoded, _, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		return nil, fmt.Errorf("failed to decode artwork image: %w", err)
	}

	artworkCache.Store(cleanURL, artworkCacheEntry{
		img:       decoded,
		timestamp: time.Now(),
	})

	return decoded, nil
}

// fetchOrGenerateProfilePhoto retrieves the user's profile photo via TDLib or creates an initials avatar.
func fetchOrGenerateProfilePhoto(
	ctx context.Context,
	c *td.Client,
	userID int64,
	userName string,
) image.Image {
	if c != nil && userID > 0 {
		if cachedPath, ok := getCachedProfilePath(userID); ok {
			if img, err := decodeImageFile(cachedPath); err == nil {
				return cropCenterAndResize(img, profileTargetSize, profileTargetSize)
			}
		}

		user, err := c.GetUser(userID)
		if err == nil && user != nil && user.ProfilePhoto != nil {
			var fileToDownload *td.File
			if user.ProfilePhoto.Small != nil {
				fileToDownload = user.ProfilePhoto.Small
			} else if user.ProfilePhoto.Big != nil {
				fileToDownload = user.ProfilePhoto.Big
			}

			if fileToDownload != nil {
				localPath := ""
				if fileToDownload.Local != nil && fileToDownload.Local.IsDownloadingCompleted && fileToDownload.Local.Path != "" {
					localPath = fileToDownload.Local.Path
				} else {
					downloaded, dlErr := c.DownloadFile(fileToDownload.Id, 0, 0, 32, &td.DownloadFileOpts{Synchronous: true})
					if dlErr == nil && downloaded != nil && downloaded.Local != nil && downloaded.Local.Path != "" {
						localPath = downloaded.Local.Path
					}
				}

				if localPath != "" {
					if img, err := decodeImageFile(localPath); err == nil {
						setCachedProfilePath(userID, localPath)
						return cropCenterAndResize(img, profileTargetSize, profileTargetSize)
					}
				}
			}
		}
	}

	// Safe fallback avatar when user has no photo or retrieval fails
	initial := "K"
	cleanName := strings.TrimSpace(userName)
	if cleanName != "" {
		for _, r := range cleanName {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				initial = strings.ToUpper(string(r))
				break
			}
		}
	}

	return createInitialsAvatar(profileTargetSize, initial, avatarFontFace)
}

func getCachedProfilePath(userID int64) (string, bool) {
	if entry, ok := profileCache.Load(userID); ok {
		cEntry := entry.(profileCacheEntry)
		if time.Since(cEntry.timestamp) < 10*time.Minute {
			if _, err := os.Stat(cEntry.path); err == nil {
				return cEntry.path, true
			}
		}
		profileCache.Delete(userID)
	}
	return "", false
}

func setCachedProfilePath(userID int64, path string) {
	profileCache.Store(userID, profileCacheEntry{
		path:      path,
		timestamp: time.Now(),
	})
}

func decodeImageFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	return img, err
}

// cropCenterAndResize crops an image to a 1:1 square centered, then scales to target dimensions using CatmullRom.
func cropCenterAndResize(src image.Image, targetW, targetH int) image.Image {
	b := src.Bounds()
	srcW := b.Dx()
	srcH := b.Dy()

	minDim := srcW
	if srcH < minDim {
		minDim = srcH
	}

	cropX := b.Min.X + (srcW-minDim)/2
	cropY := b.Min.Y + (srcH-minDim)/2
	croppedRect := image.Rect(cropX, cropY, cropX+minDim, cropY+minDim)

	cropped := image.NewRGBA(image.Rect(0, 0, minDim, minDim))
	draw.Draw(cropped, cropped.Bounds(), src, croppedRect.Min, draw.Src)

	resized := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	xdraw.CatmullRom.Scale(resized, resized.Bounds(), cropped, cropped.Bounds(), draw.Src, nil)
	return resized
}

// createInitialsAvatar creates a stylized gradient circular avatar with a centered letter.
func createInitialsAvatar(size int, initial string, face font.Face) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			factor := float64(x+y) / float64(size*2)
			r := uint8(220 + factor*30)
			g := uint8(20 + factor*40)
			b := uint8(100 + factor*80)
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}

	if face != nil && initial != "" {
		d := &font.Drawer{
			Dst:  img,
			Src:  image.NewUniform(color.White),
			Face: face,
		}
		bounds, _ := d.BoundString(initial)
		strW := (bounds.Max.X - bounds.Min.X).Ceil()
		strH := (bounds.Max.Y - bounds.Min.Y).Ceil()
		x := (size - strW) / 2
		y := (size+strH)/2 - 4
		d.Dot = fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)}
		d.DrawString(initial)
	}
	return img
}

// applyRoundedCorners masks pixels outside a rounded rectangle with smooth antialiasing.
func applyRoundedCorners(src image.Image, r int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	rf := float64(r)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := 0.0, 0.0
			isCorner := false
			if x < r && y < r {
				dx, dy = rf-float64(x), rf-float64(y)
				isCorner = true
			} else if x >= w-r && y < r {
				dx, dy = float64(x-(w-r)+1), rf-float64(y)
				isCorner = true
			} else if x < r && y >= h-r {
				dx, dy = rf-float64(x), float64(y-(h-r)+1)
				isCorner = true
			} else if x >= w-r && y >= h-r {
				dx, dy = float64(x-(w-r)+1), float64(y-(h-r)+1)
				isCorner = true
			}

			if isCorner {
				dist := math.Hypot(dx, dy)
				if dist > rf+0.5 {
					continue
				}
				alpha := 1.0
				if dist > rf-0.5 {
					alpha = rf + 0.5 - dist
				}
				origColor := src.At(b.Min.X+x, b.Min.Y+y)
				dst.Set(x, y, blendAlpha(origColor, alpha))
			} else {
				dst.Set(x, y, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
	}
	return dst
}

// applyCircularMask creates a circle with transparent borders outside the radius.
func applyCircularMask(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	size := w
	if h < size {
		size = h
	}
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	radius := float64(size) / 2.0
	cx, cy := radius, radius

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) + 0.5 - cx
			dy := float64(y) + 0.5 - cy
			dist := math.Hypot(dx, dy)

			if dist <= radius-0.5 {
				dst.Set(x, y, src.At(b.Min.X+x, b.Min.Y+y))
			} else if dist <= radius+0.5 {
				alpha := radius + 0.5 - dist
				origColor := src.At(b.Min.X+x, b.Min.Y+y)
				dst.Set(x, y, blendAlpha(origColor, alpha))
			}
		}
	}
	return dst
}

func blendAlpha(c color.Color, a float64) color.Color {
	r, g, b, origA := c.RGBA()
	scale := a * (float64(origA) / 65535.0)
	return color.RGBA{
		R: uint8((float64(r) / 65535.0) * scale * 255),
		G: uint8((float64(g) / 65535.0) * scale * 255),
		B: uint8((float64(b) / 65535.0) * scale * 255),
		A: uint8(scale * 255),
	}
}

// drawText renders text truncated with ellipsis if exceeding maxWidth.
func drawText(dst draw.Image, face font.Face, text string, x, y, maxWidth int, col color.Color) {
	if face == nil || text == "" {
		return
	}

	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(col),
		Face: face,
		Dot:  fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)},
	}

	for len(text) > 0 && d.MeasureString(text).Ceil() > maxWidth {
		text = strings.TrimSpace(text[:len(text)-1])
		if len(text) > 3 {
			text = text[:len(text)-3] + "..."
		}
	}

	d.DrawString(text)
}

// StartNowPlayingPreparation begins background generation of the custom image concurrently
// while audio is being searched and downloaded.
func StartNowPlayingPreparation(
	parentCtx context.Context,
	c *td.Client,
	chatID int64,
	track *state.Track,
	sender *td.User,
) *NowPlayingTask {
	if track == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(parentCtx, 25*time.Second)

	requesterName := track.RequesterName
	requesterID := track.RequesterID
	if sender != nil {
		requesterID = sender.Id
		if requesterName == "" {
			requesterName = strings.TrimSpace(sender.FirstName + " " + sender.LastName)
		}
	}

	data := NowPlayingData{
		SongTitle:     track.Title,
		RequesterName: requesterName,
		RequesterID:   requesterID,
		ArtworkURL:    track.Artwork,
		Duration:      track.Duration,
		TrackID:       track.ID,
	}

	taskKey := fmt.Sprintf("%d:%s", chatID, track.ID)
	task := &NowPlayingTask{
		TrackID: track.ID,
		ChatID:  chatID,
		Done:    make(chan struct{}),
		cancel:  cancel,
	}

	activeTasksMu.Lock()
	activeTasks[taskKey] = task
	activeTasksMu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				task.Err = fmt.Errorf("panic during image generation: %v", r)
				logger.Errorf("[NowPlaying] Panic during image rendering: %v", r)
			}
			close(task.Done)
			cancel()

			// Leave task in registry briefly for Wait() callers, then clean up
			time.AfterFunc(1*time.Minute, func() {
				activeTasksMu.Lock()
				delete(activeTasks, taskKey)
				activeTasksMu.Unlock()
			})
		}()

		path, err := GenerateNowPlayingImage(ctx, c, data)
		if err != nil {
			task.Err = err
			logger.Debugf("[NowPlaying] Image preparation failed for track %s: %v", track.ID, err)
			return
		}
		task.ImagePath = path
		logger.Debugf("[NowPlaying] Background image ready at %s for track %s", path, track.ID)
	}()

	return task
}

// GetActiveNowPlayingTask retrieves an existing preparation task for a chat and track.
func GetActiveNowPlayingTask(chatID int64, trackID string) *NowPlayingTask {
	taskKey := fmt.Sprintf("%d:%s", chatID, trackID)
	activeTasksMu.Lock()
	defer activeTasksMu.Unlock()
	return activeTasks[taskKey]
}

// Wait waits for the task to finish or returns an error if timeout elapses.
func (t *NowPlayingTask) Wait(timeout time.Duration) (string, error) {
	if t == nil {
		return "", errors.New("nil task")
	}

	select {
	case <-t.Done:
		if t.Err != nil {
			return "", t.Err
		}
		return t.ImagePath, nil
	case <-time.After(timeout):
		return "", errors.New("timeout waiting for image rendering")
	}
}
