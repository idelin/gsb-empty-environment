package netsec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"time"
)

const (
	// MaxAdmissionReviewBytes is the maximum accepted AdmissionReview body
	// size. Bodies larger than this are rejected before evaluation.
	MaxAdmissionReviewBytes int64 = 1 << 20

	// MaxRequestBodyBytes is kept as a descriptive alias for callers that
	// prefer to name the HTTP body limit explicitly.
	MaxRequestBodyBytes = MaxAdmissionReviewBytes
	MaxBodyBytes        = MaxAdmissionReviewBytes

	// DefaultEvaluationTimeout is used when no timeout is supplied.
	DefaultEvaluationTimeout = 5 * time.Second
)

var (
	ErrRequestTooLarge   = errors.New("admission review request body exceeds 1 MiB")
	ErrUnsupportedAPI    = errors.New("unsupported admission review apiVersion")
	ErrUnsupportedKind   = errors.New("unsupported admission review kind")
	ErrMissingRequest    = errors.New("admission review is missing request")
	ErrEvaluationTimeout = errors.New("admission evaluation timed out")
)

// GroupVersionKind identifies the type of the object in an admission request.
type GroupVersionKind struct {
	Group   string `json:"group,omitempty"`
	Version string `json:"version,omitempty"`
	Kind    string `json:"kind,omitempty"`
}

// GroupVersionResource identifies the resource in an admission request.
type GroupVersionResource struct {
	Group    string `json:"group,omitempty"`
	Version  string `json:"version,omitempty"`
	Resource string `json:"resource,omitempty"`
}

// AdmissionRequest is the request portion of an AdmissionReview. Raw JSON
// fields are retained so a policy can inspect the submitted object without
// this package imposing a schema on it.
type AdmissionRequest struct {
	UID         string               `json:"uid"`
	Kind        GroupVersionKind     `json:"kind"`
	Resource    GroupVersionResource `json:"resource,omitempty"`
	SubResource string               `json:"subResource,omitempty"`
	Name        string               `json:"name,omitempty"`
	Namespace   string               `json:"namespace,omitempty"`
	Operation   string               `json:"operation,omitempty"`
	UserInfo    json.RawMessage      `json:"userInfo,omitempty"`
	Object      json.RawMessage      `json:"object,omitempty"`
	OldObject   json.RawMessage      `json:"oldObject,omitempty"`
	DryRun      *bool                `json:"dryRun,omitempty"`
	Options     json.RawMessage      `json:"options,omitempty"`
}

// Status is the status object returned in an AdmissionResponse.
type Status struct {
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Code    int32  `json:"code,omitempty"`
}

// AdmissionResponse is the response portion of an AdmissionReview.
type AdmissionResponse struct {
	UID              string            `json:"uid"`
	Allowed          bool              `json:"allowed"`
	Status           *Status           `json:"status,omitempty"`
	Result           *Status           `json:"result,omitempty"`
	Patch            string            `json:"patch,omitempty"`
	PatchType        *string           `json:"patchType,omitempty"`
	AuditAnnotations map[string]string `json:"auditAnnotations,omitempty"`
	Warnings         []string          `json:"warnings,omitempty"`
}

// AdmissionReview is the wire format accepted and returned by the handler.
type AdmissionReview struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Request    *AdmissionRequest  `json:"request,omitempty"`
	Response   *AdmissionResponse `json:"response,omitempty"`
}

// Evaluator is the primary evaluation contract. A nil error means the request
// is allowed unless the returned response explicitly sets Allowed to false.
type Evaluator interface {
	Evaluate(context.Context, *AdmissionReview) (*AdmissionResponse, error)
}

// EvaluatorFunc adapts a function to Evaluator.
type EvaluatorFunc func(context.Context, *AdmissionReview) (*AdmissionResponse, error)

func (f EvaluatorFunc) Evaluate(ctx context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
	return f(ctx, review)
}

// RequestEvaluator is an alternative evaluation contract for policies that
// only need the AdmissionRequest.
type RequestEvaluator interface {
	Evaluate(context.Context, *AdmissionRequest) (bool, error)
}

// Config controls optional handler behavior.
type Config struct {
	Timeout      time.Duration
	MaxBodyBytes int64
}

// Option mutates a Config.
type Option func(*Config)

