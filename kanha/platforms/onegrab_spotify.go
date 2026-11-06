
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

const oneGrabSpotifyIV = "72e067fbddcbcf77ebe8bc643f630d93"

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

	iv, err := hex.DecodeString(oneGrabSpotifyIV)
	if err != nil {
		return "", fmt.Errorf("decode Spotify AES IV: %w", err)
	}

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

// rebuildOneGrabOGG restores the OGG/Vorbis header bytes.
func rebuildOneGrabOGG(filename string) error {
	file, err := os.OpenFile(filename, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open decrypted OGG: %w", err)
	}
	defer file.Close()

	patches := []struct {
		offset int64
		data   []byte
	}{
		{0, []byte("OggS")},
		{6, []byte("\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")},
		{26, []byte("\x01\x1E\x01vorbis")},
		{39, []byte("\x02")},
		{40, []byte("\x44\xAC\x00\x00")},
		{48, []byte("\x00\xE2\x04\x00")},
		{56, []byte("\xB8\x01")},
		{58, []byte("OggS")},
		{62, []byte("\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")},
	}

	for _, patch := range patches {
		if _, err := file.WriteAt(patch.data, patch.offset); err != nil {
			return fmt.Errorf(
				"rebuild OGG header at offset %d: %w",
				patch.offset,
				err,
			)
		}
	}

	return file.Sync()
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
