package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"ipmitool-microservice/internal/api/response"
)

type bootDeviceRequest struct {
	connectionRequest
	Device     string      `json:"device"`
	Persistent field[bool] `json:"persistent"`
}

func (h *Handler) SetBootDevice(c *gin.Context) {
	var request bootDeviceRequest
	if !decodeRequest(c, &request) {
		return
	}

	switch request.Device {
	case "none", "pxe", "disk", "safe", "diag", "cdrom", "bios", "floppy":
		args := []string{"chassis", "bootdev", request.Device}
		if request.Persistent.Value {
			args = append(args, "options=persistent")
		}

		h.execute(c, request.Connection, args, "")
	default:
		response.WriteError(c, http.StatusBadRequest, "invalid_request", "invalid boot device")
	}
}
