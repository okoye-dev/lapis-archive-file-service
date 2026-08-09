package server

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/okoye-dev/oss-archive/internal/handlers"
	"github.com/okoye-dev/oss-archive/internal/storage"
)

func SetupRoutes(router *gin.Engine, store storage.Storage) {
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))

	api := router.Group("/api/v1")

	api.GET("/health", handlers.HealthHandler)

	fileHandler := handlers.NewFileHandler(store)
	files := api.Group("/files")
	files.GET("", fileHandler.GetFiles)
	files.POST("", fileHandler.UploadFile)
	files.GET("/:id", fileHandler.GetFile)
	files.DELETE("/:id", fileHandler.DeleteFile)
}
