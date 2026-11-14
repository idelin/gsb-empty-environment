package netsec

import (
	"context"
	"net/http"
)

type rejectionKey struct{}

// RejectionFromContext returns the reason inbound tracing headers were not
// trusted, if the middleware rejected any.
func RejectionFromContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	err, _ := ctx.Value(rejectionKey{}).(error)
	return err
}

func withRejection(ctx context.Context, err error) context.Context {
	if err == nil {
		return ctx
	}
	return context.WithValue(ctx, rejectionKey{}, err)
}

// Middleware authenticates inbound tracing headers, starts a new local span,
// samples using local policy, and replaces the inbound tracing headers with a
// freshly signed local context. Rejected contexts never reject the HTTP
// request itself; they are isolated into a new trace and remain available via
// RejectionFromContext for audit.
func (t *Tracer) Middleware(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		extraction := t.ExtractRequest(r)
		ctx := WithTraceContext(r.Context(), extraction.Context)
		ctx = withRejection(ctx, extraction.Rejection)
		r = r.WithContext(ctx)

		// Never let an application or reverse proxy blindly forward a forged
		// inbound header. Rebuild the entire tracing header set locally.
		clearTracingHeaders(r.Header)
		if err := t.WriteHeaders(r.Header, extraction.Context); err != nil {
			clearTracingHeaders(r.Header)
			r = r.WithContext(withRejection(r.Context(), err))
		}

		next.ServeHTTP(w, r)
	})
}

// Handler is an alias for Middleware.
func (t *Tracer) Handler(next http.Handler) http.Handler {
	return t.Middleware(next)
}
