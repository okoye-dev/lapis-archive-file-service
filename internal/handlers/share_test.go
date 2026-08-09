package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
)

type fakeStorage struct {
	objects map[string][]byte
	sizes   map[string]int64
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{
		objects: make(map[string][]byte),
		sizes:   make(map[string]int64),
	}
}

func (f *fakeStorage) seed(key string, data []byte) {
	f.objects[key] = data
	f.sizes[key] = int64(len(data))
}

func (f *fakeStorage) DeleteFile(_ context.Context, key string) error {
	delete(f.objects, key)
	delete(f.sizes, key)
	return nil
}

func (f *fakeStorage) ListFiles(_ context.Context) ([]string, error) {
	var keys []string
	for k := range f.objects {
		keys = append(keys, k)
	}
	return keys, nil
}

func (f *fakeStorage) GetFileSize(_ context.Context, key string) (int64, error) {
	size, ok := f.sizes[key]
	if !ok {
		return 0, storage.ErrNotFound
	}
	return size, nil
}

func (f *fakeStorage) GetPresignedURL(_ context.Context, key string, forceDownload bool) (string, error) {
	if _, ok := f.objects[key]; !ok {
		return "", storage.ErrNotFound
	}
	return "https://bucket.example/" + key + fmt.Sprintf("?download=%t", forceDownload), nil
}

func (f *fakeStorage) GetPresignedUploadURL(_ context.Context, key string, size int64, _ string) (string, error) {
	return "https://bucket.example/upload/" + key, nil
}

func (f *fakeStorage) GetMetadata(_ context.Context, key string) ([]byte, error) {
	data, ok := f.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return data, nil
}

func (f *fakeStorage) PutMetadata(_ context.Context, key string, data []byte) error {
	f.objects[key] = data
	f.sizes[key] = int64(len(data))
	return nil
}

func setupRouter(store *fakeStorage) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	fileHandler := NewFileHandler(store, 10*1024*1024)
	router.GET("/files", fileHandler.GetFiles)
	router.POST("/files/presign-upload", fileHandler.PresignUpload)

	shareHandler := NewShareHandler(store)
	router.POST("/shares", shareHandler.CreateShare)
	router.GET("/shares/:slug", shareHandler.GetShare)
	router.POST("/shares/:slug/unlock", shareHandler.UnlockShare)

	return router
}

