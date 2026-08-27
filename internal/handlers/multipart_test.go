package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
)

type fakeMultipart struct {
	created        map[string]string // key -> uploadID
	parts          map[string][]storage.Part
	completedOrder []int32
	aborted        []string
}

func newFakeMultipart() *fakeMultipart {
	return &fakeMultipart{created: map[string]string{}, parts: map[string][]storage.Part{}}
}

func (f *fakeMultipart) CreateMultipartUpload(_ context.Context, key, _ string) (string, error) {
	f.created[key] = "upload-" + key
	return f.created[key], nil
}

func (f *fakeMultipart) PresignUploadPart(_ context.Context, key, uploadID string, partNumber int32) (string, error) {
	if f.created[key] != uploadID {
		return "", storage.ErrNoSuchUpload
	}
	return "https://bucket.test/" + key + "?partNumber=" + string(rune('0'+partNumber)), nil
}

func (f *fakeMultipart) ListParts(_ context.Context, key, uploadID string) ([]storage.Part, error) {
	if f.created[key] != uploadID {
		return nil, storage.ErrNoSuchUpload
	}
	return f.parts[key], nil
}

func (f *fakeMultipart) CompleteMultipartUpload(_ context.Context, key, uploadID string, parts []storage.Part) error {
	if f.created[key] != uploadID {
		return storage.ErrNoSuchUpload
	}
	f.completedOrder = nil
	for _, p := range parts {
		f.completedOrder = append(f.completedOrder, p.PartNumber)
	}
	return nil
}

func (f *fakeMultipart) AbortMultipartUpload(_ context.Context, key, uploadID string) error {
	if f.created[key] != uploadID {
		return storage.ErrNoSuchUpload
	}
	f.aborted = append(f.aborted, key)
	delete(f.created, key)
	return nil
}

func setupMultipartRouter(mp *fakeMultipart) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := NewMultipartHandler(mp, nil, 100*1024*1024)
	router.POST("/uploads/multipart/init", h.Init)
	router.POST("/uploads/multipart/part", h.PresignPart)
	router.POST("/uploads/multipart/status", h.Status)
	router.POST("/uploads/multipart/complete", h.Complete)
	router.POST("/uploads/multipart/abort", h.Abort)
	return router
}

func TestMultipartLifecycle(t *testing.T) {
	mp := newFakeMultipart()
	router := setupMultipartRouter(mp)

	w := doJSON(router, "POST", "/uploads/multipart/init", gin.H{
		"name": "../weird\\video.mp4", "size": 20 * 1024 * 1024,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("init: %d %s", w.Code, w.Body.String())
	}
	var init MultipartInitResponse
	json.Unmarshal(w.Body.Bytes(), &init)

	if init.PartSize != PartSize || init.PartCount != 3 {
		t.Errorf("20MiB at 8MiB parts: size=%d count=%d, want %d and 3", init.PartSize, init.PartCount, PartSize)
	}
	if strings.Contains(init.StorageKey, "/") || !strings.HasSuffix(init.StorageKey, "_video.mp4") {
		t.Errorf("storage key not sanitized: %q", init.StorageKey)
	}

	w = doJSON(router, "POST", "/uploads/multipart/part", gin.H{
		"storage_key": init.StorageKey, "upload_id": init.UploadID, "part_number": 2,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("part: %d %s", w.Code, w.Body.String())
	}

	// Client reports parts out of order; complete must sort them.
	w = doJSON(router, "POST", "/uploads/multipart/complete", gin.H{
		"storage_key": init.StorageKey, "upload_id": init.UploadID,
		"parts": []gin.H{
			{"part_number": 3, "etag": "c"},
			{"part_number": 1, "etag": "a"},
			{"part_number": 2, "etag": "b"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", w.Code, w.Body.String())
	}
	if len(mp.completedOrder) != 3 || mp.completedOrder[0] != 1 || mp.completedOrder[2] != 3 {
		t.Errorf("parts not sorted ascending: %v", mp.completedOrder)
	}
}

func TestMultipartCompleteRejectsOversized(t *testing.T) {
	mp := newFakeMultipart()
	router := setupMultipartRouter(mp) // 100MB cap in setupMultipartRouter

	w := doJSON(router, "POST", "/uploads/multipart/init", gin.H{
		"name": "sneaky.bin", "size": 1024, // declares tiny...
	})
	var init MultipartInitResponse
	json.Unmarshal(w.Body.Bytes(), &init)

	// ...but the bucket actually holds parts far over the cap. Complete must
	// reject based on real part sizes, not the declared size.
	mp.parts[init.StorageKey] = []storage.Part{
		{PartNumber: 1, ETag: "a", Size: 150 * 1024 * 1024},
	}

	w = doJSON(router, "POST", "/uploads/multipart/complete", gin.H{
		"storage_key": init.StorageKey, "upload_id": init.UploadID,
		"parts": []gin.H{{"part_number": 1, "etag": "a"}},
	})
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized complete: %d, want 413", w.Code)
	}
	if len(mp.aborted) != 1 {
		t.Errorf("oversized upload not aborted: %v", mp.aborted)
	}
}

func TestMultipartInitRejectsOversized(t *testing.T) {
	router := setupMultipartRouter(newFakeMultipart())
	w := doJSON(router, "POST", "/uploads/multipart/init", gin.H{
		"name": "big.bin", "size": 200 * 1024 * 1024,
	})
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized init: %d, want 413", w.Code)
	}
}

func TestMultipartVanishedSessionIs404(t *testing.T) {
	mp := newFakeMultipart()
	router := setupMultipartRouter(mp)

	w := doJSON(router, "POST", "/uploads/multipart/status", gin.H{
		"storage_key": "uuid_gone.bin", "upload_id": "expired-session",
	})
	if w.Code != http.StatusNotFound {
		t.Errorf("vanished session: %d, want 404", w.Code)
	}

	// Abort of a vanished session succeeds: the goal state is already true.
	w = doJSON(router, "POST", "/uploads/multipart/abort", gin.H{
		"storage_key": "uuid_gone.bin", "upload_id": "expired-session",
	})
	if w.Code != http.StatusOK {
		t.Errorf("abort vanished: %d, want 200", w.Code)
	}
}

func TestValidKey(t *testing.T) {
	valid := []string{"uuid_photo.jpg", "abc_" + strings.Repeat("x", 180)}
	for _, k := range valid {
		if !validKey(k) {
			t.Errorf("validKey(%q) = false, want true", k)
		}
	}
	bad := []string{"", "a/b", `a\b`, "a\x00b", "a\x7fb", strings.Repeat("a", maxKeyLength+1)}
	for _, k := range bad {
		if validKey(k) {
			t.Errorf("validKey(%q) = true, want false", k)
		}
	}
}

func TestMultipartRejectsBadKey(t *testing.T) {
	router := setupMultipartRouter(newFakeMultipart())
	for _, path := range []string{"status", "part", "complete"} {
		w := doJSON(router, "POST", "/uploads/multipart/"+path, gin.H{
			"storage_key": `uuid\evil.bin`,
			"upload_id":   "x",
			"part_number": 1,
			"parts":       []gin.H{{"part_number": 1, "etag": "a"}},
		})
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s with backslash key: %d, want 400", path, w.Code)
		}
	}
}

func TestMultipartPartNumberBound(t *testing.T) {
	router := setupMultipartRouter(newFakeMultipart())
	w := doJSON(router, "POST", "/uploads/multipart/part", gin.H{
		"storage_key": "uuid_x.bin", "upload_id": "x", "part_number": maxParts + 1,
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("part_number over max: %d, want 400", w.Code)
	}
}
