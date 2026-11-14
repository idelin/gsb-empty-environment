package netsec

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

var (
	// ErrNilRequest reports a nil *http.Request.
	ErrNilRequest = errors.New("netsec: nil request")
	// ErrMethodNotAllowed reports a non-GET/HEAD JSONP request.
	ErrMethodNotAllowed = errors.New("netsec: method not allowed")
	// ErrSensitiveEndpoint reports JSONP disabled for a sensitive endpoint.
	ErrSensitiveEndpoint = errors.New("netsec: JSONP disabled for sensitive endpoint")
	// ErrCredentialsNotAllowed reports a credential-bearing script request.
	ErrCredentialsNotAllowed = errors.New("netsec: credentials are not accepted")
	// ErrOriginNotAllowed reports a same-origin/allow-list failure.
	ErrOriginNotAllowed = errors.New("netsec: origin is not allowed")
)

// Options configures a Handler. The zero value is safe: same-origin requests
// are allowed, cross-origin requests are allowed only when the exact origin
// appears in one of the allow-list fields, credentials are rejected, and
// missing origin information is rejected.
type Options struct {
	// AllowedOrigins is an allow-list of exact origins such as
	// "https://trusted.example.com" or host-only entries such as
	// "trusted.example.com".
	AllowedOrigins []string

	// AllowedOrigins is the primary field; these aliases make configuration
	// convenient for callers that already use another name.
	AllowedHosts     []string
	Whitelist        []string
	WhitelistOrigins []string
	TrustedOrigins   []string
	AllowedReferers  []string
	AllowedOrigin    string

	// MaxCallbackLength overrides DefaultMaxCallbackLength when non-zero.
	MaxCallbackLength int
	MaxCallbackLen    int
	MaxLen            int

	// CallbackParam overrides CallbackParam when non-empty.
	CallbackParam string
	// CallbackQueryParam is an alias for CallbackParam.
	CallbackQueryParam string
	CallbackParameter  string
	CallbackQuery      string

	// Sensitive disables JSONP completely for a sensitive endpoint.
	Sensitive bool
	// DisableJSONP is an alias for Sensitive.
	DisableJSONP bool
	// SensitivePaths disables JSONP for request paths with these prefixes.
	SensitivePaths        []string
	SensitiveEndpoints    []string
	SensitivePathPrefixes []string
	// SensitiveFunc allows per-request classification of sensitive endpoints.
	SensitiveFunc func(*http.Request) bool

	// AllowCredentials must remain false for script endpoints. When false,
	// requests carrying Cookie, Authorization, or Proxy-Authorization are
	// rejected and no Set-Cookie header is emitted.
	AllowCredentials bool

	// AllowMissingOrigin permits non-browser clients that send neither Origin
	// nor Referer. It is false by default so a stripped-referrer cross-origin
	// script request cannot fall through the origin check.
	AllowMissingOrigin bool
	AllowNoOrigin      bool
	// RequireOrigin and AllowSameOrigin are compatibility fields. Origin
	// information is required by default and same-origin is allowed by
	// default; use AllowMissingOrigin or DenySameOrigin to change that.
	RequireOrigin   bool
	AllowSameOrigin bool

	// DenySameOrigin disables the implicit same-origin allowance.
	DenySameOrigin bool

	// Payload / Data are the default values returned when a request does not
	// supply an explicit payload to Write/Serve.
	Payload any
	Data    any

	// PayloadFunc / Resolver compute a payload from the request.
	PayloadFunc func(*http.Request) (any, error)
	Resolver    func(*http.Request) (any, error)
	DataFunc    func(*http.Request) any

	// MarshalFunc replaces encoding/json.Marshal for the payload. Its output
	// is still validated and HTML-escaped before being wrapped.
	MarshalFunc func(any) ([]byte, error)
}

// Config, JSONPOptions, and JSONPConfig are aliases for Options.
type Config = Options
type JSONPOptions = Options
type JSONPConfig = Options

