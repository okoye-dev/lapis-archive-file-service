package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/okoye-dev/lapis-archive-file-service/internal/config"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
)

type Server struct {
	httpServer *http.Server
	config     *config.Config
	storage    storage.Storage
}

func New(cfg *config.Config) (*Server, error) {
	s3Storage, err := storage.NewS3Storage(&cfg.S3)
	if err != nil {
		return nil, fmt.Errorf("initializing storage: %w", err)
	}

	return &Server{
		config:  cfg,
		storage: s3Storage,
	}, nil
}

func (s *Server) Start() error {
	gin.SetMode(s.config.Logging.Mode)
	router := gin.Default()
	SetupRoutes(router, s.storage, &s.config.Server)

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.config.Server.Port),
		Handler:      router,
		ReadTimeout:  time.Duration(s.config.Server.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(s.config.Server.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(s.config.Server.IdleTimeout) * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("server listening on port %d", s.config.Server.Port)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case <-quit:
	}

	log.Println("shutdown signal received")

	shutdownTimeout := time.Duration(s.config.Server.ShutdownTimeout) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down: %w", err)
	}

	log.Println("server stopped")
	return nil
}
