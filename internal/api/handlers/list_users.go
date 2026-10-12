package handlers

import "github.com/gin-gonic/gin"

func (h *Handler) ListUsers(c *gin.Context) {
	var request connectionRequest
	if !decodeRequest(c, &request) {
		return
	}

	channel, ok := readChannel(c)
	if !ok {
		return
	}

	h.execute(c, request.Connection, []string{"user", "list", channel}, "")
}
