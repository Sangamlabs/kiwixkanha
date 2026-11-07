package platforms

import (
	"reflect"
	"testing"
)

func TestFilterArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		flag     string
		val      string
		expected []string
	}{
		{
			name:     "drop separate flag and value",
			args:     []string{"--no-part", "--cookies", "/path/to/cookie.txt", "-q"},
			flag:     "--cookies",
			val:      "/path/to/cookie.txt",
			expected: []string{"--no-part", "-q"},
		},
		{
			name:     "drop combined flag=value",
			args:     []string{"--no-part", "--cookies=/path/to/cookie.txt", "-q"},
			flag:     "--cookies",
			val:      "/path/to/cookie.txt",
			expected: []string{"--no-part", "-q"},
		},
		{
			name:     "flag not present",
			args:     []string{"--no-part", "-q"},
			flag:     "--cookies",
			val:      "/path/to/cookie.txt",
			expected: []string{"--no-part", "-q"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterArgs(tt.args, tt.flag, tt.val)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("filterArgs() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestIsYouTubeURL(t *testing.T) {
	y := &YtdlpPlatform{}

	tests := []struct {
		url      string
		expected bool
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", true},
		{"https://youtu.be/dQw4w9WgXcQ", true},
		{"https://music.youtube.com/watch?v=dQw4w9WgXcQ", true},
		{"http://youtube.com/watch?v=12345", true},
		{"ytsearch:arijit singh songs", true},
		{"ytsearch15:lofi hip hop", true},
		{"https://soundcloud.com/artist/track", false},
		{"https://open.spotify.com/track/123", false},
		{"https://example.com/audio.mp3", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := y.isYouTubeURL(tt.url)
			if got != tt.expected {
				t.Errorf("isYouTubeURL(%q) = %v, expected %v", tt.url, got, tt.expected)
			}
		})
	}
}

func TestParseMetadataOutput(t *testing.T) {
	y := &YtdlpPlatform{}

	// Single JSON entry
	singleJSON := `{"id":"dQw4w9WgXcQ","title":"Never Gonna Give You Up","duration":213,"webpage_url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ","thumbnail":"https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg"}`
	info, err := y.parseMetadataOutput([]byte(singleJSON))
	if err != nil {
		t.Fatalf("unexpected error parsing single JSON: %v", err)
	}
	if info.ID != "dQw4w9WgXcQ" || info.Title != "Never Gonna Give You Up" || info.Duration != 213 {
		t.Errorf("parsed info mismatch: %+v", info)
	}

	// Multiple JSON lines (e.g. search / playlist entries)
	multiJSON := `{"id":"entry1","title":"Song One","duration":180}
{"id":"entry2","title":"Song Two","duration":200}`
	multiInfo, err := y.parseMetadataOutput([]byte(multiJSON))
	if err != nil {
		t.Fatalf("unexpected error parsing multi JSON lines: %v", err)
	}
	if len(multiInfo.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(multiInfo.Entries))
	}
	if multiInfo.Entries[0].ID != "entry1" || multiInfo.Entries[1].ID != "entry2" {
		t.Errorf("entry mismatch: %+v", multiInfo.Entries)
	}
}

func TestSanitizeYtDlpOutput(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "ERROR: [youtube] dQw4w9WgXcQ: The page needs to be reloaded.",
			expected: "ERROR: [youtube] dQw4w9WgXcQ: The page needs to be reloaded.",
		},
		{
			input:    "Failed with cookie=SIDCC=AKEyXzW1234567890abcdef12345 in header",
			expected: "Failed with cookie=***REDACTED*** in header",
		},
		{
			input:    "Sensitive token: ABC1234567890defghijk_secret session: 9876543210fedcba_xyz123",
			expected: "Sensitive token=***REDACTED*** session=***REDACTED***",
		},
		{
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		got := sanitizeYtDlpOutput(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeYtDlpOutput(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}
