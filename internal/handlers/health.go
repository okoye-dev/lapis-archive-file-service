package handlers

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/okoye-dev/oss-archive/internal/transport/rest"
)

func HealthHandler(c *gin.Context) {
	response := rest.HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().Unix(),
		Service:   "lapis-archive-file-service",
	}

	rest.Success(c, response)
}
