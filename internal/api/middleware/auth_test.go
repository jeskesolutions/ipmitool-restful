package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"ipmitool-microservice/internal/api/middleware"
)

const token = "test-fixed-token"

func TestAuthenticate(t *testing.T) {
	for _, tc := range []struct {
		name, configured, path string
		headers                []string
		want                   int
	}{
		{name: "valid", configured: token, path: "/", headers: []string{"Bearer " + token}, want: http.StatusNoContent},
		{name: "missing", configured: token, path: "/", want: http.StatusUnauthorized},
		{name: "incorrect", configured: token, path: "/", headers: []string{"Bearer wrong"}, want: http.StatusUnauthorized},
		{name: "wrong scheme", configured: token, path: "/", headers: []string{"Basic " + token}, want: http.StatusUnauthorized},
		{name: "empty bearer", configured: token, path: "/", headers: []string{"Bearer "}, want: http.StatusUnauthorized},
		{name: "extra fields", configured: token, path: "/", headers: []string{"Bearer " + token + " extra"}, want: http.StatusUnauthorized},
		{name: "duplicate headers", configured: token, path: "/", headers: []string{"Bearer " + token, "Bearer " + token}, want: http.StatusUnauthorized},
		{name: "query token", configured: token, path: "/?token=" + token, want: http.StatusUnauthorized},
		{name: "empty configured token", path: "/", headers: []string{"Bearer "}, want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := gin.New()
			handler.Use(middleware.Authenticate(tc.configured))
			handler.GET("/", func(c *gin.Context) {
				called = true
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			for _, header := range tc.headers {
				req.Header.Add("Authorization", header)
			}

			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", res.Code, tc.want, res.Body.String())
			}

			if called != (tc.want == http.StatusNoContent) {
				t.Fatalf("protected handler called = %v", called)
			}
		})
	}
}
