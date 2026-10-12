package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"ipmitool-microservice/internal/api/response"
)

type resetMCRequest struct {
	connectionRequest
	Mode string `json:"mode"`
}

func (h *Handler) ResetMC(c *gin.Context) {
	var request resetMCRequest
	if !decodeRequest(c, &request) {
		return
	}

	if request.Mode != "warm" && request.Mode != "cold" {
		response.WriteError(c, http.StatusBadRequest, "invalid_request", "mode must be warm or cold")
		return
	}

	h.execute(c, request.Connection, []string{"mc", "reset", request.Mode}, "")
}
