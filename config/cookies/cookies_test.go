package cookies

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"KanhaMusic/config"
)

func TestValidateNetscapeCookieFile(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Non-existent file
	_, err := ValidateNetscapeCookieFile(filepath.Join(tempDir, "nonexistent.txt"))
	if err == nil {
		t.Fatalf("expected error for non-existent file, got nil")
	}

	// 2. Empty file (0 bytes)
	emptyPath := filepath.Join(tempDir, "empty.txt")
	if err := os.WriteFile(emptyPath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = ValidateNetscapeCookieFile(emptyPath)
	if err == nil {
		t.Fatalf("expected error for empty file, got nil")
	}

	// 3. Invalid / Plain text file
	plainPath := filepath.Join(tempDir, "plain.txt")
	if err := os.WriteFile(plainPath, []byte("Hello world\nThis is not a cookie file"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = ValidateNetscapeCookieFile(plainPath)
	if err == nil {
		t.Fatalf("expected error for plain text file, got nil")
	}

	// 4. Valid Netscape format with future cookies
	validPath := filepath.Join(tempDir, "valid_cookies.txt")
	futureExpiry := time.Now().Add(24 * time.Hour).Unix()
	content := fmt.Sprintf("# Netscape HTTP Cookie File\n# Comments\n.youtube.com\tTRUE\t/\tTRUE\t%d\tSID\tsecret_token_1\n.google.com\tTRUE\t/\tTRUE\t%d\tHSID\tsecret_token_2\n",
		futureExpiry, futureExpiry)
	if err := os.WriteFile(validPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := ValidateNetscapeCookieFile(validPath)
	if err != nil {
		t.Fatalf("expected valid Netscape cookie file, got: %v", err)
	}
	if info.TotalCookies != 2 {
		t.Errorf("expected 2 cookies, got %d", info.TotalCookies)
	}
	if info.ExpiredCount != 0 {
		t.Errorf("expected 0 expired cookies, got %d", info.ExpiredCount)
	}

	// 5. Expired Netscape format
	expiredPath := filepath.Join(tempDir, "expired_cookies.txt")
	pastExpiry := time.Now().Add(-24 * time.Hour).Unix()
	expiredContent := fmt.Sprintf("# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t%d\tLOGIN_INFO\ttoken\n", pastExpiry)
	if err := os.WriteFile(expiredPath, []byte(expiredContent), 0644); err != nil {
		t.Fatal(err)
	}
	expInfo, err := ValidateNetscapeCookieFile(expiredPath)
	if err != nil {
		t.Fatalf("expected valid parse of expired file, got: %v", err)
	}
	if expInfo.ExpiredCount != 1 {
		t.Errorf("expected 1 expired cookie, got %d", expInfo.ExpiredCount)
	}
}

func TestGetCookiePathWithEnvOverride(t *testing.T) {
	tempDir := t.TempDir()
	cookiePath := filepath.Join(tempDir, "test_cookie.txt")

	futureExpiry := time.Now().Add(48 * time.Hour).Unix()
	content := fmt.Sprintf("# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t%d\tVISITOR_INFO1_LIVE\txyz\n", futureExpiry)
	if err := os.WriteFile(cookiePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Test override
	origEnv := config.YouTubeCookiesFile
	defer func() {
		config.YouTubeCookiesFile = origEnv
		mgr.mu.Lock()
		mgr.lastChecked = time.Time{}
		mgr.cachedResult = ""
		mgr.cachedInfo = nil
		mgr.mu.Unlock()
	}()

	config.YouTubeCookiesFile = cookiePath
	mgr.mu.Lock()
	mgr.lastChecked = time.Time{}
	mgr.cachedResult = ""
	mgr.cachedInfo = nil
	mgr.mu.Unlock()

	resolved := GetCookiePath()
	if resolved != cookiePath {
		t.Errorf("expected resolved path %s, got %s", cookiePath, resolved)
	}

	// Backward compatibility
	compat, err := GetRandomCookieFile()
	if err != nil {
		t.Fatalf("unexpected error from GetRandomCookieFile: %v", err)
	}
	if compat != cookiePath {
		t.Errorf("expected GetRandomCookieFile to return %s, got %s", cookiePath, compat)
	}
}

func TestGetCookiePathMissingFile(t *testing.T) {
	origEnv := config.YouTubeCookiesFile
	defer func() {
		config.YouTubeCookiesFile = origEnv
		mgr.mu.Lock()
		mgr.lastChecked = time.Time{}
		mgr.cachedResult = ""
		mgr.cachedInfo = nil
		mgr.mu.Unlock()
	}()

	config.YouTubeCookiesFile = "/non/existent/path/cookie.txt"
	mgr.mu.Lock()
	mgr.lastChecked = time.Time{}
	mgr.cachedResult = ""
	mgr.cachedInfo = nil
	mgr.mu.Unlock()

	// Should not panic, returns empty or fallback
	_ = GetCookiePath()
}

func TestCacheInvalidationOnModTimeChange(t *testing.T) {
	tempDir := t.TempDir()
	cookiePath := filepath.Join(tempDir, "dynamic_cookie.txt")

	futureExpiry := time.Now().Add(24 * time.Hour).Unix()
	contentV1 := fmt.Sprintf("# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t%d\tCOOKIE_A\ttoken1\n", futureExpiry)
	if err := os.WriteFile(cookiePath, []byte(contentV1), 0644); err != nil {
		t.Fatal(err)
	}

	origEnv := config.YouTubeCookiesFile
	defer func() {
		config.YouTubeCookiesFile = origEnv
		mgr.mu.Lock()
		mgr.lastChecked = time.Time{}
		mgr.cachedResult = ""
		mgr.cachedInfo = nil
		mgr.mu.Unlock()
	}()

	config.YouTubeCookiesFile = cookiePath
	mgr.mu.Lock()
	mgr.lastChecked = time.Time{}
	mgr.cachedResult = ""
	mgr.cachedInfo = nil
	mgr.mu.Unlock()

	// First resolution
	res1 := GetCookiePath()
	if res1 != cookiePath {
		t.Fatalf("expected res1 to be %s, got %s", cookiePath, res1)
	}

	mgr.mu.RLock()
	if mgr.cachedInfo == nil || mgr.cachedInfo.TotalCookies != 1 {
		t.Fatalf("expected 1 cookie in cache info, got %+v", mgr.cachedInfo)
	}
	mgr.mu.RUnlock()

	// Modify file and update mod time
	contentV2 := fmt.Sprintf("# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t%d\tCOOKIE_A\ttoken1\n.youtube.com\tTRUE\t/\tTRUE\t%d\tCOOKIE_B\ttoken2\n",
		futureExpiry, futureExpiry)
	if err := os.WriteFile(cookiePath, []byte(contentV2), 0644); err != nil {
		t.Fatal(err)
	}
	newModTime := time.Now().Add(5 * time.Second)
	_ = os.Chtimes(cookiePath, newModTime, newModTime)

	// Simulate cache TTL expiration
	mgr.mu.Lock()
	mgr.lastChecked = time.Now().Add(-cacheTTL - 1*time.Second)
	mgr.mu.Unlock()

	// Call again -> should detect mtime change and reload
	res2 := GetCookiePath()
	if res2 != cookiePath {
		t.Fatalf("expected res2 to be %s, got %s", cookiePath, res2)
	}

	mgr.mu.RLock()
	if mgr.cachedInfo == nil || mgr.cachedInfo.TotalCookies != 2 {
		t.Errorf("expected 2 cookies after mtime update, got %+v", mgr.cachedInfo)
	}
	mgr.mu.RUnlock()
}

func TestMalformedRowsSafelySkipped(t *testing.T) {
	tempDir := t.TempDir()
	cookiePath := filepath.Join(tempDir, "malformed.txt")

	futureExpiry := time.Now().Add(24 * time.Hour).Unix()
	content := fmt.Sprintf("# Netscape HTTP Cookie File\n# Some commentary\nmalformed row with only four columns\n\n\t\t\t\n.youtube.com\tTRUE\t/\tTRUE\t%d\tVALID_COOKIE\ttoken_val\nincomplete\trow\n", futureExpiry)
	if err := os.WriteFile(cookiePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	info, err := ValidateNetscapeCookieFile(cookiePath)
	if err != nil {
		t.Fatalf("expected file with 1 valid cookie to pass despite malformed rows: %v", err)
	}
	if info.TotalCookies != 1 {
		t.Errorf("expected 1 valid cookie extracted, got %d", info.TotalCookies)
	}
}

func TestDecodeAndSaveCookiesB64_ValidBase64(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "cookie.txt")

	futureExpiry := time.Now().Add(48 * time.Hour).Unix()
	content := fmt.Sprintf("# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t%d\tSID\tsecret_b64_test_val\n", futureExpiry)
	b64 := base64.StdEncoding.EncodeToString([]byte(content))

	err := DecodeAndSaveCookiesB64(b64, targetPath)
	if err != nil {
		t.Fatalf("expected nil error on valid base64 cookie data, got: %v", err)
	}

	readBytes, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed reading saved cookie file: %v", err)
	}
	if string(readBytes) != content {
		t.Errorf("saved content does not match expected content")
	}

	info, err := ValidateNetscapeCookieFile(targetPath)
	if err != nil {
		t.Fatalf("expected valid netscape cookie file, got: %v", err)
	}
	if info.TotalCookies != 1 {
		t.Errorf("expected 1 cookie, got %d", info.TotalCookies)
	}
}

func TestDecodeAndSaveCookiesB64_InvalidBase64(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "cookie.txt")

	invalidB64 := "this-is-not-valid-base64-!@#$%"
	err := DecodeAndSaveCookiesB64(invalidB64, targetPath)
	if err == nil {
		t.Fatalf("expected error on invalid base64, got nil")
	}

	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Errorf("expected target file not to exist, but stat returned: %v", err)
	}
}

func TestDecodeAndSaveCookiesB64_EmptyVariable(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "cookie.txt")

	// 1. Empty string
	if err := DecodeAndSaveCookiesB64("", targetPath); err != nil {
		t.Fatalf("expected nil on empty string, got: %v", err)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Errorf("expected target file not to exist on empty string")
	}

	// 2. Whitespace string
	if err := DecodeAndSaveCookiesB64("   \r\n\t  ", targetPath); err != nil {
		t.Fatalf("expected nil on whitespace string, got: %v", err)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Errorf("expected target file not to exist on whitespace string")
	}
}

func TestDecodeAndSaveCookiesB64_MalformedCookieData(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "cookie.txt")

	// Valid base64, but invalid cookie data (JSON payload)
	jsonPayload := `{"cookies": [{"name": "foo", "value": "bar"}]}`
	b64 := base64.StdEncoding.EncodeToString([]byte(jsonPayload))

	err := DecodeAndSaveCookiesB64(b64, targetPath)
	if err == nil {
		t.Fatalf("expected error on malformed non-Netscape cookie data, got nil")
	}

	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Errorf("expected target file not to exist when validation fails")
	}
}