// Handler is an importable JSONP response component. It implements
// http.Handler for callers that store a payload or resolver in Options, and
// also exposes Write/Serve for callers that pass a payload per request.
type Handler struct {
	Options

	payload    any
	hasPayload bool
	resolver   func(*http.Request) (any, error)

	initOnce sync.Once

	allowedOrigins map[string]struct{}
	allowedHosts   map[string]struct{}
	subdomainRules map[string]string

	maxCallback   int
	callbackParam string
}

// JSONPHandler is an alias for Handler.
type JSONPHandler = Handler

// Renderer and JSONPRenderer are aliases for Handler.
type Renderer = Handler
type JSONPRenderer = Handler

// NewHandler builds a Handler from zero or more Options, *Options, payloads,
// or payload resolver functions. This accepts both common construction styles:
//
//	NewHandler(Options{AllowedOrigins: []string{"https://trusted.example"}})
//	NewHandler(payload, Options{...})
func NewHandler(args ...any) *Handler {
	h := &Handler{}
	for _, arg := range args {
		switch value := arg.(type) {
		case Options:
			h.applyOptions(value)
		case *Options:
			if value != nil {
				h.applyOptions(*value)
			}
		case func(*http.Request) (any, error):
			h.resolver = value
		default:
			h.payload = value
			h.hasPayload = true
		}
	}
	h.init()
	return h
}

// New and NewJSONPHandler are aliases for NewHandler.
func New(args ...any) *Handler             { return NewHandler(args...) }
func NewJSONPHandler(args ...any) *Handler { return NewHandler(args...) }
func NewRenderer(args ...any) *Handler     { return NewHandler(args...) }
func NewJSONPRenderer(args ...any) *Handler {
	return NewHandler(args...)
}

// NewHandlerWithPayload constructs a handler with a fixed payload.
func NewHandlerWithPayload(payload any, args ...any) *Handler {
	return NewHandler(append([]any{payload}, args...)...)
}

func (h *Handler) applyOptions(o Options) {
	if len(o.AllowedOrigins) > 0 {
		h.AllowedOrigins = append(h.AllowedOrigins, o.AllowedOrigins...)
	}
	if len(o.AllowedHosts) > 0 {
		h.AllowedHosts = append(h.AllowedHosts, o.AllowedHosts...)
	}
	if len(o.Whitelist) > 0 {
		h.Whitelist = append(h.Whitelist, o.Whitelist...)
	}
	if len(o.WhitelistOrigins) > 0 {
		h.WhitelistOrigins = append(h.WhitelistOrigins, o.WhitelistOrigins...)
	}
	if len(o.TrustedOrigins) > 0 {
		h.TrustedOrigins = append(h.TrustedOrigins, o.TrustedOrigins...)
	}
	if len(o.AllowedReferers) > 0 {
		h.AllowedReferers = append(h.AllowedReferers, o.AllowedReferers...)
	}
	if o.AllowedOrigin != "" {
		h.AllowedOrigins = append(h.AllowedOrigins, o.AllowedOrigin)
	}
	if o.MaxCallbackLength > 0 {
		h.MaxCallbackLength = o.MaxCallbackLength
	}
	if o.MaxCallbackLen > 0 {
		h.MaxCallbackLen = o.MaxCallbackLen
	}
	if o.MaxLen > 0 {
		h.MaxLen = o.MaxLen
	}
	if o.CallbackParam != "" {
		h.CallbackParam = o.CallbackParam
	}
	if o.CallbackQueryParam != "" {
		h.CallbackQueryParam = o.CallbackQueryParam
	}
	if o.CallbackParameter != "" {
		h.CallbackParameter = o.CallbackParameter
	}
	if o.CallbackQuery != "" {
		h.CallbackQuery = o.CallbackQuery
	}
	h.Sensitive = h.Sensitive || o.Sensitive
	h.DisableJSONP = h.DisableJSONP || o.DisableJSONP
	if len(o.SensitivePaths) > 0 {
		h.SensitivePaths = append(h.SensitivePaths, o.SensitivePaths...)
	}
	if len(o.SensitiveEndpoints) > 0 {
		h.SensitiveEndpoints = append(h.SensitiveEndpoints, o.SensitiveEndpoints...)
	}
	if len(o.SensitivePathPrefixes) > 0 {
		h.SensitivePathPrefixes = append(h.SensitivePathPrefixes, o.SensitivePathPrefixes...)
	}
	if o.SensitiveFunc != nil {
		h.SensitiveFunc = o.SensitiveFunc
	}
	h.AllowCredentials = h.AllowCredentials || o.AllowCredentials
	h.AllowMissingOrigin = h.AllowMissingOrigin || o.AllowMissingOrigin
	h.AllowMissingOrigin = h.AllowMissingOrigin || o.AllowNoOrigin
	h.RequireOrigin = h.RequireOrigin || o.RequireOrigin
	h.AllowSameOrigin = h.AllowSameOrigin || o.AllowSameOrigin
	h.DenySameOrigin = h.DenySameOrigin || o.DenySameOrigin
	if o.Payload != nil {
		h.Payload = o.Payload
	}
	if o.Data != nil {
		h.Data = o.Data
	}
	if o.PayloadFunc != nil {
		h.PayloadFunc = o.PayloadFunc
	}
	if o.Resolver != nil {
		h.Resolver = o.Resolver
	}
	if o.DataFunc != nil {
		h.DataFunc = o.DataFunc
	}
	if o.MarshalFunc != nil {
		h.MarshalFunc = o.MarshalFunc
	}
}

