package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"ipmitool-microservice/internal/api/response"
)

func Authenticate(token string) gin.HandlerFunc {
	expected := sha256.Sum256([]byte(token))
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		values := c.Request.Header.Values("Authorization")
		parts := strings.Fields(c.GetHeader("Authorization"))
		if token == "" || len(values) != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			unauthorized(c)
			return
		}
		actual := sha256.Sum256([]byte(parts[1]))
		if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			unauthorized(c)
			return
		}
		c.Next()
	}
}

func unauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", "Bearer")
	response.WriteError(c, http.StatusUnauthorized, "unauthorized", "Invalid or missing authorization token")
}
