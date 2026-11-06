package platforms

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	state "KanhaMusic/kanha/core/models"
)

// processOneGrabSpotify downloads and decrypts OneGrab Spotify audio.
// It returns a local OGG file path ready for playback.
func processOneGrabSpotify(
	ctx context.Context,
	cdnURL string,
	hexKey string,
	track *state.Track,
) (string, error) {
	if track == nil {
		return "", fmt.Errorf("nil track")
	}
	if strings.TrimSpace(cdnURL) == "" {
		return "", fmt.Errorf("missing OneGrab CDN URL")
	}
	if strings.TrimSpace(hexKey) == "" {
		return "", fmt.Errorf("missing OneGrab Spotify decryption key")
	}

	outputPath := getPath(track, "ogg")

	if fileExists(outputPath) {
		return outputPath, nil
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return "", fmt.Errorf("create download directory: %w", err)
	}

	key, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil {
		return "", fmt.Errorf("decode Spotify AES key: %w", err)
	}

	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return "", fmt.Errorf("invalid Spotify AES key length: %d bytes", len(key))
	}

	iv := oneGrabSpotifyIV

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(
		reqCtx,
		http.MethodGet,
		cdnURL,
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("create CDN request: %w", err)
	}

	client := &http.Client{
		Timeout: 90 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download encrypted Spotify audio: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OneGrab CDN returned HTTP %d", resp.StatusCode)
	}

	encrypted, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read encrypted audio: %w", err)
	}

	if len(encrypted) == 0 {
		return "", fmt.Errorf("OneGrab returned an empty audio file")
	}

	decrypted := make([]byte, len(encrypted))
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(decrypted, encrypted)

	tempFile, err := os.CreateTemp(
		filepath.Dir(outputPath),
		"onegrab-spotify-*.ogg",
	)
	if err != nil {
		return "", fmt.Errorf("create temporary audio file: %w", err)
	}

	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if _, err := tempFile.Write(decrypted); err != nil {
		tempFile.Close()
		return "", fmt.Errorf("write decrypted audio: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		return "", fmt.Errorf("close decrypted audio: %w", err)
	}

	if err := rebuildOneGrabOGG(tempPath); err != nil {
		return "", err
	}

	if err := fixOneGrabOGG(reqCtx, tempPath, outputPath); err != nil {
		return "", err
	}

	if !fileExists(outputPath) {
		return "", fmt.Errorf("Spotify output file was not created")
	}

	return outputPath, nil
}

// fixOneGrabOGG remuxes the reconstructed OGG using FFmpeg.
func fixOneGrabOGG(
	ctx context.Context,
	inputPath string,
	outputPath string,
) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("FFmpeg is not installed or not in PATH: %w", err)
	}

	ffmpegCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	tempOutput := outputPath + ".tmp.ogg"
	defer os.Remove(tempOutput)

	cmd := exec.CommandContext(
		ffmpegCtx,
		"ffmpeg",
		"-y",
		"-i", inputPath,
		"-c", "copy",
		tempOutput,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"FFmpeg Spotify processing failed: %w: %s",
			err,
			string(output),
		)
	}

	if !fileExists(tempOutput) {
		return fmt.Errorf("FFmpeg did not create the output file")
	}

	if err := os.Rename(tempOutput, outputPath); err != nil {
		return fmt.Errorf("move processed Spotify file: %w", err)
	}

	return nil
}