func doJSON(router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestShareLifecycle(t *testing.T) {
	store := newFakeStorage()
	store.seed("uuid1_report.pdf", []byte("data"))
	router := setupRouter(store)

	w := doJSON(router, "POST", "/shares", gin.H{"storage_key": "uuid1_report.pdf"})
	if w.Code != http.StatusOK {
		t.Fatalf("create share: %d %s", w.Code, w.Body.String())
	}
	var created CreateShareResponse
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.Slug == "" || created.Code == "" {
		t.Fatalf("missing slug/code: %+v", created)
	}
	if created.FileName != "report.pdf" {
		t.Errorf("file name = %q, want report.pdf", created.FileName)
	}

	w = doJSON(router, "GET", "/shares/"+created.Slug, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get share: %d", w.Code)
	}
	var meta ShareMetaResponse
	json.Unmarshal(w.Body.Bytes(), &meta)
	if meta.Expired {
		t.Error("fresh share reported expired")
	}
	if bytes.Contains(w.Body.Bytes(), []byte(created.Code)) {
		t.Error("share metadata leaks the access code")
	}

	w = doJSON(router, "POST", "/shares/"+created.Slug+"/unlock", gin.H{"code": "XXXXXX"})
	if w.Code != http.StatusForbidden {
		t.Errorf("wrong code: %d, want 403", w.Code)
	}

	w = doJSON(router, "POST", "/shares/"+created.Slug+"/unlock", gin.H{"code": created.Code, "download": true})
	if w.Code != http.StatusOK {
		t.Fatalf("unlock: %d %s", w.Code, w.Body.String())
	}
	var unlocked UnlockShareResponse
	json.Unmarshal(w.Body.Bytes(), &unlocked)
	if unlocked.URL == "" {
		t.Error("no presigned url returned")
	}
}

func TestShareNotFound(t *testing.T) {
	router := setupRouter(newFakeStorage())

	w := doJSON(router, "GET", "/shares/aaaaaaaaaa", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("missing share: %d, want 404", w.Code)
	}

	w = doJSON(router, "POST", "/shares", gin.H{"storage_key": "nope_file.txt"})
	if w.Code != http.StatusNotFound {
		t.Errorf("share of missing file: %d, want 404", w.Code)
	}
}

func TestShareRejectsShareKeys(t *testing.T) {
	store := newFakeStorage()
	store.seed("shares/abcdefghij.json", []byte("{}"))
	router := setupRouter(store)

	w := doJSON(router, "POST", "/shares", gin.H{"storage_key": "shares/abcdefghij.json"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("share of share metadata: %d, want 400", w.Code)
	}
}

func TestUnlockRateLimit(t *testing.T) {
	store := newFakeStorage()
	store.seed("uuid2_a.txt", []byte("data"))
	router := setupRouter(store)

	w := doJSON(router, "POST", "/shares", gin.H{"storage_key": "uuid2_a.txt"})
	var created CreateShareResponse
	json.Unmarshal(w.Body.Bytes(), &created)

	var got429 bool
	for i := 0; i < 12; i++ {
		w = doJSON(router, "POST", "/shares/"+created.Slug+"/unlock", gin.H{"code": "WRONG1"})
		if w.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Error("rate limiter never engaged after 12 bad attempts")
	}
}

func TestUnlockPerSlugLimitSurvivesIPRotation(t *testing.T) {
	store := newFakeStorage()
	store.seed("uuid4_a.txt", []byte("data"))
	router := setupRouter(store)

	w := doJSON(router, "POST", "/shares", gin.H{"storage_key": "uuid4_a.txt"})
	var created CreateShareResponse
	json.Unmarshal(w.Body.Bytes(), &created)

	var got429 bool
	for i := 0; i < 40; i++ {
		var buf bytes.Buffer
		json.NewEncoder(&buf).Encode(gin.H{"code": "WRONG1"})
		req := httptest.NewRequest("POST", "/shares/"+created.Slug+"/unlock", &buf)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.%d.%d", i/256, i%256))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Error("per-slug limit never engaged despite rotating IPs")
	}
}

func TestUnlockRejectsInvalidSlug(t *testing.T) {
	router := setupRouter(newFakeStorage())
	w := doJSON(router, "POST", "/shares/../../etc/unlock", gin.H{"code": "AAAAAA"})
	if w.Code == http.StatusOK {
		t.Errorf("invalid slug unlocked: %d", w.Code)
	}
}

func TestCreateShareRejectsOversizedEmail(t *testing.T) {
	store := newFakeStorage()
	store.seed("uuid5_a.txt", []byte("data"))
	router := setupRouter(store)

	w := doJSON(router, "POST", "/shares", gin.H{
		"storage_key":     "uuid5_a.txt",
		"recipient_email": strings.Repeat("x", 1000),
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("oversized email: %d, want 400", w.Code)
	}
}

func TestGetFilesHidesShareMetadata(t *testing.T) {
	store := newFakeStorage()
	store.seed("uuid3_visible.txt", []byte("data"))
	store.seed("shares/abcdefghij.json", []byte("{}"))
	router := setupRouter(store)

	w := doJSON(router, "GET", "/files", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("shares/")) {
		t.Errorf("share metadata leaked into file list: %s", w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("visible.txt")) {
		t.Errorf("real file missing from list: %s", w.Body.String())
	}
}

func TestPresignUpload(t *testing.T) {
	router := setupRouter(newFakeStorage())

	w := doJSON(router, "POST", "/files/presign-upload", gin.H{"name": "../../evil.sh", "size": 100})
	if w.Code != http.StatusOK {
		t.Fatalf("presign: %d %s", w.Code, w.Body.String())
	}
	var resp PresignUploadResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Name != "evil.sh" {
		t.Errorf("name = %q, want sanitized evil.sh", resp.Name)
	}
	if resp.UploadURL == "" || resp.StorageKey == "" {
		t.Errorf("incomplete response: %+v", resp)
	}

	w = doJSON(router, "POST", "/files/presign-upload", gin.H{"name": "big.bin", "size": 11 * 1024 * 1024})
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize presign: %d, want 413", w.Code)
	}

	w = doJSON(router, "POST", "/files/presign-upload", gin.H{"name": "nosize.bin"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing size: %d, want 400", w.Code)
	}
}
