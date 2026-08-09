package server

import (
	"net/http"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/okoye-dev/lapis-archive-file-service/internal/config"
	"github.com/okoye-dev/lapis-archive-file-service/internal/handlers"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
)

func SetupRoutes(router *gin.Engine, store storage.Storage, cfg *config.ServerConfig) {
	corsConfig := cors.Config{
		AllowMethods:  []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:  []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders: []string{"Content-Length"},
	}
	if len(cfg.AllowedOrigins) == 1 && cfg.AllowedOrigins[0] == "*" {
		corsConfig.AllowAllOrigins = true
	} else {
		corsConfig.AllowOrigins = cfg.AllowedOrigins
	}
	router.Use(cors.New(corsConfig))

	maxUploadBytes := cfg.MaxUploadMB * 1024 * 1024
	router.MaxMultipartMemory = 32 * 1024 * 1024

	// JSON bodies are tiny; cap them so a giant payload can't be buffered
	// into memory before binding fails.
	const maxJSONBytes = 64 * 1024

	api := router.Group("/api/v1")

	api.GET("/health", handlers.HealthHandler)

	fileHandler := handlers.NewFileHandler(store, maxUploadBytes)
	files := api.Group("/files")
	files.GET("", fileHandler.GetFiles)
	files.POST("/presign-upload", limitBody(maxJSONBytes), fileHandler.PresignUpload)
	files.GET("/:id", fileHandler.GetFile)
	files.DELETE("/:id", fileHandler.DeleteFile)

	shareHandler := handlers.NewShareHandler(store)
	shareRoutes := api.Group("/shares")
	shareRoutes.POST("", limitBody(maxJSONBytes), shareHandler.CreateShare)
	shareRoutes.GET("/:slug", shareHandler.GetShare)
	shareRoutes.POST("/:slug/unlock", limitBody(maxJSONBytes), shareHandler.UnlockShare)
}

func limitBody(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}
