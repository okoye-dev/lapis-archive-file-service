package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/okoye-dev/lapis-archive-file-service/internal/auth"
	"github.com/okoye-dev/lapis-archive-file-service/internal/domain"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storagekey"
	"github.com/okoye-dev/lapis-archive-file-service/internal/transport/rest"
)

type CreateShareRequest struct {
	StorageKey     string `json:"storage_key" binding:"required"`
	OwnerEmail     string `json:"owner_email"`
	RecipientEmail string `json:"recipient_email"`
	TTLHours       int    `json:"ttl_hours"`
}

type CreateShareResponse struct {
	Slug       string    `json:"slug"`
	Code       string    `json:"code"`
	FileName   string    `json:"file_name"`
	FileSize   int64     `json:"file_size"`
	ShareCount int       `json:"share_count"`
	Rotated    bool      `json:"rotated"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// ShareMetaResponse is public (GET /shares/:slug needs no code), so it carries
// only file facts, never the recipient's email.
type ShareMetaResponse struct {
	Slug       string    `json:"slug"`
	FileName   string    `json:"file_name"`
	FileSize   int64     `json:"file_size"`
	ShareCount int       `json:"share_count"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Expired    bool      `json:"expired"`
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

type SharesListResponse struct {
	Shares []ShareMetaResponse `json:"shares"`
}

type RevokeShareResponse struct {
	Revoked string `json:"revoked"`
}

const maxEmailLength = 320

// ShareStore is the slice of the share store the handlers need, declared here
// at the consumer so it stays minimal and the concrete DBStore need not be an
// interface itself.
type ShareStore interface {
	Create(ctx context.Context, s *domain.Share) error
	GetBySlug(ctx context.Context, slug string) (*domain.Share, error)
	GetByStorageKey(ctx context.Context, storageKey string) (*domain.Share, error)
	RotateCode(ctx context.Context, s *domain.Share) error
	ListByOwner(ctx context.Context, ownerID string) ([]*domain.Share, error)
	Delete(ctx context.Context, slug, ownerID string) error
}

// UploadLookup fetches a recorded upload so a share can cap its advertised
// expiry to the file's retention deadline. Nil without a DB.
type UploadLookup interface {
	GetByStorageKey(ctx context.Context, storageKey string) (*domain.Upload, error)
}

type ShareHandler struct {
	store     ShareStore
	files     storage.Storage
	uploads   UploadLookup
	anonTTL   time.Duration
	ownedTTL  time.Duration
	perIP     *rateLimiter
	perSlug   *rateLimiter
	metaPerIP *rateLimiter
}

func NewShareHandler(store ShareStore, files storage.Storage, uploads UploadLookup, anonTTL, ownedTTL time.Duration) *ShareHandler {
	return &ShareHandler{
		store:    store,
		files:    files,
		uploads:  uploads,
		anonTTL:  anonTTL,
		ownedTTL: ownedTTL,
		// Two independent limits on unlock attempts. perIP throttles a single
		// client; perSlug bounds total guesses against one share regardless of
		// source IP. metaPerIP throttles metadata reads.
		perIP:     newRateLimiter(10, time.Minute),
		perSlug:   newRateLimiter(30, time.Minute),
		metaPerIP: newRateLimiter(60, time.Minute),
	}
}

// capToRetention shortens a share's expiry so it never outlives the file. The
// retention worker deletes the object at upload.CreatedAt + the window for its
// owner, so the API must not promise a share that lasts past that. No-op for
// untracked (legacy) uploads or when no DB is wired.
func (h *ShareHandler) capToRetention(ctx context.Context, storageKey string, share *domain.Share) {
	if h.uploads == nil {
		return
	}
	up, err := h.uploads.GetByStorageKey(ctx, storageKey)
	if err != nil {
		return
	}
	window := h.anonTTL
	if up.OwnerID != "" {
		window = h.ownedTTL
	}
	if window <= 0 {
		return
	}
	if deadline := up.CreatedAt.Add(window); share.ExpiresAt.After(deadline) {
		share.ExpiresAt = deadline
	}
}

func (h *ShareHandler) CreateShare(c *gin.Context) {
	var req CreateShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rest.BadRequest(c, "storage_key is required")
		return
	}

	if !validKey(req.StorageKey) {
		rest.BadRequest(c, "Invalid storage key")
		return
	}
	if len(req.RecipientEmail) > maxEmailLength || len(req.OwnerEmail) > maxEmailLength {
		rest.BadRequest(c, "Email too long")
		return
	}

	ctx := c.Request.Context()

	// A file keeps one link: re-sharing rotates the code on the existing share.
	existing, err := h.store.GetByStorageKey(ctx, req.StorageKey)
	switch {
	case err == nil:
		h.rotateShare(c, existing, req)
		return
	case !errors.Is(err, domain.ErrNotFound):
		log.Printf("create share: lookup %s: %v", req.StorageKey, err)
		rest.Error(c, http.StatusBadGateway, "Storage temporarily unavailable")
		return
	}

	size, err := h.files.GetFileSize(ctx, req.StorageKey)
	if err != nil {
		log.Printf("create share: sizing %s: %v", req.StorageKey, err)
		rest.NotFound(c, "File not found")
		return
	}

	_, fileName := storagekey.Split(req.StorageKey)

	// Owner comes from the verified token when signed in; the request's
	// owner_email is only a fallback for anonymous shares.
	ownerID, ownerEmail := "", req.OwnerEmail
	if user, ok := auth.UserFrom(c); ok {
		ownerID = user.ID
		if ownerEmail == "" {
			ownerEmail = user.Email
		}
	}

	share, code, err := domain.NewShare(req.StorageKey, fileName, size, ownerID, ownerEmail, req.RecipientEmail, time.Duration(req.TTLHours)*time.Hour)
	if err != nil {
		log.Printf("create share: %v", err)
		rest.InternalError(c, "Could not create share")
		return
	}
	h.capToRetention(ctx, req.StorageKey, share)

	if err := h.store.Create(ctx, share); err != nil {
		// A concurrent create won the race (unique storage_key); rotate instead.
		if racer, gerr := h.store.GetByStorageKey(ctx, req.StorageKey); gerr == nil {
			h.rotateShare(c, racer, req)
			return
		}
		log.Printf("store share %s: %v", share.Slug, err)
		rest.InternalError(c, "Could not create share")
		return
	}

	rest.Success(c, CreateShareResponse{
		Slug:       share.Slug,
		Code:       code,
		FileName:   share.FileName,
		FileSize:   share.FileSize,
		ShareCount: share.ShareCount,
		ExpiresAt:  share.ExpiresAt,
	})
}