func TestDecodeAndSaveCookiesB64_ExistingFilePreserved(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "cookie.txt")

	futureExpiry := time.Now().Add(48 * time.Hour).Unix()
	originalContent := fmt.Sprintf("# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t%d\tORIGINAL\toriginal_secret\n", futureExpiry)
	if err := os.WriteFile(targetPath, []byte(originalContent), 0600); err != nil {
		t.Fatal(err)
	}

	// 1. Calling with empty string should preserve existing file
	if err := DecodeAndSaveCookiesB64("", targetPath); err != nil {
		t.Fatalf("unexpected error on empty string: %v", err)
	}
	data, _ := os.ReadFile(targetPath)
	if string(data) != originalContent {
		t.Errorf("original file was modified after empty base64 string")
	}

	// 2. Calling with invalid base64 should return error and preserve file
	_ = DecodeAndSaveCookiesB64("!@#$not-valid", targetPath)
	data, _ = os.ReadFile(targetPath)
	if string(data) != originalContent {
		t.Errorf("original file was modified after invalid base64 string")
	}

	// 3. Calling with malformed cookie data should return error and preserve file
	badCookieB64 := base64.StdEncoding.EncodeToString([]byte("random text content not a cookie"))
	_ = DecodeAndSaveCookiesB64(badCookieB64, targetPath)
	data, _ = os.ReadFile(targetPath)
	if string(data) != originalContent {
		t.Errorf("original file was modified after malformed cookie data")
	}
}

