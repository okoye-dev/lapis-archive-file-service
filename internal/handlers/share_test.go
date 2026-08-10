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
	"github.com/okoye-dev/lapis-archive-file-service/internal/auth"
	"github.com/okoye-dev/lapis-archive-file-service/internal/shares"
)

// fakeStorage implements storage.Storage; it only tracks object sizes since
// file bytes never flow through the service.
type fakeStorage struct {
	sizes map[string]int64
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{sizes: make(map[string]int64)}
}

func (f *fakeStorage) seed(key string, size int64) { f.sizes[key] = size }

func (f *fakeStorage) DeleteFile(_ context.Context, key string) error {
	delete(f.sizes, key)
	return nil
}

func (f *fakeStorage) GetFileSize(_ context.Context, key string) (int64, error) {
	size, ok := f.sizes[key]
	if !ok {
		return 0, fmt.Errorf("not found: %s", key)
	}
	return size, nil
}

func (f *fakeStorage) GetPresignedURL(_ context.Context, key string, forceDownload bool) (string, error) {
	if _, ok := f.sizes[key]; !ok {
		return "", fmt.Errorf("not found: %s", key)
	}
	return "https://bucket.example/" + key + fmt.Sprintf("?download=%t", forceDownload), nil
}

func (f *fakeStorage) GetPresignedUploadURL(_ context.Context, key string, _ int64, _ string) (string, error) {
	return "https://bucket.example/upload/" + key, nil
}

// memStore is an in-memory ShareStore.
type memStore struct {
	byslug map[string]*shares.Share
}

func newMemStore() *memStore { return &memStore{byslug: make(map[string]*shares.Share)} }

func (m *memStore) Create(_ context.Context, s *shares.Share) error {
	m.byslug[s.Slug] = s
	return nil
}

func (m *memStore) GetBySlug(_ context.Context, slug string) (*shares.Share, error) {
	s, ok := m.byslug[slug]
	if !ok {
		return nil, shares.ErrNotFound
	}
	return s, nil
}

