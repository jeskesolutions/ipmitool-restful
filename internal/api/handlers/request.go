package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"ipmitool-microservice/internal/api/response"
	"ipmitool-microservice/internal/ipmi"
)

const maxRequestBytes = 128 << 10

// Optional JSON scalars must distinguish omission from an explicit null or zero.
type field[T any] struct {
	Value   T
	Present bool
}

func (f *field[T]) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("null is not allowed")
	}
	if err := json.Unmarshal(data, &f.Value); err != nil {
		return err
	}
	f.Present = true
	return nil
}

type connectionRequest struct {
	Connection *connectionInput `json:"connection"`
}

type connectionInput struct {
	Host        string        `json:"host"`
	Username    string        `json:"username"`
	Password    *string       `json:"password"`
	Interface   field[string] `json:"interface"`
	Port        field[int]    `json:"port"`
	Privilege   field[string] `json:"privilege"`
	AuthType    field[string] `json:"auth_type"`
	CipherSuite field[int]    `json:"cipher_suite"`
}

func (input *connectionInput) connection() (ipmi.Connection, error) {
	if input == nil {
		return ipmi.Connection{}, errors.New("Provide connection with host, username, and password")
	}
	if input.Password == nil {
		return ipmi.Connection{}, errors.New("Provide connection.password; an empty password must be explicit")
	}
	for _, option := range []struct {
		name  string
		field field[string]
	}{
		{"interface", input.Interface},
		{"privilege", input.Privilege},
		{"auth_type", input.AuthType},
	} {
		if option.field.Present && option.field.Value == "" {
			return ipmi.Connection{}, errors.New("connection." + option.name + " must not be empty")
		}
	}
	if input.Port.Present && input.Port.Value == 0 {
		return ipmi.Connection{}, errors.New("connection.port must be between 1 and 65535")
	}
	var cipherSuite *int
	if input.CipherSuite.Present {
		cipherSuite = &input.CipherSuite.Value
	}
	connection := ipmi.Connection{
		Host:        input.Host,
		Username:    input.Username,
		Password:    *input.Password,
		Interface:   input.Interface.Value,
		Port:        input.Port.Value,
		Privilege:   input.Privilege.Value,
		AuthType:    input.AuthType.Value,
		CipherSuite: cipherSuite,
	}.WithDefaults()
	if err := connection.Validate(); err != nil {
		// Validation errors identify fields, never the supplied credential values.
		return ipmi.Connection{}, err
	}
	return connection, nil
}

func decodeRequest(c *gin.Context, destination any) bool {
	contentType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || contentType != "application/json" {
		response.WriteError(c, http.StatusUnsupportedMediaType, "unsupported_media_type", "Use Content-Type: application/json")
		return false
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(destination)
	if err == nil {
		var extra any
		if err = decoder.Decode(&extra); err == io.EOF {
			return true
		}
	}

	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		response.WriteError(c, http.StatusRequestEntityTooLarge, "body_too_large", "JSON exceeds 128 KiB")
	} else {
		response.WriteError(c, http.StatusBadRequest, "invalid_json", "Provide one valid JSON object with the expected fields and no null values")
	}
	return false
}

func readChannel(c *gin.Context) (string, bool) {
	channel, err := strconv.Atoi(c.Param("channel"))
	if err != nil || channel < 0 || channel > 15 {
		response.WriteError(c, http.StatusBadRequest, "invalid_request", "Channel must be between 0 and 15")
		return "", false
	}
	return strconv.Itoa(channel), true
}
