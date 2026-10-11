package handlers

import "github.com/gin-gonic/gin"

type commandRequest struct {
	connectionRequest
	Args  []field[string] `json:"args"`
	Stdin field[string]   `json:"stdin"`
}

func (h *Handler) RunCommand(c *gin.Context) {
	var request commandRequest
	if !decodeRequest(c, &request) {
		return
	}
	args := make([]string, len(request.Args))
	for i, argument := range request.Args {
		args[i] = argument.Value
	}
	h.execute(c, request.Connection, args, request.Stdin.Value)
}