func (h *Handler) init() {
	h.initOnce.Do(func() {
		maxCallback := h.MaxCallbackLength
		if maxCallback == 0 {
			maxCallback = h.MaxCallbackLen
		}
		if maxCallback == 0 {
			maxCallback = h.MaxLen
		}
		h.maxCallback = callbackLengthLimit(maxCallback)

		h.callbackParam = h.CallbackParam
		if h.callbackParam == "" {
			h.callbackParam = h.CallbackQueryParam
		}
		if h.callbackParam == "" {
			h.callbackParam = h.CallbackParameter
		}
		if h.callbackParam == "" {
			h.callbackParam = h.CallbackQuery
		}
		if h.callbackParam == "" {
			h.callbackParam = CallbackParam
		}

		h.allowedOrigins = make(map[string]struct{})
		h.allowedHosts = make(map[string]struct{})
		h.subdomainRules = make(map[string]string)
		var entries []string
		entries = append(entries, h.AllowedOrigins...)
		entries = append(entries, h.AllowedHosts...)
		entries = append(entries, h.Whitelist...)
		entries = append(entries, h.WhitelistOrigins...)
		entries = append(entries, h.TrustedOrigins...)
		entries = append(entries, h.AllowedReferers...)
		if h.AllowedOrigin != "" {
			entries = append(entries, h.AllowedOrigin)
		}
		for _, entry := range entries {
			h.addAllowedOrigin(entry)
		}

		if h.Payload != nil {
			h.payload = h.Payload
			h.hasPayload = true
		}
		if h.Data != nil {
			h.payload = h.Data
			h.hasPayload = true
		}
		if h.Resolver != nil {
			h.resolver = h.Resolver
		}
		if h.PayloadFunc != nil {
			h.resolver = h.PayloadFunc
		}
		if h.DataFunc != nil {
			dataFunc := h.DataFunc
			h.resolver = func(r *http.Request) (any, error) {
				return dataFunc(r), nil
			}
		}
	})
}

