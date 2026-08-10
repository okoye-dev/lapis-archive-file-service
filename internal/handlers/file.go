package handlers

import (
	"fmt"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
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

type FileHandler struct {
	storage        storage.Storage
	maxUploadBytes int64
}

func NewFileHandler(storage storage.Storage, maxUploadBytes int64) *FileHandler {
	return &FileHandler{storage: storage, maxUploadBytes: maxUploadBytes}
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
	storageKey := fmt.Sprintf("%s_%s", fileID, fileName)

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
	if storageKey == "" || strings.Contains(storageKey, "/") {
		rest.BadRequest(c, "File ID required")
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
