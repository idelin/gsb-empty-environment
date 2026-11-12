package netsec

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// UnlimitedBodyBytes disables the request body limit when assigned to
// Config.MaxBodyBytes. Config.MaxBodyBytes uses a deny-by-default zero value,
// so callers that need unlimited bodies must opt in explicitly.
const UnlimitedBodyBytes int64 = -1

// Authenticator decides whether a request may proceed to the next handler.
// It runs before the request body is made available and must not read the body.
// When allowed is false, statusCode is sent as the final response and the
// connection is closed. A zero or non-error statusCode defaults to 401.
type Authenticator func(r *http.Request) (allowed bool, statusCode int)

// Config controls Expect handling, request body budgeting and authentication.
type Config struct {
	// MaxBodyBytes is the maximum number of request body bytes accepted for
	// this request. Set it to UnlimitedBodyBytes to disable the limit.
	MaxBodyBytes int64

	// Authenticate runs only after the request has been validated and before
	// the request body can be read. A nil Authenticate allows every request.
	Authenticate Authenticator

	// Next handles an authenticated request. Expect has already been removed
	// from the request headers when Next is called.
	Next http.Handler
}

// NewHandler returns an HTTP handler that implements the configured Expect
// policy. It is equivalent to returning cfg directly.
func NewHandler(cfg Config) http.Handler {
	return cfg
}

// New is a convenience constructor for the common Config layout.
func New(maxBodyBytes int64, authenticate Authenticator, next http.Handler) http.Handler {
	return Config{
		MaxBodyBytes: maxBodyBytes,
		Authenticate: authenticate,
		Next:         next,
	}
}

// Middleware returns a middleware function that installs the configured
// policy around next.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		cfg.Next = next
		return cfg
	}
}

// ServeHTTP validates the request, obtains an authentication decision, and
// only then exposes the body to the next handler. Go's net/http server sends
// 100 Continue automatically on the first body read, so no 100 response is
// emitted before authentication completes.
func (cfg Config) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r == nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	if hasTransferEncoding(r) && declaresContentLength(r) {
		rejectRequest(w, r, http.StatusBadRequest)
		return
	}

	declaredLength, hasDeclaredLength, err := declaredBodyLength(r)
	if err != nil {
		rejectRequest(w, r, http.StatusBadRequest)
		return
	}
	if cfg.MaxBodyBytes >= 0 && hasDeclaredLength && declaredLength > cfg.MaxBodyBytes {
		rejectRequest(w, r, http.StatusRequestEntityTooLarge)
		return
	}

	_, status := inspectExpect(r)
	if status != 0 {
		rejectRequest(w, r, status)
		return
	}

	originalBody := r.Body
	if originalBody != nil {
		// Prevent authentication code, intentionally or accidentally, from
		// starting the body read that triggers net/http's automatic 100.
		r.Body = &authenticationPendingBody{}
	}
	defer func() {
		r.Body = originalBody
	}()

	allowed, status := true, 0
	if cfg.Authenticate != nil {
		allowed, status = cfg.Authenticate(r)
	}
	if !allowed {
		if status < 400 || status > 599 {
			status = http.StatusUnauthorized
		}
		rejectRequest(w, r, status)
		return
	}

	r.Body = originalBody
	deleteHeader(r.Header, "Expect")

	var guard *readGuard
	if cfg.MaxBodyBytes >= 0 && originalBody != nil && originalBody != http.NoBody {
		guard = &readGuard{request: r, writer: w}
		r.Body = &budgetBody{
			body:      originalBody,
			limit:     cfg.MaxBodyBytes,
			remaining: cfg.MaxBodyBytes,
			guard:     guard,
		}
	}

	next := cfg.Next
	if next == nil {
		next = http.NotFoundHandler()
	}

	tracked := &trackingResponseWriter{
		ResponseWriter: w,
		request:        r,
		guard:          guard,
	}
	next.ServeHTTP(tracked, r)

	// Restore the original body so net/http can recognize an unconsumed
	// expectContinueReader and close the connection without trying to drain it.
	r.Body = originalBody
	if guard != nil && guard.overflowed() {
		closeRequest(tracked, r)
		if !tracked.wroteHeader {
			tracked.WriteHeader(http.StatusRequestEntityTooLarge)
		}
	}
}

