package h2guard

import "fmt"

// ConnError is an HTTP/2 connection-level error. When Handle returns one, the
// connection must be torn down after emitting the GOAWAY frame available from
// the parser (see Parser.GoAwayFrame).
type ConnError struct {
	// Code is the HTTP/2 error code carried by GOAWAY/RST_STREAM.
	Code uint32
	// Message is a human-readable diagnostic, suitable for debug data.
	Message string
	// GoAway is true when the host must send GOAWAY and close the TCP
	// connection. Every connection error produced by the parser is fatal.
	GoAway bool
}

func (e *ConnError) Error() string {
	return fmt.Sprintf("h2guard: connection error code=0x%x: %s", e.Code, e.Message)
}

// StreamError is an error scoped to a single stream. The stream must be reset
// (RST_STREAM with Code) but the connection stays alive.
type StreamError struct {
	StreamID uint32
	Code     uint32
	Message  string
}

func (e *StreamError) Error() string {
	return fmt.Sprintf("h2guard: stream %d error code=0x%x: %s", e.StreamID, e.Code, e.Message)
}

// AsConnError unwraps err to a *ConnError.
func AsConnError(err error) (*ConnError, bool) {
	ce, ok := err.(*ConnError)
	return ce, ok
}

// AsStreamError unwraps err to a *StreamError.
func AsStreamError(err error) (*StreamError, bool) {
	se, ok := err.(*StreamError)
	return se, ok
}

func connError(code uint32, format string, args ...any) *ConnError {
	return &ConnError{Code: code, Message: fmt.Sprintf(format, args...), GoAway: true}
}

func streamError(streamID, code uint32, format string, args ...any) *StreamError {
	return &StreamError{StreamID: streamID, Code: code, Message: fmt.Sprintf(format, args...)}
}