// rotateShare re-shares an existing file: same slug, fresh code and expiry.
func (h *ShareHandler) rotateShare(c *gin.Context, share *domain.Share, req CreateShareRequest) {
	user, signedIn := auth.UserFrom(c)

	// The storage key leaks to anyone who unlocked the share (it's in the
	// download URL), so an owned share may only be rotated by its owner.
	if share.OwnerID != "" {
		if !signedIn || user.ID != share.OwnerID {
			rest.Error(c, http.StatusForbidden,
				"Only the person who shared this file can reshare it")
			return
		}
	} else if signedIn {
		share.OwnerID = user.ID
	}
	share.RecipientEmail = req.RecipientEmail

	code, err := share.Rotate(time.Duration(req.TTLHours) * time.Hour)
	if errors.Is(err, domain.ErrShareLimit) {
		rest.Error(c, http.StatusConflict,
			"This file has reached its share limit of 3 codes")
		return
	}
	if err != nil {
		log.Printf("rotate share %s: %v", share.Slug, err)
		rest.InternalError(c, "Could not create share")
		return
	}
	h.capToRetention(c.Request.Context(), share.StorageKey, share)

	if err := h.store.RotateCode(c.Request.Context(), share); err != nil {
		log.Printf("rotate share %s: %v", share.Slug, err)
		rest.InternalError(c, "Could not create share")
		return
	}

	rest.Success(c, CreateShareResponse{
		Slug:       share.Slug,
		Code:       code,
		FileName:   share.FileName,
		FileSize:   share.FileSize,
		ShareCount: share.ShareCount,
		Rotated:    true,
		ExpiresAt:  share.ExpiresAt,
	})
}

