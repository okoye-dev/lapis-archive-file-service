package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
	"github.com/okoye-dev/lapis-archive-file-service/internal/transport/rest"
)

// R2 requires every non-final part to be the same size, so it's fixed.
const (
	PartSize           = 8 * 1024 * 1024
	MultipartThreshold = 2 * PartSize
	maxParts           = 10000
)

type MultipartStorage interface {
	CreateMultipartUpload(ctx context.Context, key, contentType string) (string, error)
	PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32) (string, error)
	ListParts(ctx context.Context, key, uploadID string) ([]storage.Part, error)
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []storage.Part) error
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error
}

type MultipartHandler struct {
	storage        MultipartStorage
	uploads        UploadRecorder
	maxUploadBytes int64
}

func NewMultipartHandler(storage MultipartStorage, uploads UploadRecorder, maxUploadBytes int64) *MultipartHandler {
	return &MultipartHandler{storage: storage, uploads: uploads, maxUploadBytes: maxUploadBytes}
}

type MultipartInitRequest struct {
	Name        string `json:"name" binding:"required"`
	Size        int64  `json:"size" binding:"required,gt=0"`
	ContentType string `json:"content_type"`
}

type MultipartInitResponse struct {
	StorageKey string `json:"storage_key"`
	UploadID   string `json:"upload_id"`
	Name       string `json:"name"`
	PartSize   int64  `json:"part_size"`
	PartCount  int    `json:"part_count"`
	ExpiresIn  int    `json:"expires_in"`
}

type MultipartRef struct {
	StorageKey string `json:"storage_key" binding:"required"`
	UploadID   string `json:"upload_id" binding:"required"`
}

type PresignPartRequest struct {
	MultipartRef
	PartNumber int32 `json:"part_number" binding:"required,gt=0"`
}

type PresignPartResponse struct {
	URL       string `json:"url"`
	ExpiresIn int    `json:"expires_in"`
}

type MultipartStatusResponse struct {
	Parts []storage.Part `json:"parts"`
}

type MultipartCompleteRequest struct {
	MultipartRef
	Parts []storage.Part `json:"parts" binding:"required,min=1"`
}

type MultipartCompleteResponse struct {
	StorageKey string `json:"storage_key"`
	ID         string `json:"id"`
	Name       string `json:"name"`
}

type MultipartAbortResponse struct {
	Aborted string `json:"aborted"`
}

func (h *MultipartHandler) Init(c *gin.Context) {
	var req MultipartInitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rest.BadRequest(c, "name and size are required")
		return
	}
	if req.Size > h.maxUploadBytes {
		rest.Error(c, http.StatusRequestEntityTooLarge, "File too large")
		return
	}

	partCount := int((req.Size + PartSize - 1) / PartSize)
	if partCount > maxParts {
		rest.Error(c, http.StatusRequestEntityTooLarge, "File too large")
		return
	}

	fileName := sanitizeFilename(req.Name)
	fileID := uuid.New().String()
	storageKey := fmt.Sprintf("%s_%s", fileID, fileName)

	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	uploadID, err := h.storage.CreateMultipartUpload(c.Request.Context(), storageKey, contentType)
	if err != nil {
		log.Printf("multipart init %s: %v", storageKey, err)
		rest.InternalError(c, "Could not prepare upload")
		return
	}

	if err := recordUploadFor(c, h.uploads, storageKey, fileName, req.Size); err != nil {
		log.Printf("record upload %s: %v", storageKey, err)
		if aerr := h.storage.AbortMultipartUpload(c.Request.Context(), storageKey, uploadID); aerr != nil {
			log.Printf("abort untracked multipart %s: %v", storageKey, aerr)
		}
		rest.InternalError(c, "Could not prepare upload")
		return
	}

	rest.Success(c, MultipartInitResponse{
		StorageKey: storageKey,
		UploadID:   uploadID,
		Name:       fileName,
		PartSize:   PartSize,
		PartCount:  partCount,
		ExpiresIn:  int(storage.UploadPresignTTL.Seconds()),
	})
}