func (h *Handler) addAllowedOrigin(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}

	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "https://*.") || strings.HasPrefix(lower, "http://*.") {
		scheme, suffix, ok := splitSubdomainRule(lower)
		if !ok {
			return
		}
		h.subdomainRules[scheme] = suffix
		return
	}

	if strings.Contains(raw, "://") {
		if origin, ok := normalizeOrigin(raw); ok {
			h.allowedOrigins[origin.name] = struct{}{}
			return
		}
		// An entry with a scheme but no valid origin is a configuration
		// error. Ignoring it is fail-closed.
		return
	}

	host := strings.ToLower(strings.TrimSuffix(raw, "/"))
	if host == "" {
		return
	}
	h.allowedHosts[host] = struct{}{}
}

func splitSubdomainRule(rule string) (scheme, suffix string, ok bool) {
	parts := strings.SplitN(rule, "://*.", 2)
	if len(parts) != 2 || parts[0] != "http" && parts[0] != "https" {
		return "", "", false
	}
	suffix = strings.TrimSuffix(strings.ToLower(parts[1]), "/")
	if suffix == "" {
		return "", "", false
	}
	return parts[0], suffix, true
}

// ServeHTTP satisfies http.Handler using the payload configured in Options.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	value, err := h.requestPayload(r)
	if err != nil {
		writeJSONPError(w, http.StatusInternalServerError, "payload unavailable")
		return
	}
	_, _ = h.write(w, r, value, "", false)
}

// Write emits a JSONP response using data. Any Options or resolver supplied
// here augment the handler's configuration for this request.
func (h *Handler) Write(w http.ResponseWriter, r *http.Request, data ...any) error {
	value, err := h.requestPayload(r, data...)
	if err != nil {
		writeJSONPError(w, http.StatusInternalServerError, "payload unavailable")
		return err
	}
	_, err = h.write(w, r, value, "", false)
	return err
}

// WriteJSONP is an alias for Write.
func (h *Handler) WriteJSONP(w http.ResponseWriter, r *http.Request, data ...any) error {
	return h.Write(w, r, data...)
}

// Serve is an alias for Write.
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request, data ...any) error {
	return h.Write(w, r, data...)
}

// ServeJSONP is an alias for Write.
func (h *Handler) ServeJSONP(w http.ResponseWriter, r *http.Request, data ...any) error {
	return h.Write(w, r, data...)
}

// Render writes the response and returns the exact script body.
func (h *Handler) Render(w http.ResponseWriter, r *http.Request, data ...any) ([]byte, error) {
	value, err := h.requestPayload(r, data...)
	if err != nil {
		writeJSONPError(w, http.StatusInternalServerError, "payload unavailable")
		return nil, err
	}
	return h.write(w, r, value, "", false)
}

func (h *Handler) requestPayload(r *http.Request, data ...any) (any, error) {
	h.init()
	if len(data) > 0 {
		return data[0], nil
	}
	if h.resolver != nil {
		value, err := h.resolver(r)
		if err != nil {
			return nil, err
		}
		return value, nil
	}
	if h.hasPayload {
		return h.payload, nil
	}
	return nil, nil
}

func (h *Handler) write(
	w http.ResponseWriter,
	r *http.Request,
	data any,
	callbackOverride string,
	hasCallbackOverride bool,
) ([]byte, error) {
	h.init()

	if r == nil {
		writeJSONPError(w, http.StatusBadRequest, "bad request")
		return nil, ErrNilRequest
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeJSONPError(w, http.StatusMethodNotAllowed, "method not allowed")
		return nil, ErrMethodNotAllowed
	}

	if h.isSensitive(r) {
		writeJSONPError(w, http.StatusForbidden, "JSONP is disabled for this endpoint")
		return nil, ErrSensitiveEndpoint
	}

	var (
		name string
		err  error
	)
	if hasCallbackOverride {
		name, err = NormalizeCallback(callbackOverride, h.maxCallback)
	} else {
		name, err = h.callbackFromRequest(r)
	}
	if err != nil {
		writeJSONPError(w, http.StatusBadRequest, "invalid callback")
		return nil, err
	}

	if !h.AllowCredentials && requestHasCredentials(r) {
		writeJSONPError(w, http.StatusForbidden, "credentials are not accepted")
		return nil, ErrCredentialsNotAllowed
	}

	if !h.sourceAllowed(r) {
		writeJSONPError(w, http.StatusForbidden, "origin is not allowed")
		return nil, ErrOriginNotAllowed
	}

	payload, err := marshalWith(h.MarshalFunc, data)
	if err != nil {
		writeJSONPError(w, http.StatusInternalServerError, "payload unavailable")
		return nil, err
	}
	body := wrapJSONPayload(name, payload)

	header := w.Header()
	header.Set("Content-Type", JSONPContentType)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Add("Vary", "Origin")
	header.Add("Vary", "Referer")
	header.Del("Access-Control-Allow-Credentials")
	header.Del("Set-Cookie")
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Length", strconv.Itoa(len(body)))

	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
	return body, nil
}

