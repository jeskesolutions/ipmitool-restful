package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"ipmitool-microservice/internal/api/response"
)

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"ipmitool_version"`
}

func (h *Handler) Health(c *gin.Context) {
	payload := healthResponse{Status: "ok", Version: h.runner.Version()}
	response.WriteJSON(c, http.StatusOK, payload)
}
