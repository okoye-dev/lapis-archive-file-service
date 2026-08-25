package handlers

import (
	"context"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/okoye-dev/lapis-archive-file-service/internal/auth"
	"github.com/okoye-dev/lapis-archive-file-service/internal/domain"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storagekey"
	"github.com/okoye-dev/lapis-archive-file-service/internal/transport/rest"
)

const maxFilenameLength = 180

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)

	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)

	cleaned = strings.TrimSpace(cleaned)
	if runes := []rune(cleaned); len(runes) > maxFilenameLength {
		ext := path.Ext(cleaned)
		if len([]rune(ext)) > 20 {
			ext = ""
		}
		keep := maxFilenameLength - len([]rune(ext))
		cleaned = string(runes[:keep]) + ext
	}

	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "file"
	}
	return cleaned
}

type FileDownloadResponse struct {
	URL       string `json:"url"`
	ExpiresIn int    `json:"expires_in"`
	Download  bool   `json:"download"`
}

type PresignUploadRequest struct {
	Name        string `json:"name" binding:"required"`
	Size        int64  `json:"size" binding:"required,gt=0"`
	ContentType string `json:"content_type"`
}

type PresignUploadResponse struct {
	UploadURL  string `json:"upload_url"`
	StorageKey string `json:"storage_key"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	ExpiresIn  int    `json:"expires_in"`
}

// UploadRecorder records uploads for the retention worker. Nil without a DB.
type UploadRecorder interface {
	Create(ctx context.Context, up *domain.Upload) error
}

type FileHandler struct {
	storage        storage.Storage
	uploads        UploadRecorder
	maxUploadBytes int64
}

func NewFileHandler(storage storage.Storage, uploads UploadRecorder, maxUploadBytes int64) *FileHandler {
	return &FileHandler{storage: storage, uploads: uploads, maxUploadBytes: maxUploadBytes}
}

// recordUploadFor tags the upload with its owner (when signed in) for retention.
func recordUploadFor(c *gin.Context, uploads UploadRecorder, storageKey, fileName string, size int64) error {
	if uploads == nil {
		return nil
	}
	ownerID := ""
	if user, ok := auth.UserFrom(c); ok {
		ownerID = user.ID
	}
	return uploads.Create(c.Request.Context(), &domain.Upload{
		StorageKey: storageKey,
		OwnerID:    ownerID,
		FileName:   fileName,
		SizeBytes:  size,
		CreatedAt:  time.Now().UTC(),
	})
}

func (h *FileHandler) PresignUpload(c *gin.Context) {
	var req PresignUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rest.BadRequest(c, "name and size are required")
		return
	}

	if req.Size > h.maxUploadBytes {
		rest.Error(c, http.StatusRequestEntityTooLarge, "File too large")
		return
	}

	fileName := sanitizeFilename(req.Name)
	fileID := uuid.New().String()
	storageKey := storagekey.Build(fileID, fileName)

	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	url, err := h.storage.GetPresignedUploadURL(c.Request.Context(), storageKey, req.Size, contentType)
	if err != nil {
		log.Printf("presign upload %s: %v", storageKey, err)
		rest.InternalError(c, "Could not prepare upload")
		return
	}

	if err := recordUploadFor(c, h.uploads, storageKey, fileName, req.Size); err != nil {
		log.Printf("record upload %s: %v", storageKey, err)
		rest.InternalError(c, "Could not prepare upload")
		return
	}

	rest.Success(c, PresignUploadResponse{
		UploadURL:  url,
		StorageKey: storageKey,
		ID:         fileID,
		Name:       fileName,
		ExpiresIn:  int(storage.UploadPresignTTL.Seconds()),
	})
}

func (h *FileHandler) GetFile(c *gin.Context) {
	storageKey := c.Param("id")
	if !validKey(storageKey) {
		rest.BadRequest(c, "Invalid file ID")
		return
	}

	forceDownload := c.Query("download") == "true"

	presignedURL, err := h.storage.GetPresignedURL(c.Request.Context(), storageKey, forceDownload)
	if err != nil {
		log.Printf("presign %s: %v", storageKey, err)
		rest.NotFound(c, "File not found")
		return
	}

	rest.Success(c, FileDownloadResponse{
		URL:       presignedURL,
		Download:  forceDownload,
		ExpiresIn: int(storage.PresignTTL.Seconds()),
	})
}
