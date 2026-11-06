/*
 * ● Kiwi Music (KanhaMusic fork)
 * ○ Unit Tests for Now Playing Thumbnail System
 */

package modules

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	state "KanhaMusic/kanha/core/models"
)

// Helper to create a test HTTP server serving an image
func createMockArtworkServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		img := image.NewRGBA(image.Rect(0, 0, 400, 400))
		for y := 0; y < 400; y++ {
			for x := 0; x < 400; x++ {
				img.Set(x, y, color.RGBA{R: 200, G: 50, B: 120, A: 255})
			}
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_ = jpeg.Encode(w, img, nil)
	}))
}

func TestLocateAndCacheTemplate(t *testing.T) {
	path, err := locateTemplate()
	if err != nil {
		t.Fatalf("Failed to locate template: %v", err)
	}
	if !strings.Contains(path, "Neon Kiwi Music Bot Template.png") && !strings.Contains(path, "template.png") {
		t.Errorf("Unexpected template path: %s", path)
	}

	tpl, err := loadCachedTemplate()
	if err != nil {
		t.Fatalf("Failed to load cached template: %v", err)
	}

	b := tpl.Bounds()
	if b.Dx() != templateWidth || b.Dy() != templateHeight {
		t.Errorf("Expected template dimensions %dx%d, got %dx%d", templateWidth, templateHeight, b.Dx(), b.Dy())
	}
}

func TestInitFonts(t *testing.T) {
	if err := initFonts(); err != nil {
		t.Fatalf("Failed to init fonts: %v", err)
	}
	if titleFontFace == nil || reqFontFace == nil || durFontFace == nil || avatarFontFace == nil {
		t.Fatal("One or more font faces is nil after initFonts")
	}
}

func TestSanitizeRequesterName(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"<a href=\"tg://user?id=123\">Sangam</a>", "Sangam"},
		{"<b>Admin</b>", "Admin"},
		{"Normal Name", "Normal Name"},
		{"  Spaces  ", "Spaces"},
		{"", ""},
	}

	for _, c := range cases {
		got := sanitizeRequesterName(c.input)
		if got != c.expected {
			t.Errorf("sanitizeRequesterName(%q) = %q, expected %q", c.input, got, c.expected)
		}
	}
}

func TestGenerateNowPlayingImageNormal(t *testing.T) {
	srv := createMockArtworkServer(t)
	defer srv.Close()

	data := NowPlayingData{
		SongTitle:     "Kiwi Anthem (Official Audio)",
		RequesterName: "Sangam",
		RequesterID:   123456,
		ArtworkURL:    srv.URL + "/thumb.jpg",
		Duration:      215,
		TrackID:       "track_test_1",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	outPath, err := GenerateNowPlayingImage(ctx, nil, data)
	if err != nil {
		t.Fatalf("GenerateNowPlayingImage failed: %v", err)
	}
	defer os.Remove(outPath)

	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("Output file does not exist: %v", err)
	}
	if info.Size() < 50000 {
		t.Errorf("Output file unexpectedly small: %d bytes", info.Size())
	}
}

func TestGenerateNowPlayingImageLongStrings(t *testing.T) {
	srv := createMockArtworkServer(t)
	defer srv.Close()

	data := NowPlayingData{
		SongTitle:     "This is an extremely long song title designed to test proper truncation and ensure there are absolutely no visual overflows or errors when rendering",
		RequesterName: "ExtremelyLongTelegramDisplayNameThatExceedsNormalLengthsAndShouldBeSafelyTruncated",
		RequesterID:   0, // tests avatar generation fallback
		ArtworkURL:    srv.URL + "/art.jpg",
		Duration:      7345,
		TrackID:       "track_long_test",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	outPath, err := GenerateNowPlayingImage(ctx, nil, data)
	if err != nil {
		t.Fatalf("GenerateNowPlayingImage with long strings failed: %v", err)
	}
	defer os.Remove(outPath)

	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("Output file was not created: %v", err)
	}
}

func TestInvalidArtworkFallback(t *testing.T) {
	data := NowPlayingData{
		SongTitle:     "Invalid Track",
		RequesterName: "User",
		ArtworkURL:    "http://127.0.0.1:54321/nonexistent.jpg",
		Duration:      180,
		TrackID:       "track_invalid_art",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := GenerateNowPlayingImage(ctx, nil, data)
	if err == nil {
		t.Fatal("Expected error for invalid artwork URL, got nil")
	}
}

func TestConcurrentRenderingSafety(t *testing.T) {
	srv := createMockArtworkServer(t)
	defer srv.Close()

	const workers = 5
	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			data := NowPlayingData{
				SongTitle:     fmt.Sprintf("Concurrent Song %d", id),
				RequesterName: fmt.Sprintf("User %d", id),
				RequesterID:   int64(1000 + id),
				ArtworkURL:    srv.URL + fmt.Sprintf("/thumb%d.jpg", id),
				Duration:      120 + id*10,
				TrackID:       fmt.Sprintf("concurrent_%d", id),
			}

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()

			path, err := GenerateNowPlayingImage(ctx, nil, data)
			if err != nil {
				errCh <- fmt.Errorf("worker %d failed: %w", id, err)
				return
			}
			defer os.Remove(path)

			if _, err := os.Stat(path); err != nil {
				errCh <- fmt.Errorf("worker %d file missing: %w", id, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("Concurrent rendering error: %v", err)
	}
}

func TestNowPlayingTaskWait(t *testing.T) {
	track := &state.Track{
		ID:        "task_test_track",
		Title:     "Task Song",
		Duration:  200,
		Artwork:   "http://invalid.local/test.jpg",
		Requester: "Tester",
	}

	task := StartNowPlayingPreparation(context.Background(), nil, 999888, track, nil)
	if task == nil {
		t.Fatal("Expected non-nil NowPlayingTask")
	}

	// Should finish (with expected error due to invalid URL)
	path, err := task.Wait(3 * time.Second)
	if err == nil && path == "" {
		t.Error("Expected either error or valid path")
	}
}

func TestApplyRoundedCornersAndCircleMask(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(src, src.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	rounded := applyRoundedCorners(src, 10)
	if rounded == nil || rounded.Bounds().Dx() != 100 {
		t.Fatal("applyRoundedCorners returned invalid image")
	}

	circle := applyCircularMask(src)
	if circle == nil || circle.Bounds().Dx() != 100 {
		t.Fatal("applyCircularMask returned invalid image")
	}

	// Outside corner should have zero alpha
	_, _, _, a := circle.At(0, 0).RGBA()
	if a != 0 {
		t.Errorf("Expected circle corner alpha 0, got %d", a)
	}
}
