package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"ipmitool-microservice/internal/api/handlers"
	"ipmitool-microservice/internal/api/middleware"
	"ipmitool-microservice/internal/api/response"
	"ipmitool-microservice/internal/config"
)

func New(cfg config.Config, runner handlers.Runner, logger *zap.Logger) http.Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	handler := handlers.New(runner, cfg.IPMITool.MaxConcurrent)
	router := gin.New()
	router.RedirectTrailingSlash = false
	router.RedirectFixedPath = false
	router.HandleMethodNotAllowed = true
	router.Use(middleware.LogRequests(logger), middleware.Recover(logger), middleware.Authenticate(cfg.Auth.Token), middleware.AllowIPs(cfg.Server.AllowedIPs))
	router.NoRoute(notFound)
	router.NoMethod(methodNotAllowed)

	routes := router.Group("/api/v1")
	routes.GET("/health", handler.Health)
	routes.POST("/commands", handler.RunCommand)

	routes.POST("/power/status", handler.PowerStatus)
	routes.PUT("/power", handler.SetPower)
	routes.POST("/power/actions", handler.PowerAction)
	routes.POST("/chassis/status", handler.ChassisStatus)
	routes.PUT("/boot-device", handler.SetBootDevice)

	routes.POST("/sensors", handler.ListSensors)
	routes.POST("/sdr", handler.ListSDR)
	routes.POST("/fru", handler.ReadFRU)
	routes.POST("/sel/entries", handler.ListSEL)
	routes.POST("/sel/clear", handler.ClearSEL)

	routes.POST("/mc/info", handler.MCInfo)
	routes.POST("/mc/resets", handler.ResetMC)
	routes.POST("/lan/:channel", handler.LANInfo)
	routes.POST("/users/:channel", handler.ListUsers)

	return router
}

func notFound(c *gin.Context) {
	response.WriteError(c, http.StatusNotFound, "not_found", "Route not found")
}

func methodNotAllowed(c *gin.Context) {
	response.WriteError(c, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
}
