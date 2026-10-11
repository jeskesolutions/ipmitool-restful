package middleware

import (
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"ipmitool-microservice/internal/api/response"
)

func LogRequests(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unknown"
		}
		clientIP := ""
		if address, ok := peerAddress(c.Request); ok {
			clientIP = address.String()
		}
		logger.Info("HTTP request",
			zap.String("method", c.Request.Method),
			zap.String("route", route),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("duration", time.Since(started)),
			zap.String("client_ip", clientIP),
		)
	}
}

func Recover(logger *zap.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
		logger.Error("HTTP request panic recovered")
		response.WriteError(c, http.StatusInternalServerError, "internal_error", "Internal server error")
	})
}
