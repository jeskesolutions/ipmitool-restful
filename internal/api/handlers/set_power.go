package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"ipmitool-microservice/internal/api/response"
)

type powerRequest struct {
	connectionRequest
	State string `json:"state"`
}

func (h *Handler) SetPower(c *gin.Context) {
	var request powerRequest
	if !decodeRequest(c, &request) {
		return
	}
	if request.State != "on" && request.State != "off" {
		response.WriteError(c, http.StatusBadRequest, "invalid_request", "state must be on or off")
		return
	}
	h.execute(c, request.Connection, []string{"chassis", "power", request.State}, "")
}
