package netsec

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// Responder writes the CONNECT response. WriteSuccess must return only after
// every response byte has been handed to the client: the ingress uses that
// return as the barrier that permits tunnel data to be forwarded.
type Responder interface {
	WriteSuccess(context.Context, io.Writer) error
	WriteFailure(context.Context, io.Writer, int) error
}

// ResponderFuncs adapts functions to Responder. A nil function selects the
// default behavior for that response class.
type ResponderFuncs struct {
	Success func(context.Context, io.Writer) error
	Failure func(context.Context, io.Writer, int) error
}

func (r ResponderFuncs) WriteSuccess(ctx context.Context, w io.Writer) error {
	if r.Success != nil {
		return r.Success(ctx, w)
	}
	return DefaultResponder{}.WriteSuccess(ctx, w)
}

func (r ResponderFuncs) WriteFailure(ctx context.Context, w io.Writer, status int) error {
	if r.Failure != nil {
		return r.Failure(ctx, w, status)
	}
	return DefaultResponder{}.WriteFailure(ctx, w, status)
}

// DefaultResponder writes minimal HTTP/1.1 CONNECT responses.
type DefaultResponder struct{}

func (DefaultResponder) WriteSuccess(ctx context.Context, w io.Writer) error {
	return writeStatus(ctx, w, http.StatusOK, "Connection Established")
}

func (DefaultResponder) WriteFailure(ctx context.Context, w io.Writer, status int) error {
	reason := http.StatusText(status)
	if reason == "" {
		reason = "Tunnel Failed"
	}
	return writeStatus(ctx, w, status, reason)
}

// WriteResponse writes a complete HTTP/1.1 status response.
func WriteResponse(w io.Writer, status int, reason string) error {
	return writeStatus(context.Background(), w, status, reason)
}

func writeStatus(ctx context.Context, w io.Writer, status int, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeAll(w, []byte(fmt.Sprintf("HTTP/1.1 %d %s\r\n\r\n", status, reason)))
}
