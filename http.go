package netsec

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

// HTTPEmitter writes stages to an http.ResponseWriter. It temporarily limits
// the writer's header map to the filtered hint fields while a 103 is written,
// then restores the previous map. This prevents unrelated headers already in
// the map from being attached to an informational response.
//
// The writer must support informational (1xx) responses, as the net/http
// server response writer does.
//
// Emit calls on one HTTPEmitter are serialized. The underlying
// http.ResponseWriter must still not be used concurrently by other code.
type HTTPEmitter struct {
	w         http.ResponseWriter
	policy    *HintPolicy
	mu        sync.Mutex
	finalized bool
}

// NewHTTPEmitter creates an emitter for w using the default hint policy.
func NewHTTPEmitter(w http.ResponseWriter) (*HTTPEmitter, error) {
	return NewHTTPEmitterWithPolicy(w, DefaultHintPolicy())
}

// NewHTTPEmitterWithPolicy creates an emitter that applies policy to every
// hint it writes. A policy is required so direct use of the emitter cannot
// bypass the hint whitelist.
func NewHTTPEmitterWithPolicy(w http.ResponseWriter, policy *HintPolicy) (*HTTPEmitter, error) {
	if isNil(w) {
		return nil, errors.New("netsec: nil http response writer")
	}
	if policy == nil {
		return nil, errors.New("netsec: nil hint policy")
	}
	return &HTTPEmitter{w: w, policy: policy}, nil
}

// NewHTTPResponse binds a MultiStageResponse to an http.ResponseWriter.
func NewHTTPResponse(ctx context.Context, id RequestID, w http.ResponseWriter, opts ...Option) (*MultiStageResponse, error) {
	cfg, err := applyOptions(opts)
	if err != nil {
		return nil, err
	}
	emitter, err := NewHTTPEmitterWithPolicy(w, cfg.policy)
	if err != nil {
		return nil, err
	}
	return newMultiStageResponse(ctx, id, emitter, cfg)
}

// Emit writes one stage to the response writer.
func (e *HTTPEmitter) Emit(stage Stage) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.finalized {
		if stage.Kind == StageHint {
			return ErrLateHint
		}
		return ErrFinalized
	}

	switch stage.Kind {
	case StageHint:
		if stage.Status != StatusEarlyHints {
			return ErrInvalidStatus
		}
		header, _ := e.policy.Filter(stage.Header)
		if len(header) == 0 {
			return ErrNoHintFields
		}
		return e.writeHint(header)
	case StageFinal:
		if stage.Status < 200 || stage.Status > 599 {
			return ErrInvalidStatus
		}
		header, err := cloneHeader(stage.Header)
		if err != nil {
			return err
		}
		e.finalized = true
		writeHeaderValues(e.w.Header(), header)
		e.w.WriteHeader(stage.Status)
		return nil
	default:
		return errors.New("netsec: unknown response stage")
	}
}

func (e *HTTPEmitter) writeHint(header http.Header) error {
	current := e.w.Header().Clone()
	clearHeader(e.w.Header())
	writeHeaderValues(e.w.Header(), header)
	e.w.WriteHeader(StatusEarlyHints)
	clearHeader(e.w.Header())
	restoreHeader(e.w.Header(), current)
	return nil
}

func writeHeaderValues(dst, src http.Header) {
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
}

func restoreHeader(dst, src http.Header) {
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
}

func clearHeader(h http.Header) {
	for key := range h {
		delete(h, key)
	}
}