func (h *ShareHandler) GetShare(c *gin.Context) {
	if !h.metaPerIP.Allow(c.ClientIP()) {
		rest.Error(c, http.StatusTooManyRequests, "Too many requests, slow down")
		return
	}

	share, ok := h.loadShare(c, c.Param("slug"))
	if !ok {
		return
	}

	rest.Success(c, toMeta(share))
}

func (h *ShareHandler) UnlockShare(c *gin.Context) {
	slug := c.Param("slug")

	// Validate the slug before it becomes a rate-limiter map key, so an
	// attacker can't seed the map with arbitrary-length garbage.
	if err := domain.ValidateSlug(slug); err != nil {
		rest.NotFound(c, "Share not found")
		return
	}

	if !h.perSlug.Allow(slug) || !h.perIP.Allow(slug+"|"+c.ClientIP()) {
		rest.Error(c, http.StatusTooManyRequests, "Too many attempts, slow down")
		return
	}

	var req UnlockShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rest.BadRequest(c, "code is required")
		return
	}

	share, ok := h.loadShare(c, slug)
	if !ok {
		return
	}

	if err := share.Verify(req.Code); err != nil {
		switch {
		case errors.Is(err, domain.ErrExpired):
			rest.Error(c, http.StatusGone, "This share has expired")
		case errors.Is(err, domain.ErrWrongCode):
			rest.Error(c, http.StatusForbidden, "Wrong access code")
		default:
			log.Printf("verify share %s: %v", share.Slug, err)
			rest.InternalError(c, "Could not verify code")
		}
		return
	}

	url, err := h.files.GetPresignedURL(c.Request.Context(), share.StorageKey, req.Download)
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

func (h *ShareHandler) ListMine(c *gin.Context) {
	user, ok := auth.UserFrom(c)
	if !ok {
		rest.Error(c, http.StatusUnauthorized, "Authentication required")
		return
	}

	list, err := h.store.ListByOwner(c.Request.Context(), user.ID)
	if err != nil {
		log.Printf("list shares for %s: %v", user.ID, err)
		rest.InternalError(c, "Could not load your shares")
		return
	}

	out := make([]ShareMetaResponse, 0, len(list))
	for _, s := range list {
		out = append(out, toMeta(s))
	}
	rest.Success(c, SharesListResponse{Shares: out})
}

func (h *ShareHandler) RevokeShare(c *gin.Context) {
	user, ok := auth.UserFrom(c)
	if !ok {
		rest.Error(c, http.StatusUnauthorized, "Authentication required")
		return
	}

	slug := c.Param("slug")
	if err := domain.ValidateSlug(slug); err != nil {
		rest.NotFound(c, "Share not found")
		return
	}

	if err := h.store.Delete(c.Request.Context(), slug, user.ID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			rest.NotFound(c, "Share not found")
			return
		}
		log.Printf("revoke share %s: %v", slug, err)
		rest.InternalError(c, "Could not revoke share")
		return
	}

	rest.Success(c, RevokeShareResponse{Revoked: slug})
}

func (h *ShareHandler) loadShare(c *gin.Context, slug string) (*domain.Share, bool) {
	if err := domain.ValidateSlug(slug); err != nil {
		rest.NotFound(c, "Share not found")
		return nil, false
	}

	share, err := h.store.GetBySlug(c.Request.Context(), slug)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			rest.NotFound(c, "Share not found")
		} else {
			log.Printf("load share %s: %v", slug, err)
			rest.Error(c, http.StatusBadGateway, "Storage temporarily unavailable")
		}
		return nil, false
	}
	return share, true
}

func toMeta(s *domain.Share) ShareMetaResponse {
	return ShareMetaResponse{
		Slug:       s.Slug,
		FileName:   s.FileName,
		FileSize:   s.FileSize,
		ShareCount: s.ShareCount,
		CreatedAt:  s.CreatedAt,
		ExpiresAt:  s.ExpiresAt,
		Expired:    s.Expired(),
	}
}