// WithTimeout sets the evaluation deadline.
func WithTimeout(timeout time.Duration) Option {
	return func(cfg *Config) {
		cfg.Timeout = timeout
	}
}

// WithEvaluationTimeout is an alias for WithTimeout.
func WithEvaluationTimeout(timeout time.Duration) Option {
	return WithTimeout(timeout)
}

// WithDeadline is an alias for WithTimeout.
func WithDeadline(timeout time.Duration) Option {
	return WithTimeout(timeout)
}

// WithMaxBodyBytes changes the body size limit. It is primarily useful for
// callers that want a limit stricter than the mandatory 1 MiB ceiling.
func WithMaxBodyBytes(limit int64) Option {
	return func(cfg *Config) {
		cfg.MaxBodyBytes = limit
	}
}

// DefaultConfig returns the handler defaults.
func DefaultConfig() Config {
	return Config{
		Timeout:      DefaultEvaluationTimeout,
		MaxBodyBytes: MaxAdmissionReviewBytes,
	}
}

type evaluationResult struct {
	response    *AdmissionResponse
	err         error
	completedAt time.Time
}

type handlerOptions struct {
	timeout      time.Duration
	maxBodyBytes int64
	evaluate     func(context.Context, *AdmissionReview) (*AdmissionResponse, error)
	setupErr     error
}

type admissionHandler struct {
	options handlerOptions
}

// NewHandler creates an http.Handler that accepts AdmissionReview requests.
//
// The second and later arguments may be a time.Duration, Option values, or a
// Config. Supplying no arguments uses DefaultConfig.
func NewHandler(evaluator any, args ...any) http.Handler {
	return newAdmissionHandler(evaluator, args...)
}

// NewAdmissionHandler is an alias for NewHandler.
func NewAdmissionHandler(evaluator any, args ...any) http.Handler {
	return newAdmissionHandler(evaluator, args...)
}

// NewAdmissionReviewHandler is an alias for NewHandler.
func NewAdmissionReviewHandler(evaluator any, args ...any) http.Handler {
	return newAdmissionHandler(evaluator, args...)
}

// NewWebhook is an alias for NewHandler.
func NewWebhook(evaluator any, args ...any) http.Handler {
	return newAdmissionHandler(evaluator, args...)
}

// NewAdmissionWebhook is an alias for NewHandler.
func NewAdmissionWebhook(evaluator any, args ...any) http.Handler {
	return newAdmissionHandler(evaluator, args...)
}

// NewValidationWebhook is an alias for NewHandler.
func NewValidationWebhook(evaluator any, args ...any) http.Handler {
	return newAdmissionHandler(evaluator, args...)
}

// Handler wraps an evaluator in an http.Handler. This form is convenient when
// a function-style constructor is preferred over NewHandler.
func Handler(evaluator any, args ...any) http.Handler {
	return newAdmissionHandler(evaluator, args...)
}

// AdmissionReviewHandler is an alias for Handler.
func AdmissionReviewHandler(evaluator any, args ...any) http.Handler {
	return newAdmissionHandler(evaluator, args...)
}

func newAdmissionHandler(evaluator any, args ...any) http.Handler {
	cfg := DefaultConfig()
	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
			continue
		case time.Duration:
			cfg.Timeout = value
		case Option:
			if value != nil {
				value(&cfg)
			}
		case func(*Config):
			if value != nil {
				value(&cfg)
			}
		case Config:
			cfg = value
		case *Config:
			if value != nil {
				cfg = *value
			}
		}
	}
	if cfg.MaxBodyBytes <= 0 || cfg.MaxBodyBytes > MaxAdmissionReviewBytes {
		cfg.MaxBodyBytes = MaxAdmissionReviewBytes
	}

	evaluate, err := adaptEvaluator(evaluator)
	return &admissionHandler{
		options: handlerOptions{
			timeout:      cfg.Timeout,
			maxBodyBytes: cfg.MaxBodyBytes,
			evaluate:     evaluate,
			setupErr:     err,
		},
	}
}

