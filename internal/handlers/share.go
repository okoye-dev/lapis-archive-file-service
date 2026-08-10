package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/okoye-dev/lapis-archive-file-service/internal/shares"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
	"github.com/okoye-dev/lapis-archive-file-service/internal/transport/rest"
)

type CreateShareRequest struct {
	StorageKey     string `json:"storage_key" binding:"required"`
	OwnerEmail     string `json:"owner_email"`
	RecipientEmail string `json:"recipient_email"`
	TTLHours       int    `json:"ttl_hours"`
}

type CreateShareResponse struct {
	Slug      string    `json:"slug"`
	Code      string    `json:"code"`
	FileName  string    `json:"file_name"`
	FileSize  int64     `json:"file_size"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ShareMetaResponse struct {
	Slug      string    `json:"slug"`
	FileName  string    `json:"file_name"`
	FileSize  int64     `json:"file_size"`
	ExpiresAt time.Time `json:"expires_at"`
	Expired   bool      `json:"expired"`
}

type UnlockShareRequest struct {
	Code     string `json:"code" binding:"required"`
	Download bool   `json:"download"`
}

type UnlockShareResponse struct {
	URL       string `json:"url"`
	ExpiresIn int    `json:"expires_in"`
	FileName  string `json:"file_name"`
	FileSize  int64  `json:"file_size"`
}

const maxRecipientEmailLength = 320

type ShareHandler struct {
	storage   storage.Storage
	perIP     *rateLimiter
	perSlug   *rateLimiter
	metaPerIP *rateLimiter
}

func NewShareHandler(store storage.Storage) *ShareHandler {
	return &ShareHandler{
		storage: store,
		// Two independent limits on unlock attempts. perIP throttles a
		// single client; perSlug bounds total guesses against one share
		// regardless of source IP, so rotating IPs can't brute-force the
		// 6-char code within its TTL. metaPerIP throttles metadata reads.
		perIP:     newRateLimiter(10, time.Minute),
		perSlug:   newRateLimiter(30, time.Minute),
		metaPerIP: newRateLimiter(60, time.Minute),
	}
}

func (h *ShareHandler) CreateShare(c *gin.Context) {
	var req CreateShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rest.BadRequest(c, "storage_key is required")
		return
	}

	if strings.HasPrefix(req.StorageKey, shares.KeyPrefix) || strings.Contains(req.StorageKey, "/") {
		rest.BadRequest(c, "Invalid storage key")
		return
	}

	if len(req.RecipientEmail) > maxRecipientEmailLength || len(req.OwnerEmail) > maxRecipientEmailLength {
		rest.BadRequest(c, "Email too long")
		return
	}

	ctx := c.Request.Context()

	size, err := h.storage.GetFileSize(ctx, req.StorageKey)
	if err != nil {
		log.Printf("create share: sizing %s: %v", req.StorageKey, err)
		rest.NotFound(c, "File not found")
		return
	}

	_, fileName, found := strings.Cut(req.StorageKey, "_")
	if !found {
		fileName = req.StorageKey
	}

	share, code, err := shares.New(req.StorageKey, fileName, size, req.OwnerEmail, req.RecipientEmail, time.Duration(req.TTLHours)*time.Hour)
	if err != nil {
		log.Printf("create share: %v", err)
		rest.InternalError(c, "Could not create share")
		return
	}

	shareKey, err := shares.StorageKeyFor(share.Slug)
	if err != nil {
		log.Printf("share key for %s: %v", share.Slug, err)
		rest.InternalError(c, "Could not create share")
		return
	}

	data, err := json.Marshal(share)
	if err != nil {
		log.Printf("marshal share %s: %v", share.Slug, err)
		rest.InternalError(c, "Could not create share")
		return
	}

	if err := h.storage.PutMetadata(ctx, shareKey, data); err != nil {
		log.Printf("store share %s: %v", share.Slug, err)
		rest.InternalError(c, "Could not create share")
		return
	}

	rest.Success(c, CreateShareResponse{
		Slug:      share.Slug,
		Code:      code,
		FileName:  share.FileName,
		FileSize:  share.FileSize,
		ExpiresAt: share.ExpiresAt,
	})
}

func (h *ShareHandler) GetShare(c *gin.Context) {
	if !h.metaPerIP.Allow(c.ClientIP()) {
		rest.Error(c, http.StatusTooManyRequests, "Too many requests, slow down")
		return
	}

	share, ok := h.loadShare(c)
	if !ok {
		return
	}

	rest.Success(c, ShareMetaResponse{
		Slug:      share.Slug,
		FileName:  share.FileName,
		FileSize:  share.FileSize,
		ExpiresAt: share.ExpiresAt,
		Expired:   share.Expired(),
	})
}

func (h *ShareHandler) UnlockShare(c *gin.Context) {
	slug := c.Param("slug")

	// Validate the slug before it becomes a rate-limiter map key, so an
	// attacker can't seed the map with arbitrary-length garbage.
	shareKey, err := shares.StorageKeyFor(slug)
	if err != nil {
		rest.NotFound(c, "Share not found")
		return
	}

	// perSlug is the real brute-force ceiling: it caps total guesses per
	// share regardless of source IP. perIP is the friendlier per-client cap.
	if !h.perSlug.Allow(slug) || !h.perIP.Allow(slug+"|"+c.ClientIP()) {
		rest.Error(c, http.StatusTooManyRequests, "Too many attempts, slow down")
		return
	}

	var req UnlockShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rest.BadRequest(c, "code is required")
		return
	}

	share, ok := h.loadShareByKey(c, shareKey)
	if !ok {
		return
	}

	if err := share.Verify(req.Code); err != nil {
		switch {
		case errors.Is(err, shares.ErrExpired):
			rest.Error(c, http.StatusGone, "This share has expired")
		case errors.Is(err, shares.ErrWrongCode):
			rest.Error(c, http.StatusForbidden, "Wrong access code")
		default:
			log.Printf("verify share %s: %v", share.Slug, err)
			rest.InternalError(c, "Could not verify code")
		}
		return
	}

	url, err := h.storage.GetPresignedURL(c.Request.Context(), share.StorageKey, req.Download)
	if err != nil {
		log.Printf("presign share %s: %v", share.Slug, err)
		rest.InternalError(c, "Could not prepare download")
		return
	}

	rest.Success(c, UnlockShareResponse{
		URL:       url,
		ExpiresIn: int(storage.PresignTTL.Seconds()),
		FileName:  share.FileName,
		FileSize:  share.FileSize,
	})
}

func (h *ShareHandler) loadShare(c *gin.Context) (*shares.Share, bool) {
	shareKey, err := shares.StorageKeyFor(c.Param("slug"))
	if err != nil {
		rest.NotFound(c, "Share not found")
		return nil, false
	}
	return h.loadShareByKey(c, shareKey)
}

func (h *ShareHandler) loadShareByKey(c *gin.Context, shareKey string) (*shares.Share, bool) {
	data, err := h.storage.GetMetadata(c.Request.Context(), shareKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			rest.NotFound(c, "Share not found")
		} else {
			log.Printf("load share %s: %v", shareKey, err)
			rest.Error(c, http.StatusBadGateway, "Storage temporarily unavailable")
		}
		return nil, false
	}

	var share shares.Share
	if err := json.Unmarshal(data, &share); err != nil {
		log.Printf("unmarshal share %s: %v", shareKey, err)
		rest.InternalError(c, "Could not load share")
		return nil, false
	}

	return &share, true
}
