package handlers

import "github.com/gin-gonic/gin"

func (h *Handler) ListSEL(c *gin.Context) {
	var request connectionRequest
	if !decodeRequest(c, &request) {
		return
	}
	h.execute(c, request.Connection, []string{"sel", "list"}, "")
}