func TestDecodeAndSaveCookiesB64_CustomCookiePath(t *testing.T) {
	tempDir := t.TempDir()
	customPath := filepath.Join(tempDir, "custom", "nested", "subfolder", "youtube_cookies.txt")

	futureExpiry := time.Now().Add(24 * time.Hour).Unix()
	content := fmt.Sprintf("# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t%d\tTEST\tcustom_path_secret\n", futureExpiry)
	b64 := base64.StdEncoding.EncodeToString([]byte(content))

	err := DecodeAndSaveCookiesB64(b64, customPath)
	if err != nil {
		t.Fatalf("expected success writing to custom path, got: %v", err)
	}

	info, err := ValidateNetscapeCookieFile(customPath)
	if err != nil {
		t.Fatalf("expected valid file at custom path, got: %v", err)
	}
	if info.Path != customPath {
		t.Errorf("expected path %s, got %s", customPath, info.Path)
	}
}

func TestDecodeAndSaveCookiesB64_FilePermissions(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "cookie_perms.txt")

	futureExpiry := time.Now().Add(24 * time.Hour).Unix()
	content := fmt.Sprintf("# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t%d\tPERM\tperm_test\n", futureExpiry)
	b64 := base64.StdEncoding.EncodeToString([]byte(content))

	if err := DecodeAndSaveCookiesB64(b64, targetPath); err != nil {
		t.Fatalf("failed saving cookies: %v", err)
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}

	if runtime.GOOS != "windows" {
		perm := info.Mode().Perm()
		if perm != 0600 {
			t.Errorf("expected file permissions 0600, got: %04o", perm)
		}
	}
}

