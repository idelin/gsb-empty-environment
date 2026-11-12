package netsec

import (
	"bufio"
	"net"
	"net/http"
)

type trackingResponseWriter struct {
	http.ResponseWriter
	request     *http.Request
	guard       *readGuard
	status      int
	wroteHeader bool
}

func (w *trackingResponseWriter) WriteHeader(status int) {
	if status >= 100 && status <= 199 {
		w.ResponseWriter.WriteHeader(status)
		return
	}

	if w.guard != nil && w.guard.overflowed() {
		closeRequest(w, w.request)
		status = http.StatusRequestEntityTooLarge
	}
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *trackingResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *trackingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *trackingResponseWriter) FlushError() error {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *trackingResponseWriter) Flush() {
	_ = w.FlushError()
}

func (w *trackingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *trackingResponseWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}
