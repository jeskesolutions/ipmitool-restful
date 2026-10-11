package handlers

import "github.com/gin-gonic/gin"

func (h *Handler) ListSDR(c *gin.Context) {
	var request connectionRequest
	if !decodeRequest(c, &request) {
		return
	}
	h.execute(c, request.Connection, []string{"sdr", "list"}, "")
}
