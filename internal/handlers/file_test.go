package handlers

import (
	"strings"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "photo.jpg", "photo.jpg"},
		{"path traversal", "../../etc/passwd", "passwd"},
		{"windows path", `C:\Users\me\doc.pdf`, "doc.pdf"},
		{"nested path", "a/b/c/report.txt", "report.txt"},
		{"control chars", "bad\x00name\x1f.txt", "badname.txt"},
		{"empty", "", "file"},
		{"dot", ".", "file"},
		{"dotdot", "..", "file"},
		{"spaces trimmed", "  hello.png  ", "hello.png"},
		{"unicode kept", "фото-отпуск.jpg", "фото-отпуск.jpg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeFilename(tt.in); got != tt.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeFilenameLength(t *testing.T) {
	long := strings.Repeat("a", 300) + ".pdf"
	got := sanitizeFilename(long)
	if len([]rune(got)) > maxFilenameLength {
		t.Errorf("length = %d, want <= %d", len([]rune(got)), maxFilenameLength)
	}
	if !strings.HasSuffix(got, ".pdf") {
		t.Errorf("extension lost: %q", got)
	}

	longUnicode := strings.Repeat("ы", 300) + ".txt"
	got = sanitizeFilename(longUnicode)
	if len([]rune(got)) > maxFilenameLength {
		t.Errorf("unicode length = %d, want <= %d", len([]rune(got)), maxFilenameLength)
	}
}
