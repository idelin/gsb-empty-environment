// Package jsonp is a small importable facade over the secure JSONP component
// in the parent netsec package. It keeps the import path convenient for
// callers that expect a dedicated jsonp package.
package jsonp

import (
	"net/http"

	base "example.org/netsec/q0303"
)

const (
	CallbackParam            = base.CallbackParam
	DefaultMaxCallbackLength = base.DefaultMaxCallbackLength
	MaxCallbackLength        = base.DefaultMaxCallbackLength
	MaxCallbackLen           = base.MaxCallbackLen
	CallbackCharset          = base.CallbackCharset
	JSONPContentType         = base.JSONPContentType
	ContentType              = base.ContentType
	ScriptContentType        = base.ScriptContentType
)

var (
	ErrMissingCallback       = base.ErrMissingCallback
	ErrInvalidCallback       = base.ErrInvalidCallback
	ErrCallbackTooLong       = base.ErrCallbackTooLong
	ErrReservedCallback      = base.ErrReservedCallback
	ErrNilRequest            = base.ErrNilRequest
	ErrMethodNotAllowed      = base.ErrMethodNotAllowed
	ErrSensitiveEndpoint     = base.ErrSensitiveEndpoint
	ErrCredentialsNotAllowed = base.ErrCredentialsNotAllowed
	ErrOriginNotAllowed      = base.ErrOriginNotAllowed

	CallbackPattern = base.CallbackPattern
)

type (
	Options       = base.Options
	Config        = base.Config
	JSONPOptions  = base.JSONPOptions
	JSONPConfig   = base.JSONPConfig
	Handler       = base.Handler
	JSONPHandler  = base.JSONPHandler
	Renderer      = base.Renderer
	JSONPRenderer = base.JSONPRenderer
)

func NormalizeCallback(raw string, maxLen ...int) (string, error) {
	return base.NormalizeCallback(raw, maxLen...)
}

func ValidateCallback(name string, maxLen ...int) error {
	return base.ValidateCallback(name, maxLen...)
}

func IsValidCallback(name string, maxLen ...int) bool {
	return base.IsValidCallback(name, maxLen...)
}

func ValidCallback(raw string, maxLen ...int) bool {
	return base.ValidCallback(raw, maxLen...)
}

func ValidateCallbackName(name string, maxLen ...int) error {
	return base.ValidateCallbackName(name, maxLen...)
}

func CheckCallback(name string, maxLen ...int) bool {
	return base.CheckCallback(name, maxLen...)
}

func SanitizeCallback(raw string, maxLen ...int) (string, error) {
	return base.SanitizeCallback(raw, maxLen...)
}

func ValidateJSONPCallback(name string, maxLen ...int) error {
	return base.ValidateJSONPCallback(name, maxLen...)
}

func IsValidJSONPCallback(name string, maxLen ...int) bool {
	return base.IsValidJSONPCallback(name, maxLen...)
}

func MarshalJSONP(callback string, data any, maxLen ...int) ([]byte, error) {
	return base.MarshalJSONP(callback, data, maxLen...)
}

func WrapJSONP(callback string, data any, maxLen ...int) ([]byte, error) {
	return base.WrapJSONP(callback, data, maxLen...)
}

func Marshal(callback string, data any, maxLen ...int) ([]byte, error) {
	return base.MarshalJSONP(callback, data, maxLen...)
}

func Wrap(callback string, data any, maxLen ...int) ([]byte, error) {
	return base.WrapJSONP(callback, data, maxLen...)
}

func JSONPString(callback string, data any, maxLen ...int) (string, error) {
	return base.JSONPString(callback, data, maxLen...)
}

func NewHandler(args ...any) *Handler {
	return base.NewHandler(args...)
}

func New(args ...any) *Handler {
	return base.NewHandler(args...)
}

func NewJSONPHandler(args ...any) *Handler {
	return base.NewHandler(args...)
}

func NewRenderer(args ...any) *Handler {
	return base.NewHandler(args...)
}

func NewJSONPRenderer(args ...any) *Handler {
	return base.NewHandler(args...)
}

func NewHandlerWithPayload(payload any, args ...any) *Handler {
	return base.NewHandlerWithPayload(payload, args...)
}

func WriteJSONP(w http.ResponseWriter, r *http.Request, args ...any) error {
	return base.WriteJSONP(w, r, args...)
}

func ServeJSONP(w http.ResponseWriter, r *http.Request, args ...any) error {
	return base.ServeJSONP(w, r, args...)
}

func SafeJSONP(w http.ResponseWriter, r *http.Request, args ...any) error {
	return base.SafeJSONP(w, r, args...)
}

func WriteSafeJSONP(w http.ResponseWriter, r *http.Request, args ...any) error {
	return base.WriteSafeJSONP(w, r, args...)
}

func RespondJSONP(w http.ResponseWriter, r *http.Request, args ...any) error {
	return base.RespondJSONP(w, r, args...)
}

func RenderJSONP(args ...any) ([]byte, error) {
	return base.RenderJSONP(args...)
}

func Render(args ...any) ([]byte, error) {
	return base.Render(args...)
}

func JSONP(args ...any) (string, error) {
	return base.JSONP(args...)
}
