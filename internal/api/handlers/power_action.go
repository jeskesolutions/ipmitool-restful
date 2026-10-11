package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"ipmitool-microservice/internal/api/response"
)

type powerActionRequest struct {
	connectionRequest
	Action string `json:"action"`
}

func (h *Handler) PowerAction(c *gin.Context) {
	var request powerActionRequest
	if !decodeRequest(c, &request) {
		return
	}
	switch request.Action {
	case "cycle", "reset", "diag", "soft":
		h.execute(c, request.Connection, []string{"chassis", "power", request.Action}, "")
	default:
		response.WriteError(c, http.StatusBadRequest, "invalid_request", "action must be cycle, reset, diag, or soft")
	}
}
