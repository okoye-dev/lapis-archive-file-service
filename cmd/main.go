package main

import (
	"log"

	"github.com/okoye-dev/lapis-archive-file-service/internal/config"
	"github.com/okoye-dev/lapis-archive-file-service/internal/server"
)

func main() {
	cfg := config.Load()

	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("failed to start: %v", err)
	}

	if err := srv.Start(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
