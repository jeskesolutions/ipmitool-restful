package response

import "github.com/gin-gonic/gin"

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error Error `json:"error"`
}

func WriteJSON(c *gin.Context, status int, value any) {
	c.JSON(status, value)
}

func WriteError(c *gin.Context, status int, code, message string) {
	payload := errorResponse{
		Error: Error{Code: code, Message: message},
	}
	c.AbortWithStatusJSON(status, payload)
}