// rejectRequest sends a final response without reading the body and forces the
// connection closed, since unread request bytes must not be reused.
func rejectRequest(w http.ResponseWriter, r *http.Request, status int) {
	closeRequest(w, r)
	w.WriteHeader(status)
}

func closeRequest(w http.ResponseWriter, r *http.Request) {
	if r != nil {
		r.Close = true
		if r.ProtoMajor < 2 {
			w.Header().Set("Connection", "close")
		}
	}
	// net/http closes the body in finishRequest, and its early-close path may
	// otherwise drain up to a fixed number of unread bytes. Expiring the read
	// deadline makes that cleanup fail immediately instead of reading the body.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now())
}

type expectDisposition int

const (
	expectNone expectDisposition = iota
	expectContinue
)

// inspectExpect validates all Expect field values. A common disposition is
// returned only for a single, HTTP/1.1-or-later 100-continue value.
func inspectExpect(r *http.Request) (expectDisposition, int) {
	values, present := headerValues(r.Header, "Expect")
	if !present {
		return expectNone, 0
	}

	if len(values) > 1 {
		return expectNone, http.StatusBadRequest
	}

	// HTTP/1.0 has no 100-continue exchange. Ignore the field after validating
	// that it is not duplicated.
	if r.ProtoMajor == 1 && r.ProtoMinor == 0 {
		return expectNone, 0
	}

	if len(values) != 1 || !strings.EqualFold(strings.TrimSpace(values[0]), "100-continue") {
		return expectNone, http.StatusExpectationFailed
	}
	return expectContinue, 0
}

func hasTransferEncoding(r *http.Request) bool {
	if len(r.TransferEncoding) > 0 {
		return true
	}
	_, present := headerValues(r.Header, "Transfer-Encoding")
	return present
}

func declaresContentLength(r *http.Request) bool {
	if _, present := headerValues(r.Header, "Content-Length"); present {
		return true
	}
	return r.ContentLength >= 0
}

// declaredBodyLength returns a syntactically valid Content-Length, if one is
// declared. Multiple Content-Length fields are rejected rather than
// interpreted, mirroring the ambiguity that enables request smuggling.
func declaredBodyLength(r *http.Request) (length int64, declared bool, err error) {
	values, present := headerValues(r.Header, "Content-Length")
	if present {
		if len(values) != 1 {
			return 0, false, errors.New("multiple Content-Length fields")
		}
		value := strings.TrimSpace(values[0])
		if value == "" {
			return 0, false, errors.New("empty Content-Length")
		}
		length, err = strconv.ParseInt(value, 10, 64)
		if err != nil || length < 0 {
			return 0, false, errors.New("invalid Content-Length")
		}
		return length, true, nil
	}
	if r.ContentLength >= 0 {
		return r.ContentLength, true, nil
	}
	return 0, false, nil
}

// headerValues performs a case-insensitive lookup directly in the map. Header
// accessors only canonicalize keys, which is not enough for hand-built
// requests and for proxy code that may preserve odd casing.
func headerValues(h http.Header, name string) (values []string, present bool) {
	for key, fieldValues := range h {
		if strings.EqualFold(key, name) {
			present = true
			if len(fieldValues) == 0 {
				values = append(values, "")
				continue
			}
			values = append(values, fieldValues...)
		}
	}
	return values, present
}

func deleteHeader(h http.Header, name string) {
	for key := range h {
		if strings.EqualFold(key, name) {
			delete(h, key)
		}
	}
}