func TestInitBase64Cookies_EnvVariableIntegration(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "env_cookie.txt")

	origFile := config.YouTubeCookiesFile
	origB64 := config.CookiesB64
	defer func() {
		config.YouTubeCookiesFile = origFile
		config.CookiesB64 = origB64
		ResetBase64CacheForTesting()
	}()

	ResetBase64CacheForTesting()
	config.YouTubeCookiesFile = targetPath
	config.CookiesB64 = ""

	futureExpiry := time.Now().Add(24 * time.Hour).Unix()
	content := fmt.Sprintf("# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t%d\tENV_COOKIE\tenv_val\n", futureExpiry)
	b64 := base64.StdEncoding.EncodeToString([]byte(content))

	t.Setenv("COOKIES_B64", b64)

	// GetCookiePath calls InitBase64Cookies internally
	resolved := GetCookiePath()
	if resolved != targetPath {
		t.Fatalf("expected resolved path %s, got %s", targetPath, resolved)
	}

	readBytes, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read decoded cookie file: %v", err)
	}
	if string(readBytes) != content {
		t.Errorf("content does not match expected")
	}
}

func TestDecodeBase64CookieData_Variants(t *testing.T) {
	futureExpiry := time.Now().Add(24 * time.Hour).Unix()
	content := fmt.Sprintf("# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t%d\tVAR\tval_123\n", futureExpiry)
	rawBytes := []byte(content)

	// 1. Standard
	std := base64.StdEncoding.EncodeToString(rawBytes)
	out, err := decodeBase64CookieData(std)
	if err != nil || string(out) != content {
		t.Errorf("standard encoding decode failed: %v", err)
	}

	// 2. Raw standard (no padding)
	rawStd := base64.RawStdEncoding.EncodeToString(rawBytes)
	out, err = decodeBase64CookieData(rawStd)
	if err != nil || string(out) != content {
		t.Errorf("raw standard encoding decode failed: %v", err)
	}

	// 3. URL safe
	urlEnc := base64.URLEncoding.EncodeToString(rawBytes)
	out, err = decodeBase64CookieData(urlEnc)
	if err != nil || string(out) != content {
		t.Errorf("URL encoding decode failed: %v", err)
	}

	// 4. Raw URL safe
	rawUrlEnc := base64.RawURLEncoding.EncodeToString(rawBytes)
	out, err = decodeBase64CookieData(rawUrlEnc)
	if err != nil || string(out) != content {
		t.Errorf("raw URL encoding decode failed: %v", err)
	}

	// 5. With embedded newlines and carriage returns
	withNewlines := std[:len(std)/2] + "\r\n  " + std[len(std)/2:] + "\n"
	out, err = decodeBase64CookieData(withNewlines)
	if err != nil || string(out) != content {
		t.Errorf("decode with newlines failed: %v", err)
	}
}
