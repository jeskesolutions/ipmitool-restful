package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"ipmitool-microservice/internal/api/response"
	"ipmitool-microservice/internal/ipmi"
)

type Runner interface {
	RunWithInput(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error)
	Version() string
}

type Handler struct {
	runner Runner
	slots  chan struct{}
}

func New(runner Runner, maxConcurrent int) *Handler {
	return &Handler{runner: runner, slots: make(chan struct{}, maxConcurrent)}
}

func (h *Handler) execute(c *gin.Context, input *connectionInput, args []string, stdin string) {
	connection, err := input.connection()
	if err != nil {
		response.WriteError(c, http.StatusBadRequest, "invalid_connection", err.Error())
		return
	}
	if err := ipmi.ValidateCommand(args, stdin); err != nil {
		response.WriteError(c, http.StatusBadRequest, "invalid_command", err.Error())
		return
	}

	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		c.Header("Retry-After", "1")
		response.WriteError(c, http.StatusServiceUnavailable, "busy", "Concurrent command limit reached")
		return
	}

	result, err := h.runner.RunWithInput(c.Request.Context(), connection, args, stdin)
	if err == nil && result.ExitCode != 0 || errors.Is(err, ipmi.ErrCommandFailed) && (result.ExitCode == 0 || result.ExitCode < -1) {
		response.WriteError(c, http.StatusInternalServerError, "invalid_runner_response", "Runner returned an inconsistent exit status")
		return
	}
	if err != nil {
		writeExecutionError(c, result, err)
		return
	}
	response.WriteJSON(c, http.StatusOK, result)
}

func writeExecutionError(c *gin.Context, result ipmi.Result, err error) {
	status := http.StatusInternalServerError
	detail := response.Error{Code: "execution_failed", Message: "Unable to execute ipmitool"}

	switch {
	case errors.Is(err, ipmi.ErrInvalidCommand):
		status = http.StatusBadRequest
		detail = response.Error{Code: "invalid_command", Message: "Invalid command or arguments"}
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
		detail = response.Error{Code: "timeout", Message: "Command timed out"}
	case errors.Is(err, context.Canceled):
		status = http.StatusRequestTimeout
		detail = response.Error{Code: "canceled", Message: "Request canceled"}
	case errors.Is(err, ipmi.ErrCommandFailed):
		status = http.StatusBadGateway
		detail = response.Error{Code: "command_failed", Message: "ipmitool returned an error"}
	}

	response.WriteJSON(c, status, struct {
		Error  response.Error `json:"error"`
		Result ipmi.Result    `json:"result"`
	}{Error: detail, Result: result})
}
