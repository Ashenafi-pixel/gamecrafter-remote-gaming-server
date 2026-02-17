package server

import (
	"encoding/json"
	"net/http"
)

// APIError is the standard error response for RGS APIs.
type APIError struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func writeError(w http.ResponseWriter, code int, errMsg, codeStr string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(APIError{
		Error:   errMsg,
		Code:    codeStr,
		Message: errMsg,
	})
}