func (m *memStore) ListByOwner(_ context.Context, ownerID string) ([]*shares.Share, error) {
	var out []*shares.Share
	for _, s := range m.byslug {
		if s.OwnerID == ownerID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *memStore) Delete(_ context.Context, slug, ownerID string) error {
	s, ok := m.byslug[slug]
	if !ok || s.OwnerID != ownerID {
		return shares.ErrNotFound
	}
	delete(m.byslug, slug)
	return nil
}

// setupRouter wires the handlers. If asUser is non-empty, an auth middleware
// injects that user so the authenticated routes can be exercised.
func setupRouter(files *fakeStorage, store ShareStore, asUser string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	if asUser != "" {
		router.Use(func(c *gin.Context) {
			auth.SetUser(c, &auth.User{ID: asUser, Email: asUser + "@example.com"})
			c.Next()
		})
	}

	fileHandler := NewFileHandler(files, 10*1024*1024)
	router.POST("/files/presign-upload", fileHandler.PresignUpload)

	sh := NewShareHandler(store, files)
	router.POST("/shares", sh.CreateShare)
	router.GET("/shares", sh.ListMine)
	router.GET("/shares/:slug", sh.GetShare)
	router.POST("/shares/:slug/unlock", sh.UnlockShare)
	router.DELETE("/shares/:slug", sh.RevokeShare)
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
	files := newFakeStorage()
	files.seed("uuid1_report.pdf", 54)
	router := setupRouter(files, newMemStore(), "")

	w := doJSON(router, "POST", "/shares", gin.H{"storage_key": "uuid1_report.pdf"})
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created CreateShareResponse
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.Slug == "" || created.Code == "" {
		t.Fatalf("missing slug/code: %+v", created)
	}
	if created.FileName != "report.pdf" {
		t.Errorf("file name = %q", created.FileName)
	}

	w = doJSON(router, "GET", "/shares/"+created.Slug, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d", w.Code)
	}
	if bytes.Contains(w.Body.Bytes(), []byte(created.Code)) {
		t.Error("metadata leaks the access code")
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
		t.Error("no presigned url")
	}
}

func TestShareNotFound(t *testing.T) {
	router := setupRouter(newFakeStorage(), newMemStore(), "")

	if w := doJSON(router, "GET", "/shares/aaaaaaaaaa", nil); w.Code != http.StatusNotFound {
		t.Errorf("missing share: %d, want 404", w.Code)
	}
	if w := doJSON(router, "POST", "/shares", gin.H{"storage_key": "nope_file.txt"}); w.Code != http.StatusNotFound {
		t.Errorf("share of missing file: %d, want 404", w.Code)
	}
}

func TestCreateShareRejectsOversizedEmail(t *testing.T) {
	files := newFakeStorage()
	files.seed("uuid_a.txt", 4)
	router := setupRouter(files, newMemStore(), "")

	w := doJSON(router, "POST", "/shares", gin.H{
		"storage_key":     "uuid_a.txt",
		"recipient_email": strings.Repeat("x", 1000),
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("oversized email: %d, want 400", w.Code)
	}
}

func TestUnlockRateLimited(t *testing.T) {
	files := newFakeStorage()
	files.seed("uuid_a.txt", 4)
	router := setupRouter(files, newMemStore(), "")

	w := doJSON(router, "POST", "/shares", gin.H{"storage_key": "uuid_a.txt"})
	var created CreateShareResponse
	json.Unmarshal(w.Body.Bytes(), &created)

	got429 := false
	for i := 0; i < 40; i++ {
		w = doJSON(router, "POST", "/shares/"+created.Slug+"/unlock", gin.H{"code": "WRONG1"})
		if w.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Error("rate limiter never engaged")
	}
}

func TestListMineAndRevoke(t *testing.T) {
	files := newFakeStorage()
	files.seed("uuid_a.txt", 4)
	store := newMemStore()

	// user-a creates a share
	routerA := setupRouter(files, store, "user-a")
	w := doJSON(routerA, "POST", "/shares", gin.H{"storage_key": "uuid_a.txt"})
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created CreateShareResponse
	json.Unmarshal(w.Body.Bytes(), &created)

	// user-a sees it
	w = doJSON(routerA, "GET", "/shares", nil)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(created.Slug)) {
		t.Fatalf("list mine: %d %s", w.Code, w.Body.String())
	}

	// user-b can't see or revoke it
	routerB := setupRouter(files, store, "user-b")
	w = doJSON(routerB, "GET", "/shares", nil)
	if bytes.Contains(w.Body.Bytes(), []byte(created.Slug)) {
		t.Error("user-b sees user-a's share")
	}
	if w = doJSON(routerB, "DELETE", "/shares/"+created.Slug, nil); w.Code != http.StatusNotFound {
		t.Errorf("user-b revoke: %d, want 404", w.Code)
	}

	// user-a revokes it
	if w = doJSON(routerA, "DELETE", "/shares/"+created.Slug, nil); w.Code != http.StatusOK {
		t.Errorf("user-a revoke: %d, want 200", w.Code)
	}
	w = doJSON(routerA, "GET", "/shares", nil)
	if bytes.Contains(w.Body.Bytes(), []byte(created.Slug)) {
		t.Error("share still listed after revoke")
	}
}

func TestPresignUpload(t *testing.T) {
	router := setupRouter(newFakeStorage(), newMemStore(), "")

	w := doJSON(router, "POST", "/files/presign-upload", gin.H{"name": "../../evil.sh", "size": 100})
	if w.Code != http.StatusOK {
		t.Fatalf("presign: %d %s", w.Code, w.Body.String())
	}
	var resp PresignUploadResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Name != "evil.sh" {
		t.Errorf("name = %q, want evil.sh", resp.Name)
	}

	if w := doJSON(router, "POST", "/files/presign-upload", gin.H{"name": "big.bin", "size": 11 * 1024 * 1024}); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize: %d, want 413", w.Code)
	}
	if w := doJSON(router, "POST", "/files/presign-upload", gin.H{"name": "nosize.bin"}); w.Code != http.StatusBadRequest {
		t.Errorf("missing size: %d, want 400", w.Code)
	}
}
