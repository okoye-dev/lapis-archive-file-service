package server

import (
	"log"
	"net/http"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/okoye-dev/lapis-archive-file-service/internal/auth"
	"github.com/okoye-dev/lapis-archive-file-service/internal/config"
	"github.com/okoye-dev/lapis-archive-file-service/internal/handlers"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
	"github.com/okoye-dev/lapis-archive-file-service/internal/transport/rest"
)

type Deps struct {
	Files    storage.Storage
	Shares   handlers.ShareStore
	Verifier *auth.Verifier
	Config   *config.ServerConfig
}

func SetupRoutes(router *gin.Engine, deps Deps) {
	cfg := deps.Config

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
	const maxJSONBytes = 64 * 1024

	api := router.Group("/api/v1")
	api.GET("/health", handlers.HealthHandler)

	fileHandler := handlers.NewFileHandler(deps.Files, maxUploadBytes)
	files := api.Group("/files")
	files.GET("", fileHandler.GetFiles)
	files.POST("/presign-upload", limitBody(maxJSONBytes), fileHandler.PresignUpload)
	files.GET("/:id", fileHandler.GetFile)
	files.DELETE("/:id", fileHandler.DeleteFile)

	shareRoutes := api.Group("/shares")

	// Without a database, sharing is down (no bucket-JSON fallback). Answer
	// every share route with a clear maintenance response.
	if deps.Shares == nil {
		shareRoutes.Any("", shareMaintenance)
		shareRoutes.Any("/:slug", shareMaintenance)
		shareRoutes.Any("/:slug/unlock", shareMaintenance)
		return
	}

	shareHandler := handlers.NewShareHandler(deps.Shares, deps.Files)

	// Optional auth on create so a signed-in user's share is tagged with their
	// id; anonymous creation still works.
	create := shareRoutes.Group("")
	if deps.Verifier != nil {
		create.Use(deps.Verifier.Optional())
	}
	create.POST("", limitBody(maxJSONBytes), shareHandler.CreateShare)

	shareRoutes.GET("/:slug", shareHandler.GetShare)
	shareRoutes.POST("/:slug/unlock", limitBody(maxJSONBytes), shareHandler.UnlockShare)

	// History and revoke require a verified user, so they only exist when auth
	// is configured.
	if deps.Verifier != nil {
		me := shareRoutes.Group("")
		me.Use(deps.Verifier.Required())
		me.GET("", shareHandler.ListMine)
		me.DELETE("/:slug", shareHandler.RevokeShare)
	} else {
		log.Println("auth: history and revoke endpoints disabled")
	}
}

func limitBody(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

func shareMaintenance(c *gin.Context) {
	rest.Error(c, http.StatusServiceUnavailable,
		"Sharing is temporarily unavailable while the database is under maintenance. Back in a few minutes.")
}
