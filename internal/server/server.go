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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/okoye-dev/lapis-archive-file-service/internal/audit"
	"github.com/okoye-dev/lapis-archive-file-service/internal/auth"
	"github.com/okoye-dev/lapis-archive-file-service/internal/config"
	"github.com/okoye-dev/lapis-archive-file-service/internal/handlers"
	"github.com/okoye-dev/lapis-archive-file-service/internal/migrate"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
	"github.com/okoye-dev/lapis-archive-file-service/internal/store"
	"github.com/okoye-dev/lapis-archive-file-service/internal/worker"
)

type Server struct {
	httpServer *http.Server
	config     *config.Config
	storage    storage.Storage
	shareStore handlers.ShareStore
	verifier   *auth.Verifier
	pool       *pgxpool.Pool
	runner     *worker.Runner
}

func New(cfg *config.Config) (*Server, error) {
	s3Storage, err := storage.NewS3Storage(&cfg.S3)
	if err != nil {
		return nil, fmt.Errorf("initializing storage: %w", err)
	}

	srv := &Server{config: cfg, storage: s3Storage}

	if cfg.Database.URL != "" {
		pool, err := pgxpool.New(context.Background(), cfg.Database.URL)
		if err != nil {
			return nil, fmt.Errorf("connecting to database: %w", err)
		}
		if err := pool.Ping(context.Background()); err != nil {
			return nil, fmt.Errorf("pinging database: %w", err)
		}
		if err := migrate.Run(context.Background(), pool); err != nil {
			return nil, fmt.Errorf("running migrations: %w", err)
		}
		pg := store.NewShareStore(pool)
		srv.pool = pool
		srv.shareStore = pg
		srv.runner = worker.New(
			audit.NewDBRunRecorder(pool).Record,
			worker.PurgeExpiredShares{
				Store:   pg,
				Objects: s3Storage,
				Auditor: audit.NewDBAuditor(pool),
			},
		)
		log.Println("shares: database store enabled")
	} else {
		log.Println("shares: DATABASE_URL not set, share endpoints disabled")
	}

	if cfg.Auth.JWKSURL != "" {
		v, err := auth.NewVerifier(context.Background(), cfg.Auth.JWKSURL, cfg.Auth.Issuer)
		if err != nil {
			return nil, fmt.Errorf("initializing auth: %w", err)
		}
		srv.verifier = v
		log.Println("auth: jwks verification enabled")
	} else {
		log.Println("auth: AUTH_JWKS_URL not set, authenticated endpoints disabled")
	}

	return srv, nil
}

func (s *Server) Start() error {
	gin.SetMode(s.config.Logging.Mode)
	router := gin.Default()

	// By default trust no proxies, so ClientIP() is the real TCP peer and
	// X-Forwarded-For can't be spoofed to defeat rate limiting. Behind a
	// known proxy (e.g. Railway), set TRUSTED_PROXIES to its CIDR(s).
	if err := router.SetTrustedProxies(s.config.Server.TrustedProxies); err != nil {
		return fmt.Errorf("setting trusted proxies: %w", err)
	}

	SetupRoutes(router, Deps{
		Files:    s.storage,
		Shares:   s.shareStore,
		Verifier: s.verifier,
		Config:   &s.config.Server,
	})

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.config.Server.Port),
		Handler:      router,
		ReadTimeout:  time.Duration(s.config.Server.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(s.config.Server.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(s.config.Server.IdleTimeout) * time.Second,
	}

	// Background jobs run until shutdown cancels this context.
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	if s.runner != nil {
		s.runner.Start(workerCtx)
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

	stopWorkers()
	if s.runner != nil {
		s.runner.Wait()
	}
	if s.pool != nil {
		s.pool.Close()
	}

	log.Println("server stopped")
	return nil
}
