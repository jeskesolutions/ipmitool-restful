package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"ipmitool-microservice/internal/api/response"
)

func (h *Handler) Health(c *gin.Context) {
	response.WriteJSON(c, http.StatusOK, struct {
		Status  string `json:"status"`
		Version string `json:"ipmitool_version"`
	}{Status: "ok", Version: h.runner.Version()})
}
