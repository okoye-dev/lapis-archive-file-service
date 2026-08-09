package rest

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp int64  `json:"timestamp"`
	Service   string `json:"service"`
}

type ErrorResponse struct {
	Error string `json:"error"`
	Code  int    `json:"code"`
}

func Success(c *gin.Context, data any) {
	c.JSON(http.StatusOK, data)
}

func Error(c *gin.Context, code int, message string) {
	c.JSON(code, ErrorResponse{
		Error: message,
		Code:  code,
	})
}

func InternalError(c *gin.Context, message string) {
	Error(c, http.StatusInternalServerError, message)
}

func BadRequest(c *gin.Context, message string) {
	Error(c, http.StatusBadRequest, message)
}

func NotFound(c *gin.Context, message string) {
	Error(c, http.StatusNotFound, message)
}