func (h *admissionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAdmissionResponse(w, "admission.k8s.io/v1", "", false, http.StatusMethodNotAllowed,
			"admission review endpoint only accepts POST")
		return
	}
	if r.Body != nil {
		defer r.Body.Close()
	}

	body, tooLarge, readErr := readLimited(r.Body, h.options.maxBodyBytes)
	uid := extractRequestUID(body)

	if readErr != nil {
		writeAdmissionResponse(w, "admission.k8s.io/v1", uid, false, http.StatusForbidden,
			"could not read admission review request body")
		return
	}
	if tooLarge {
		writeAdmissionResponse(w, "admission.k8s.io/v1", uid, false, http.StatusForbidden,
			ErrRequestTooLarge.Error())
		return
	}

	review, apiVersion, err := decodeAdmissionReview(body)
	if err != nil {
		writeAdmissionResponse(w, "admission.k8s.io/v1", uid, false, http.StatusForbidden,
			"invalid admission review: "+err.Error())
		return
	}
	apiVersion = review.APIVersion

	if !supportedAPIVersion(review.APIVersion) {
		writeAdmissionResponse(w, apiVersion, uid, false, http.StatusForbidden,
			ErrUnsupportedAPI.Error())
		return
	}
	if review.Kind != "AdmissionReview" {
		writeAdmissionResponse(w, apiVersion, uid, false, http.StatusForbidden,
			ErrUnsupportedKind.Error())
		return
	}
	if review.Request == nil {
		writeAdmissionResponse(w, apiVersion, uid, false, http.StatusForbidden,
			ErrMissingRequest.Error())
		return
	}
	uid = review.Request.UID
	if h.options.setupErr != nil {
		writeAdmissionResponse(w, apiVersion, uid, false, http.StatusForbidden,
			"admission evaluator is not usable: "+h.options.setupErr.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.options.timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()

	results := make(chan evaluationResult, 1)
	go h.evaluateSafely(ctx, review, results)

	select {
	case result := <-results:
		if !result.completedAt.Before(deadline) {
			writeAdmissionResponse(w, apiVersion, uid, false, http.StatusForbidden,
				ErrEvaluationTimeout.Error())
			return
		}
		if result.err != nil {
			writeAdmissionResponse(w, apiVersion, uid, false, http.StatusForbidden,
				"admission evaluation failed: "+result.err.Error())
			return
		}
		response := result.response
		if response == nil {
			response = &AdmissionResponse{Allowed: true}
		}
		if response.Allowed {
			writeAdmissionResponse(w, apiVersion, uid, true, http.StatusOK, "")
			return
		}
		writeAdmissionResponse(w, apiVersion, uid, false, http.StatusForbidden,
			denialMessage(response))
	case <-ctx.Done():
		// The evaluator goroutine may still be running, but it has no access
		// to this ResponseWriter. The handler writes exactly one response here.
		writeAdmissionResponse(w, apiVersion, uid, false, http.StatusForbidden,
			ErrEvaluationTimeout.Error())
	}
}

func (h *admissionHandler) evaluateSafely(ctx context.Context, review *AdmissionReview, results chan<- evaluationResult) {
	var result evaluationResult
	defer func() {
		if recovered := recover(); recovered != nil {
			result.response = nil
			result.err = fmt.Errorf("panic: %v", recovered)
		}
		result.completedAt = time.Now()
		results <- result
	}()

	result.response, result.err = h.options.evaluate(ctx, review)
}

func readLimited(body io.Reader, limit int64) ([]byte, bool, error) {
	if body == nil {
		return nil, false, errors.New("request body is missing")
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	return data, int64(len(data)) > limit, err
}

func decodeAdmissionReview(body []byte) (*AdmissionReview, string, error) {
	var review AdmissionReview
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&review); err != nil {
		return nil, "", err
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, "", errors.New("multiple JSON values in request body")
		}
		return nil, "", fmt.Errorf("invalid trailing data: %w", err)
	}
	return &review, review.APIVersion, nil
}

func supportedAPIVersion(version string) bool {
	switch version {
	case "admission.k8s.io/v1", "admission.k8s.io/v1beta1":
		return true
	default:
		return false
	}
}

func denialMessage(response *AdmissionResponse) string {
	for _, status := range []*Status{response.Status, response.Result} {
		if status != nil && status.Message != "" {
			return status.Message
		}
	}
	return "admission request was denied"
}

