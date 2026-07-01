// Package httpx contains small HTTP helpers: JSON encoding, a typed API error,
// and middleware. Kept dependency-free on purpose.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// APIError is a client-facing error carrying an HTTP status.
type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Message }

func Errorf(status int, code, msg string) *APIError {
	return &APIError{Status: status, Code: code, Message: msg}
}

var (
	ErrUnauthorized = Errorf(http.StatusUnauthorized, "unauthorized", "authentication required")
	ErrForbidden    = Errorf(http.StatusForbidden, "forbidden", "you do not have access to this resource")
	ErrNotFound     = Errorf(http.StatusNotFound, "not_found", "resource not found")
)

// JSON writes v as JSON with the given status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			slog.Error("encode response", "err", err)
		}
	}
}

// Fail writes an error response. Unknown errors become 500 without leaking
// internals to the client.
func Fail(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		JSON(w, apiErr.Status, map[string]any{"error": apiErr})
		return
	}
	slog.Error("internal error", "err", err)
	JSON(w, http.StatusInternalServerError, map[string]any{
		"error": map[string]string{"code": "internal", "message": "internal server error"},
	})
}

// DecodeJSON reads a JSON body into dst, rejecting unknown fields.
func DecodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 5<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return Errorf(http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
	}
	return nil
}
