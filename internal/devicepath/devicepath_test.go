package devicepath

import (
	"strings"
	"testing"
)

func TestParse_Valid(t *testing.T) {
	tests := []struct {
		input    string
		wantRoot string
		wantPath string
		wantStr  string
	}{
		{"sdcard:/", "sdcard", "/", "sdcard:/"},
		{"sdcard:/models/cube.3mf", "sdcard", "/models/cube.3mf", "sdcard:/models/cube.3mf"},
		{"sdcard:/calibration-cube.3mf", "sdcard", "/calibration-cube.3mf", "sdcard:/calibration-cube.3mf"},
		// Normalization: empty segments removed.
		{"sdcard:///models//cube.3mf", "sdcard", "/models/cube.3mf", "sdcard:/models/cube.3mf"},
		// Normalization: "." segments removed.
		{"sdcard:/./models/./cube.3mf", "sdcard", "/models/cube.3mf", "sdcard:/models/cube.3mf"},
		// Root with allowed special chars.
		{"sd-card_1.0:/file.gcode", "sd-card_1.0", "/file.gcode", "sd-card_1.0:/file.gcode"},
		// Trailing slash preserved for upload directory detection.
		{"sdcard:/models/", "sdcard", "/models/", "sdcard:/models/"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dp, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.input, err)
			}
			if dp.Root != tt.wantRoot {
				t.Errorf("Root = %q, want %q", dp.Root, tt.wantRoot)
			}
			if dp.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", dp.Path, tt.wantPath)
			}
			if dp.String() != tt.wantStr {
				t.Errorf("String() = %q, want %q", dp.String(), tt.wantStr)
			}
		})
	}
}

func TestParse_Invalid(t *testing.T) {
	tests := []struct {
		input   string
		wantMsg string
	}{
		{"", "device path is required"},
		{"sdcard", "missing root separator"},
		{":/file.gcode", "root name is empty"},
		{"-invalid:/file", "must start with an ASCII letter or digit"},
		{"sd card:/file", "contains invalid character"},
		{"sdcard:file", "path must start with '/'"},
		{"sdcard:/../etc/passwd", "'..' traversal"},
		{"sdcard:/models/../secret", "'..' traversal"},
		{"sdcard:/file\x00name", "invalid control characters"},
		{"sdcard:/file\x01name", "invalid control characters"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := Parse(tt.input)
			if err == nil {
				t.Fatalf("Parse(%q) expected error, got nil", tt.input)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("Parse(%q) error = %q, want substring %q", tt.input, err.Error(), tt.wantMsg)
			}
		})
	}
}

func TestParse_MaxLength(t *testing.T) {
	// Create a path that exceeds 1024 bytes.
	long := "sdcard:/" + strings.Repeat("a", 1020)
	_, err := Parse(long)
	if err == nil {
		t.Fatal("expected error for path exceeding max length")
	}
	if !strings.Contains(err.Error(), "exceeds maximum length") {
		t.Errorf("error = %q, want 'exceeds maximum length'", err.Error())
	}
}

func TestBaseName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"sdcard:/", ""},
		{"sdcard:/cube.3mf", "cube.3mf"},
		{"sdcard:/models/bracket.3mf", "bracket.3mf"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dp, err := Parse(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if got := dp.BaseName(); got != tt.want {
				t.Errorf("BaseName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeForDisplay(t *testing.T) {
	input := "sdcard:/file\x01name\x7ftest"
	got := SanitizeForDisplay(input)
	if strings.ContainsAny(got, "\x01\x7f") {
		t.Errorf("SanitizeForDisplay still contains control chars: %q", got)
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{240640, "235.0 KiB"},
		{1887436, "1.8 MiB"},
		{13314390016, "12.4 GiB"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := FormatSize(tt.bytes)
			if got != tt.want {
				t.Errorf("FormatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}