func writeAdmissionResponse(w http.ResponseWriter, apiVersion, uid string, allowed bool, code int, message string) {
	response := &AdmissionResponse{
		UID:     uid,
		Allowed: allowed,
	}
	if !allowed {
		response.Status = &Status{
			Status:  "Failure",
			Message: message,
			Code:    int32(code),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(AdmissionReview{
		APIVersion: apiVersion,
		Kind:       "AdmissionReview",
		Response:   response,
	})
}

// extractRequestUID is used on malformed or oversized bodies where a normal
// JSON decode cannot complete. It walks tokens only far enough to find the
// top-level request.uid and never needs the rest of the body.
func extractRequestUID(body []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ""
	}

	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return ""
		}
		key, ok := keyToken.(string)
		if !ok {
			return ""
		}
		if key == "request" {
			return extractObjectUID(decoder)
		}
		if err := skipValue(decoder); err != nil {
			return ""
		}
	}
	return ""
}

func extractObjectUID(decoder *json.Decoder) string {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ""
	}

	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return ""
		}
		key, ok := keyToken.(string)
		if !ok {
			return ""
		}
		if key == "uid" {
			valueToken, err := decoder.Token()
			if err != nil {
				return ""
			}
			uid, _ := valueToken.(string)
			return uid
		}
		if err := skipValue(decoder); err != nil {
			return ""
		}
	}
	return ""
}

func skipValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok || (delim != '{' && delim != '[') {
		return nil
	}

	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			continue
		}
		switch delim {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
	}
	return nil
}

func adaptEvaluator(evaluator any) (func(context.Context, *AdmissionReview) (*AdmissionResponse, error), error) {
	switch fn := evaluator.(type) {
	case Evaluator:
		return fn.Evaluate, nil
	case RequestEvaluator:
		return func(ctx context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
			allowed, err := fn.Evaluate(ctx, review.Request)
			if err != nil {
				return nil, err
			}
			return &AdmissionResponse{Allowed: allowed}, nil
		}, nil
	case func(context.Context, *AdmissionReview) (*AdmissionResponse, error):
		return fn, nil
	case func(context.Context, *AdmissionReview) error:
		return func(ctx context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
			if err := fn(ctx, review); err != nil {
				return nil, err
			}
			return &AdmissionResponse{Allowed: true}, nil
		}, nil
	case func(context.Context, *AdmissionReview) (bool, error):
		return func(ctx context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
			allowed, err := fn(ctx, review)
			if err != nil {
				return nil, err
			}
			return &AdmissionResponse{Allowed: allowed}, nil
		}, nil
	case func(context.Context, *AdmissionRequest) (*AdmissionResponse, error):
		return func(ctx context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
			return fn(ctx, review.Request)
		}, nil
	case func(context.Context, *AdmissionRequest) error:
		return func(ctx context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
			if err := fn(ctx, review.Request); err != nil {
				return nil, err
			}
			return &AdmissionResponse{Allowed: true}, nil
		}, nil
	case func(context.Context, *AdmissionRequest) (bool, error):
		return func(ctx context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
			allowed, err := fn(ctx, review.Request)
			if err != nil {
				return nil, err
			}
			return &AdmissionResponse{Allowed: allowed}, nil
		}, nil
	case func(*AdmissionReview) (*AdmissionResponse, error):
		return func(_ context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
			return fn(review)
		}, nil
	case func(*AdmissionReview) error:
		return func(_ context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
			if err := fn(review); err != nil {
				return nil, err
			}
			return &AdmissionResponse{Allowed: true}, nil
		}, nil
	case func(*AdmissionReview) (bool, error):
		return func(_ context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
			allowed, err := fn(review)
			if err != nil {
				return nil, err
			}
			return &AdmissionResponse{Allowed: allowed}, nil
		}, nil
	case func(*AdmissionRequest) (*AdmissionResponse, error):
		return func(_ context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
			return fn(review.Request)
		}, nil
	case func(*AdmissionRequest) error:
		return func(_ context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
			if err := fn(review.Request); err != nil {
				return nil, err
			}
			return &AdmissionResponse{Allowed: true}, nil
		}, nil
	case func(*AdmissionRequest) (bool, error):
		return func(_ context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
			allowed, err := fn(review.Request)
			if err != nil {
				return nil, err
			}
			return &AdmissionResponse{Allowed: allowed}, nil
		}, nil
	}

	return adaptReflectedEvaluator(evaluator)
}

