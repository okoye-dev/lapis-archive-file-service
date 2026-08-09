package handlers

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
	"github.com/okoye-dev/lapis-archive-file-service/internal/transport/rest"
)

type FilesResponse struct {
	Files []FileResponse `json:"files"`
}

type FileResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	StorageKey string `json:"storage_key"`
	Size       int64  `json:"size"`
}

type FileUploadResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	StorageKey string `json:"storage_key"`
	FileSize   int64  `json:"file_size"`
	FileType   string `json:"file_type"`
	CreatedAt  string `json:"created_at"`
}

type FileDownloadResponse struct {
	URL       string `json:"url"`
	ExpiresIn int    `json:"expires_in"`
	Download  bool   `json:"download"`
}

type FileHandler struct {
	storage storage.Storage
}

func NewFileHandler(storage storage.Storage) *FileHandler {
	return &FileHandler{storage: storage}
}

func (h *FileHandler) UploadFile(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		rest.BadRequest(c, "No file provided")
		return
	}
	defer file.Close()

	fileID := uuid.New().String()
	storageKey := fmt.Sprintf("%s_%s", fileID, header.Filename)

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if err := h.storage.UploadFile(c.Request.Context(), storageKey, file, header.Size, contentType); err != nil {
		log.Printf("upload %s: %v", storageKey, err)
		rest.InternalError(c, "Upload failed, try again")
		return
	}

	rest.Success(c, FileUploadResponse{
		ID:         fileID,
		Name:       header.Filename,
		StorageKey: storageKey,
		FileSize:   header.Size,
		FileType:   contentType,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *FileHandler) GetFiles(c *gin.Context) {
	ctx := c.Request.Context()

	keys, err := h.storage.ListFiles(ctx)
	if err != nil {
		log.Printf("list files: %v", err)
		rest.InternalError(c, "Could not list files")
		return
	}

	fileList := make([]FileResponse, 0, len(keys))
	for _, storageKey := range keys {
		fileID, fileName, found := strings.Cut(storageKey, "_")
		if !found {
			fileID = storageKey
			fileName = filepath.Base(storageKey)
		}

		fileSize, err := h.storage.GetFileSize(ctx, storageKey)
		if err != nil {
			log.Printf("size %s: %v", storageKey, err)
			fileSize = 0
		}

		fileList = append(fileList, FileResponse{
			ID:         fileID,
			Name:       fileName,
			StorageKey: storageKey,
			Size:       fileSize,
		})
	}

	rest.Success(c, FilesResponse{Files: fileList})
}

func (h *FileHandler) GetFile(c *gin.Context) {
	storageKey := c.Param("id")
	if storageKey == "" {
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

func (h *FileHandler) DeleteFile(c *gin.Context) {
	storageKey := c.Param("id")
	if storageKey == "" {
		rest.BadRequest(c, "File ID required")
		return
	}

	if err := h.storage.DeleteFile(c.Request.Context(), storageKey); err != nil {
		log.Printf("delete %s: %v", storageKey, err)
		rest.InternalError(c, "Delete failed, try again")
		return
	}

	rest.Success(c, gin.H{"deleted": storageKey})
}
