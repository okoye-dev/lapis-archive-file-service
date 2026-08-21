package server

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/okoye-dev/lapis-archive-file-service/internal/auth"
	"github.com/okoye-dev/lapis-archive-file-service/internal/config"
	"github.com/okoye-dev/lapis-archive-file-service/internal/handlers"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
	"github.com/okoye-dev/lapis-archive-file-service/internal/transport/rest"
)

type Deps struct {
	Files          storage.Storage
	Multipart      handlers.MultipartStorage
	Shares         handlers.ShareStore
	Uploads        handlers.UploadRecorder
	UploadLookup   handlers.UploadLookup
	Verifier       *auth.Verifier
	Config         *config.ServerConfig
	RetentionAnon  time.Duration
	RetentionOwned time.Duration
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
	// The multipart complete body holds up to 10000 parts (~70 bytes each).
	const maxCompleteBytes = 2 * 1024 * 1024

	api := router.Group("/api/v1")
	api.GET("/health", handlers.HealthHandler)

	// No bucket-listing or delete endpoint: listing the whole bucket leaked
	// every uploader's files, and deletion is destructive. Uploads return
	// their own key (the client tracks them), downloads use the capability
	// URL below, and cleanup happens via share revoke + the purge worker.
	// Upload endpoints are unauthenticated by design, so throttle per IP to
	// blunt bucket-fill / op abuse. A generous limit that only a script
	// trips.
	uploadLimit := handlers.RateLimitByIP(60, time.Minute)

	fileHandler := handlers.NewFileHandler(deps.Files, deps.Uploads, maxUploadBytes)
	files := api.Group("/files")
	// Optional auth so a signed-in upload is tagged with its owner, which
	// earns the longer retention window.
	if deps.Verifier != nil {
		files.Use(deps.Verifier.Optional())
	}
	files.POST("/presign-upload", uploadLimit, limitBody(maxJSONBytes), fileHandler.PresignUpload)
	files.GET("/:id", fileHandler.GetFile)

	// Multipart lives under its own group: gin cannot mix literal segments
	// with the :id parameter above.
	if deps.Multipart != nil {
		mp := handlers.NewMultipartHandler(deps.Multipart, deps.Uploads, maxUploadBytes)
		uploads := api.Group("/uploads/multipart")
		uploads.Use(uploadLimit)
		if deps.Verifier != nil {
			uploads.Use(deps.Verifier.Optional())
		}
		uploads.POST("/init", limitBody(maxJSONBytes), mp.Init)
		uploads.POST("/part", limitBody(maxJSONBytes), mp.PresignPart)
		uploads.POST("/status", limitBody(maxJSONBytes), mp.Status)
		// Complete's body carries the parts list; give it more room than the
		// tiny control calls so large uploads (many parts) can finalize.
		uploads.POST("/complete", limitBody(maxCompleteBytes), mp.Complete)
		uploads.POST("/abort", limitBody(maxJSONBytes), mp.Abort)
	}

	shareRoutes := api.Group("/shares")

	// Without a database, sharing is down (no bucket-JSON fallback). Answer
	// every share route with a clear maintenance response.
	if deps.Shares == nil {
		shareRoutes.Any("", shareMaintenance)
		shareRoutes.Any("/:slug", shareMaintenance)
		shareRoutes.Any("/:slug/unlock", shareMaintenance)
		return
	}

	shareHandler := handlers.NewShareHandler(deps.Shares, deps.Files, deps.UploadLookup, deps.RetentionAnon, deps.RetentionOwned)

	// Optional auth on create so a signed-in user's share is tagged with their
	// id; anonymous creation still works.
	create := shareRoutes.Group("")
	// Public and unauthenticated; each call hits the DB and a billed S3
	// HeadObject, so throttle per IP like the upload endpoints.
	create.Use(handlers.RateLimitByIP(30, time.Minute))
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
		shareRoutes.GET("", authUnavailable)
		shareRoutes.DELETE("/:slug", authUnavailable)
	}
}

func authUnavailable(c *gin.Context) {
	rest.Error(c, http.StatusServiceUnavailable, "Accounts are unavailable right now")
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
