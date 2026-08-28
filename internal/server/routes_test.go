package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/okoye-dev/lapis-archive-file-service/internal/config"
)

func init() { gin.SetMode(gin.TestMode) }

// bindingHandler reads the JSON body, so an over-cap body trips MaxBytesReader
// and surfaces as a bind error (400, not 413).
func bindingHandler(c *gin.Context) {
	var body struct {
		Data string `json:"data"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func postSized(t *testing.T, r http.Handler, path string, n int) int {
	t.Helper()
	body := `{"data":"` + strings.Repeat("a", n) + `"}`
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestLimitBodyCaps(t *testing.T) {
	const small = 64 * 1024
	const large = 2 * 1024 * 1024

	r := gin.New()
	r.POST("/small", limitBody(small), bindingHandler)
	r.POST("/large", limitBody(large), bindingHandler)

	cases := []struct {
		name string
		path string
		size int
		want int
	}{
		{"small under cap", "/small", 1024, http.StatusOK},
		{"small over cap", "/small", small + 1024, http.StatusBadRequest},
		// Over the small cap but under the large cap: proves /complete's override.
		{"large under cap", "/large", small + 1024, http.StatusOK},
		{"large over cap", "/large", large + 1024, http.StatusBadRequest},
	}
	for _, tc := range cases {
		if code := postSized(t, r, tc.path, tc.size); code != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, code, tc.want)
		}
	}
}

func TestCORSFailsClosed(t *testing.T) {
	cases := []struct {
		name     string
		origins  []string
		origin   string
		wantACAO string
	}{
		{"unset blocks cross-origin", nil, "https://evil.test", ""},
		{"wildcard allows any", []string{"*"}, "https://any.test", "*"},
		{"explicit allows a match", []string{"https://app.test"}, "https://app.test", "https://app.test"},
		{"explicit blocks others", []string{"https://app.test"}, "https://evil.test", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			SetupRoutes(r, Deps{Config: &config.ServerConfig{AllowedOrigins: tc.origins, MaxUploadMB: 1}})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
			req.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if got := w.Header().Get("Access-Control-Allow-Origin"); got != tc.wantACAO {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tc.wantACAO)
			}
		})
	}
}