func (h *Handler) callbackFromRequest(r *http.Request) (string, error) {
	if r.URL == nil {
		return "", ErrMissingCallback
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", ErrInvalidCallback
	}
	values := query[h.callbackParam]
	switch len(values) {
	case 0:
		return "", ErrMissingCallback
	case 1:
		if values[0] == "" {
			return "", ErrMissingCallback
		}
		return NormalizeCallback(values[0], h.maxCallback)
	default:
		// Ambiguous duplicate parameters can be interpreted differently by
		// proxies and this process. Reject rather than pick one.
		return "", ErrInvalidCallback
	}
}

func (h *Handler) isSensitive(r *http.Request) bool {
	if h.Sensitive || h.DisableJSONP {
		return true
	}
	if h.SensitiveFunc != nil && h.SensitiveFunc(r) {
		return true
	}
	if r.URL != nil {
		prefixes := make([]string, 0,
			len(h.SensitivePaths)+len(h.SensitiveEndpoints)+len(h.SensitivePathPrefixes))
		prefixes = append(prefixes, h.SensitivePaths...)
		prefixes = append(prefixes, h.SensitiveEndpoints...)
		prefixes = append(prefixes, h.SensitivePathPrefixes...)
		for _, prefix := range prefixes {
			if prefix != "" && strings.HasPrefix(r.URL.Path, prefix) {
				return true
			}
		}
	}
	return false
}

func requestHasCredentials(r *http.Request) bool {
	if r.URL != nil && r.URL.User != nil {
		return true
	}
	return r.Header.Get("Cookie") != "" ||
		r.Header.Get("Authorization") != "" ||
		r.Header.Get("Proxy-Authorization") != ""
}

func (h *Handler) sourceAllowed(r *http.Request) bool {
	origins := nonEmptyHeaderValues(r.Header.Values("Origin"))
	referers := nonEmptyHeaderValues(r.Header.Values("Referer"))

	if len(origins) > 0 {
		if len(origins) != 1 || !h.originHeaderAllowed(origins[0], r) {
			return false
		}
	}
	if len(referers) > 0 {
		if len(referers) != 1 || !h.refererAllowed(referers[0], r) {
			return false
		}
	}

	if len(origins) == 0 && len(referers) == 0 {
		// Modern browsers set Sec-Fetch-Site. A same-origin value is an
		// explicit same-origin assertion, so accept it even when the browser
		// stripped both Origin and Referer. Anything cross-site is rejected.
		site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
		switch site {
		case "same-origin", "none":
			return true
		case "":
			return h.AllowMissingOrigin
		default:
			return false
		}
	}
	return true
}

func nonEmptyHeaderValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func (h *Handler) originHeaderAllowed(raw string, r *http.Request) bool {
	origin, ok := normalizeOrigin(raw)
	if !ok {
		return false
	}
	return h.originAllowed(origin, r)
}

func (h *Handler) refererAllowed(raw string, r *http.Request) bool {
	origin, ok := originFromReferer(raw)
	if !ok {
		return false
	}
	return h.originAllowed(origin, r)
}

