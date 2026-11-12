package netsec

import (
	"context"
	"net/http"
)

// Handler wraps next so every inbound request owns a pipeline stream backed
// by its own http.ResponseWriter. The stream is bound to the concrete
// *http.Request instance, canceled automatically when the request context
// ends, and finalized (default 200) if next returns without writing status.
func (p *Pipeline) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sink := &httpSink{w: w}
		stream, err := p.Bind(r, sink)
		if err != nil {
			http.Error(w, "stream bind failed", http.StatusInternalServerError)
			return
		}
		stop := context.AfterFunc(r.Context(), func() { stream.Cancel() })
		defer stop()
		rw := &pipelineResponseWriter{
			ResponseWriter: w,
			stream:         stream,
			sink:           sink,
			hdr:            http.Header{},
		}
		defer func() {
			if !rw.wroteFinal && r.Context().Err() == nil && !stream.finished() {
				rw.WriteHeader(http.StatusOK)
			}
		}()

		next.ServeHTTP(rw, r)
	})
}

// StreamOf returns the stream associated with the response writer installed
// by Handler. Handlers use it to emit early hints directly.
func StreamOf(w http.ResponseWriter) (*Stream, bool) {
	rw, ok := w.(*pipelineResponseWriter)
	if !ok {
		return nil, false
	}
	return rw.stream, true
}

// httpSink serializes frames onto the underlying ResponseWriter. Hint headers
// are kept out of the final header map: for a 103 frame the writer's live
// header is replaced by the sanitized hint, then restored and scrubbed of
// authentication fields before the final response is built.
type httpSink struct {
	w http.ResponseWriter
}

func (s *httpSink) Hint(h http.Header) error {
	live := s.w.Header()
	snapshot := live.Clone()
	replaceHeader(live, h)
	s.w.WriteHeader(http.StatusEarlyHints)
	replaceHeader(live, snapshot)
	stripAuthSensitive(live)
	return nil
}

// Final takes explicit ownership of the underlying header map: the final
// frame is built solely from h. Hint headers live in separate maps and are
// never merged in, and any credential field is removed before the terminal
// WriteHeader so it cannot reach the receiver.
func (s *httpSink) Final(status int, h http.Header, _ []byte) error {
	scrubbed := h.Clone()
	stripAuthSensitive(scrubbed)
	replaceHeader(s.w.Header(), scrubbed)
	s.w.WriteHeader(status)
	return nil
}

func (s *httpSink) writeBody(b []byte) (int, error) {
	return s.w.Write(b)
}

// pipelineResponseWriter presents an isolated header map to the handler.
// Headers written there only reach the wire through Final; WriteHeader(103)
// sends a sanitized early-hints frame instead of a terminal response.
type pipelineResponseWriter struct {
	http.ResponseWriter
	stream        *Stream
	sink          *httpSink
	hdr           http.Header
	wroteFinal    bool
	finalAccepted bool
}

func (w *pipelineResponseWriter) Header() http.Header {
	return w.hdr
}

func (w *pipelineResponseWriter) WriteHeader(status int) {
	if w.wroteFinal {
		return
	}
	if status == http.StatusEarlyHints {
		_ = w.stream.Hint(w.hdr)
		return
	}
	w.wroteFinal = true
	if err := w.stream.Final(status, w.hdr, nil); err == nil {
		w.finalAccepted = true
	}
}

func (w *pipelineResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteFinal {
		w.WriteHeader(http.StatusOK)
	}
	rejected := w.stream.finished() && !w.finalAccepted
	if rejected {
		// The final frame was rejected (late/canceled): swallow body bytes
		// so the net/http server does not emit an unsolicited 200 afterwards.
		return len(b), nil
	}
	return w.sink.writeBody(b)
}

// Flush satisfies http.Flusher, triggering an implicit final response first.
func (w *pipelineResponseWriter) Flush() {
	if !w.wroteFinal {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func replaceHeader(dst, src http.Header) {
	for key := range dst {
		delete(dst, key)
	}
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
}

func stripAuthSensitive(h http.Header) {
	for key := range authSensitiveHeader {
		h.Del(key)
	}
}
