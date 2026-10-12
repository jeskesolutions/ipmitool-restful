package response_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"ipmitool-microservice/internal/api/response"
)

func TestWriteErrorPreservesMessageAndCode(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	response.WriteError(c, http.StatusBadRequest, "UnchangedCode", "invalid IP address: BMC.EXAMPLE")

	var envelope struct {
		Error response.Error `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}

	if recorder.Code != http.StatusBadRequest || !c.IsAborted() || envelope.Error.Code != "UnchangedCode" || envelope.Error.Message != "invalid IP address: BMC.EXAMPLE" {
		t.Fatalf("unexpected error response: status = %d, aborted = %v, body = %s", recorder.Code, c.IsAborted(), recorder.Body.String())
	}
}