var (
	contextType  = reflect.TypeOf((*context.Context)(nil)).Elem()
	reviewType   = reflect.TypeOf((*AdmissionReview)(nil))
	requestType  = reflect.TypeOf((*AdmissionRequest)(nil))
	responseType = reflect.TypeOf((*AdmissionResponse)(nil))
	errorType    = reflect.TypeOf((*error)(nil)).Elem()
)

// adaptReflectedEvaluator supports named function types and small evaluator
// objects whose Evaluate method differs from the primary interface. It keeps
// the public surface compatible across common policy shapes.
func adaptReflectedEvaluator(evaluator any) (func(context.Context, *AdmissionReview) (*AdmissionResponse, error), error) {
	value := reflect.ValueOf(evaluator)
	if !value.IsValid() {
		return nil, errors.New("nil admission evaluator")
	}

	if value.Kind() != reflect.Func {
		if value.Kind() != reflect.Ptr && value.Kind() != reflect.Interface {
			return nil, fmt.Errorf("admission evaluator has unsupported type %T", evaluator)
		}
		if value.IsNil() {
			return nil, errors.New("nil admission evaluator")
		}
		method := value.MethodByName("Evaluate")
		if !method.IsValid() {
			return nil, fmt.Errorf("admission evaluator has unsupported type %T", evaluator)
		}
		value = method
	}

	function := value.Type()
	if function.Kind() != reflect.Func {
		return nil, fmt.Errorf("admission evaluator has unsupported type %T", evaluator)
	}
	if function.IsVariadic() {
		return nil, errors.New("variadic admission evaluators are not supported")
	}
	if function.NumIn() > 2 {
		return nil, fmt.Errorf("admission evaluator has %d parameters; expected at most 2", function.NumIn())
	}
	if function.NumOut() > 2 {
		return nil, fmt.Errorf("admission evaluator has %d return values; expected at most 2", function.NumOut())
	}

	return func(ctx context.Context, review *AdmissionReview) (*AdmissionResponse, error) {
		inputs := make([]reflect.Value, function.NumIn())
		for i := 0; i < function.NumIn(); i++ {
			parameter := function.In(i)
			switch {
			case parameter == contextType:
				inputs[i] = reflect.ValueOf(ctx)
			case parameter == reviewType:
				inputs[i] = reflect.ValueOf(review)
			case parameter == requestType:
				inputs[i] = reflect.ValueOf(review.Request)
			default:
				panic(fmt.Sprintf("admission evaluator parameter %d has unsupported type %s", i, parameter))
			}
		}

		outputs := value.Call(inputs)
		return reflectedResult(outputs)
	}, nil
}

func reflectedResult(outputs []reflect.Value) (*AdmissionResponse, error) {
	if len(outputs) == 0 {
		return &AdmissionResponse{Allowed: true}, nil
	}
	if len(outputs) == 2 {
		last := outputs[1]
		if !last.Type().Implements(errorType) {
			return nil, errors.New("admission evaluator's second return value is not an error")
		}
		if err := reflectError(last); err != nil {
			return nil, err
		}
		return reflectResponse(outputs[0])
	}

	single := outputs[0]
	if single.Type().Implements(errorType) {
		if err := reflectError(single); err != nil {
			return nil, err
		}
		return &AdmissionResponse{Allowed: true}, nil
	}
	return reflectResponse(single)
}

func reflectResponse(value reflect.Value) (*AdmissionResponse, error) {
	switch {
	case value.Type() == responseType:
		if value.IsNil() {
			return &AdmissionResponse{Allowed: true}, nil
		}
		return value.Interface().(*AdmissionResponse), nil
	case value.Type() == reflect.TypeOf(AdmissionResponse{}):
		response := value.Interface().(AdmissionResponse)
		return &response, nil
	case value.Kind() == reflect.Bool:
		return &AdmissionResponse{Allowed: value.Bool()}, nil
	default:
		return nil, fmt.Errorf("admission evaluator returned unsupported type %s", value.Type())
	}
}

func reflectError(value reflect.Value) error {
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		if value.IsNil() {
			return nil
		}
	}
	if value.CanInterface() {
		if err, ok := value.Interface().(error); ok {
			return err
		}
	}
	return errors.New("admission evaluator returned a non-nil invalid error value")
}