func (h *MultipartHandler) PresignPart(c *gin.Context) {
	var req PresignPartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rest.BadRequest(c, "storage_key, upload_id and part_number are required")
		return
	}
	if !validKey(req.StorageKey) || req.PartNumber > maxParts {
		rest.BadRequest(c, "Invalid storage key or part number")
		return
	}

	url, err := h.storage.PresignUploadPart(c.Request.Context(), req.StorageKey, req.UploadID, req.PartNumber)
	if err != nil {
		log.Printf("presign part %d of %s: %v", req.PartNumber, req.StorageKey, err)
		rest.InternalError(c, "Could not prepare upload")
		return
	}

	rest.Success(c, PresignPartResponse{URL: url, ExpiresIn: int(storage.UploadPresignTTL.Seconds())})
}

func (h *MultipartHandler) Status(c *gin.Context) {
	var req MultipartRef
	if err := c.ShouldBindJSON(&req); err != nil {
		rest.BadRequest(c, "storage_key and upload_id are required")
		return
	}
	if !validKey(req.StorageKey) {
		rest.BadRequest(c, "Invalid storage key")
		return
	}

	parts, err := h.storage.ListParts(c.Request.Context(), req.StorageKey, req.UploadID)
	if err != nil {
		h.multipartError(c, "status", req.StorageKey, err)
		return
	}

	rest.Success(c, MultipartStatusResponse{Parts: parts})
}

func (h *MultipartHandler) Complete(c *gin.Context) {
	var req MultipartCompleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rest.BadRequest(c, "storage_key, upload_id and parts are required")
		return
	}
	if !validKey(req.StorageKey) {
		rest.BadRequest(c, "Invalid storage key")
		return
	}

	// Part PUTs aren't size-limited, so cap the total here against what the
	// bucket actually holds.
	held, err := h.storage.ListParts(c.Request.Context(), req.StorageKey, req.UploadID)
	if err != nil {
		h.multipartError(c, "complete", req.StorageKey, err)
		return
	}
	var total int64
	for _, p := range held {
		total += p.Size
	}
	if total > h.maxUploadBytes {
		if aerr := h.storage.AbortMultipartUpload(c.Request.Context(), req.StorageKey, req.UploadID); aerr != nil {
			log.Printf("multipart abort oversized %s: %v", req.StorageKey, aerr)
		}
		rest.Error(c, http.StatusRequestEntityTooLarge, "File too large")
		return
	}

	// R2 needs parts in ascending order; the client may send them shuffled.
	sort.Slice(req.Parts, func(i, j int) bool { return req.Parts[i].PartNumber < req.Parts[j].PartNumber })

	if err := h.storage.CompleteMultipartUpload(c.Request.Context(), req.StorageKey, req.UploadID, req.Parts); err != nil {
		h.multipartError(c, "complete", req.StorageKey, err)
		return
	}

	fileID, fileName, _ := strings.Cut(req.StorageKey, "_")
	rest.Success(c, MultipartCompleteResponse{
		StorageKey: req.StorageKey,
		ID:         fileID,
		Name:       fileName,
	})
}

func (h *MultipartHandler) Abort(c *gin.Context) {
	var req MultipartRef
	if err := c.ShouldBindJSON(&req); err != nil {
		rest.BadRequest(c, "storage_key and upload_id are required")
		return
	}
	if !validKey(req.StorageKey) {
		rest.BadRequest(c, "Invalid storage key")
		return
	}

	if err := h.storage.AbortMultipartUpload(c.Request.Context(), req.StorageKey, req.UploadID); err != nil &&
		!errors.Is(err, storage.ErrNoSuchUpload) {
		log.Printf("multipart abort %s: %v", req.StorageKey, err)
		rest.InternalError(c, "Could not cancel upload")
		return
	}

	rest.Success(c, MultipartAbortResponse{Aborted: req.StorageKey})
}

// A vanished session (404) tells the client to restart; anything else is a 500.
func (h *MultipartHandler) multipartError(c *gin.Context, op, key string, err error) {
	if errors.Is(err, storage.ErrNoSuchUpload) {
		rest.NotFound(c, "Upload session no longer exists, start it again")
		return
	}
	log.Printf("multipart %s %s: %v", op, key, err)
	rest.InternalError(c, "Could not finish upload")
}

// validKey holds a client-supplied key to the same shape sanitizeFilename
// produces, so control chars and oversized keys can't slip through.
func validKey(key string) bool {
	if key == "" || len(key) > maxKeyLength {
		return false
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

const maxKeyLength = 40 + maxFilenameLength
