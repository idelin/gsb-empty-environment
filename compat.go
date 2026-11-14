package netsec

import (
	"net/http"
)

const (
	// MaxCallbackLen is an alias for DefaultMaxCallbackLength.
	MaxCallbackLen = DefaultMaxCallbackLength
	// ContentType and ScriptContentType are aliases for JSONPContentType.
	ContentType       = JSONPContentType
	ScriptContentType = JSONPContentType
)

// RenderJSONP returns a validated script body. When the first two arguments
// are an http.ResponseWriter and *http.Request it also writes the response.
func RenderJSONP(args ...any) ([]byte, error) {
	if len(args) >= 2 {
		if w, ok := args[0].(http.ResponseWriter); ok {
			if r, ok := args[1].(*http.Request); ok {
				h, data, callback, hasCallback := configAndArguments(args[2:])
				value, err := h.requestPayload(r, data...)
				if err != nil {
					writeJSONPError(w, http.StatusInternalServerError, "payload unavailable")
					return nil, err
				}
				return h.write(w, r, value, callback, hasCallback)
			}
		}
	}

	if len(args) == 0 {
		return nil, ErrMissingCallback
	}
	callback, ok := args[0].(string)
	if !ok {
		return nil, ErrInvalidCallback
	}
	var (
		data   any
		maxLen []int
	)
	if len(args) >= 2 {
		data = args[1]
	}
	if len(args) >= 3 {
		switch value := args[2].(type) {
		case int:
			maxLen = []int{value}
		case []int:
			maxLen = value
		}
	}
	return MarshalJSONP(callback, data, maxLen...)
}

// Render is an alias for RenderJSONP.
func Render(args ...any) ([]byte, error) {
	return RenderJSONP(args...)
}

// JSONP returns a validated callback(payload); script. When the first two
// arguments are an http.ResponseWriter and *http.Request, it writes the response
// and returns the body as well.
func JSONP(args ...any) (string, error) {
	if len(args) >= 2 {
		if w, ok := args[0].(http.ResponseWriter); ok {
			if r, ok := args[1].(*http.Request); ok {
				body, err := RenderJSONP(append([]any{w, r}, args[2:]...)...)
				return string(body), err
			}
		}
	}

	if len(args) == 0 {
		return "", ErrMissingCallback
	}
	callback, ok := args[0].(string)
	if !ok {
		return "", ErrInvalidCallback
	}

	var (
		data   any
		maxLen []int
	)
	if len(args) >= 2 {
		data = args[1]
	}
	if len(args) >= 3 {
		switch value := args[2].(type) {
		case int:
			maxLen = []int{value}
		case []int:
			maxLen = value
		}
	}
	return JSONPString(callback, data, maxLen...)
}

// ValidateCallbackName is an alias for ValidateCallback.
func ValidateCallbackName(name string, maxLen ...int) error {
	return ValidateCallback(name, maxLen...)
}

// CheckCallback is an alias for IsValidCallback.
func CheckCallback(name string, maxLen ...int) bool {
	return IsValidCallback(name, maxLen...)
}

// SanitizeCallback normalizes percent-encoding and returns the safe name.
func SanitizeCallback(raw string, maxLen ...int) (string, error) {
	return NormalizeCallback(raw, maxLen...)
}

// ValidateJSONPCallback is an alias for ValidateCallback.
func ValidateJSONPCallback(name string, maxLen ...int) error {
	return ValidateCallback(name, maxLen...)
}

// IsValidJSONPCallback is an alias for IsValidCallback.
func IsValidJSONPCallback(name string, maxLen ...int) bool {
	return IsValidCallback(name, maxLen...)
}