func (h *Handler) originAllowed(origin originValue, r *http.Request) bool {
	if !h.DenySameOrigin {
		same := requestOrigin(r)
		if same.host != "" && origin.name == same.name {
			return true
		}
	}

	if _, ok := h.allowedOrigins[origin.name]; ok {
		return true
	}

	hostOnly := origin.host
	if hostName, ok := hostOnlyName(hostOnly); ok {
		hostOnly = hostName
	}
	if _, ok := h.allowedHosts[origin.host]; ok {
		return true
	}
	if _, ok := h.allowedHosts[hostOnly]; ok {
		return true
	}

	if suffix, ok := h.subdomainRules[origin.scheme]; ok {
		hostName, nameOK := hostOnlyName(origin.host)
		if nameOK && strings.HasSuffix(hostName, suffix) &&
			len(hostName) > len(suffix) && hostName[len(hostName)-len(suffix)-1] == '.' {
			return true
		}
	}
	return false
}

func hostOnlyName(host string) (string, bool) {
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		// Preserve IPv6 literals (for example [::1]) as a single host.
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			return host, true
		}
		if !strings.Contains(host[i+1:], "]") {
			return host[:i], true
		}
	}
	return host, true
}

func wrapJSONPayload(name string, payload []byte) []byte {
	payload = escapeJSONForScript(payload)
	body := make([]byte, 0, len(name)+len(payload)+3)
	body = append(body, name...)
	body = append(body, '(')
	body = append(body, payload...)
	body = append(body, ')', ';')
	return body
}

func writeJSONPError(w http.ResponseWriter, status int, message string) {
	header := w.Header()
	header.Del("Set-Cookie")
	header.Del("Access-Control-Allow-Credentials")
	header.Del("Access-Control-Allow-Origin")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Cache-Control", "no-store")
	http.Error(w, message, status)
}

// WriteJSONP writes a JSONP response using the callback query parameter and
// optional Options/payload arguments.
func WriteJSONP(w http.ResponseWriter, r *http.Request, args ...any) error {
	h, data, callback, hasCallback := configAndArguments(args)
	value, err := h.requestPayload(r, data...)
	if err != nil {
		writeJSONPError(w, http.StatusInternalServerError, "payload unavailable")
		return err
	}
	_, err = h.write(w, r, value, callback, hasCallback)
	return err
}

// ServeJSONP is an alias for WriteJSONP.
func ServeJSONP(w http.ResponseWriter, r *http.Request, args ...any) error {
	return WriteJSONP(w, r, args...)
}

// SafeJSONP is an alias for WriteJSONP.
func SafeJSONP(w http.ResponseWriter, r *http.Request, args ...any) error {
	return WriteJSONP(w, r, args...)
}

// WriteSafeJSONP is an alias for WriteJSONP.
func WriteSafeJSONP(w http.ResponseWriter, r *http.Request, args ...any) error {
	return WriteJSONP(w, r, args...)
}

// RespondJSONP is an alias for WriteJSONP.
func RespondJSONP(w http.ResponseWriter, r *http.Request, args ...any) error {
	return WriteJSONP(w, r, args...)
}

func configAndArguments(args []any) (*Handler, []any, string, bool) {
	var (
		options     []any
		data        []any
		callback    string
		hasCallback bool
	)
	for i, arg := range args {
		switch value := arg.(type) {
		case Options:
			options = append(options, value)
		case *Options:
			if value != nil {
				options = append(options, *value)
			}
		case func(*http.Request) (any, error):
			options = append(options, value)
		case string:
			if i == 0 && len(args) > 1 && !hasCallback && ValidCallback(value) &&
				!isConfigurationArgument(args[1]) {
				callback = value
				hasCallback = true
				continue
			}
			data = append(data, value)
		default:
			data = append(data, value)
		}
	}
	return NewHandler(options...), data, callback, hasCallback
}

func isConfigurationArgument(arg any) bool {
	switch arg.(type) {
	case Options, *Options, func(*http.Request) (any, error):
		return true
	default:
		return false
	}
}
