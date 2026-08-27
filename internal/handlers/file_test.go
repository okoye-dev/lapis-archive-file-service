package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupFileRouter(files *fakeStorage) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewFileHandler(files, nil, 10*1024*1024)
	r.GET("/files/:id", h.GetFile)
	return r
}

func TestGetFile(t *testing.T) {
	files := newFakeStorage()
	files.seed("uuid_report.pdf", 10)
	r := setupFileRouter(files)

	w := doJSON(r, "GET", "/files/uuid_report.pdf", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	var resp FileDownloadResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Download {
		t.Error("default view should be inline (Download=false)")
	}

	w = doJSON(r, "GET", "/files/uuid_report.pdf?download=true", nil)
	json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != http.StatusOK || !resp.Download {
		t.Errorf("download=true: code=%d Download=%v", w.Code, resp.Download)
	}

	// The fake errors when presigning an unseeded key, so the handler 404s.
	w = doJSON(r, "GET", "/files/uuid_missing.pdf", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("missing key: %d, want 404", w.Code)
	}
}

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
