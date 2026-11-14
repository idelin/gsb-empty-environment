package netsec

import (
	"errors"
	"fmt"
)

// ErrorCode is the HTTP/2 error code space used by ConnectionError and
// StreamError.
type ErrorCode uint32

const (
	CodeNoError            ErrorCode = 0x0
	CodeProtocol           ErrorCode = 0x1
	CodeInternal           ErrorCode = 0x2
	CodeFlowControl        ErrorCode = 0x3
	CodeSettingsTimeout    ErrorCode = 0x4
	CodeStreamClosed       ErrorCode = 0x5
	CodeFrameSize          ErrorCode = 0x6
	CodeRefusedStream      ErrorCode = 0x7
	CodeCancel             ErrorCode = 0x8
	CodeCompression        ErrorCode = 0x9
	CodeConnect            ErrorCode = 0xa
	CodeEnhanceYourCalm    ErrorCode = 0xb
	CodeInadequateSecurity ErrorCode = 0xc
	CodeHTTP11Required     ErrorCode = 0xd
)

func (c ErrorCode) String() string {
	switch c {
	case CodeNoError:
		return "NO_ERROR"
	case CodeProtocol:
		return "PROTOCOL_ERROR"
	case CodeInternal:
		return "INTERNAL_ERROR"
	case CodeFlowControl:
		return "FLOW_CONTROL_ERROR"
	case CodeSettingsTimeout:
		return "SETTINGS_TIMEOUT"
	case CodeStreamClosed:
		return "STREAM_CLOSED"
	case CodeFrameSize:
		return "FRAME_SIZE_ERROR"
	case CodeRefusedStream:
		return "REFUSED_STREAM"
	case CodeCancel:
		return "CANCEL"
	case CodeCompression:
		return "COMPRESSION_ERROR"
	case CodeConnect:
		return "CONNECT_ERROR"
	case CodeEnhanceYourCalm:
		return "ENHANCE_YOUR_CALM"
	case CodeInadequateSecurity:
		return "INADEQUATE_SECURITY"
	case CodeHTTP11Required:
		return "HTTP_1_1_REQUIRED"
	default:
		return fmt.Sprintf("ERROR_CODE_%d", uint32(c))
	}
}

// Sentinel errors used by errors.Is.
var (
	ErrCanceled                = errors.New("netsec: header block canceled")
	ErrLimitExceeded           = errors.New("netsec: limit exceeded")
	ErrCompression             = errors.New("netsec: compression error")
	ErrInvalidHuffman          = errors.New("netsec: invalid Huffman-encoded data")
	ErrTruncatedBlock          = errors.New("netsec: truncated header block")
	ErrInvalidIndex            = errors.New("netsec: invalid HPACK index")
	ErrInvalidEncoding         = errors.New("netsec: invalid HPACK encoding")
	ErrDynamicTableSize        = errors.New("netsec: invalid dynamic table size update")
	ErrSensitiveHeaderIndexed  = errors.New("netsec: sensitive header must not be indexed")
	ErrPseudoHeaderOrder       = errors.New("netsec: pseudo-header after regular header")
	ErrDuplicateHeader         = errors.New("netsec: duplicate header")
	ErrUppercaseHeader         = errors.New("netsec: header name is not lowercase")
	ErrInvalidHeaderName       = errors.New("netsec: invalid header name")
	ErrFrameOrder              = errors.New("netsec: invalid connection-level frame order")
	ErrUnexpectedContinuation  = errors.New("netsec: unexpected CONTINUATION frame")
	ErrStreamClosed            = errors.New("netsec: stream is closed")
	ErrTenantIsolation         = errors.New("netsec: header compression context tenant mismatch")
	ErrSettingsNotSynchronized = errors.New("netsec: tenant switch requires synchronized SETTINGS")
	ErrConnectionClosed        = errors.New("netsec: connection is closed")
)

// ConnectionError is a connection-scoped error. The caller must not continue
// using the HTTP/2 connection after receiving one.
type ConnectionError struct {
	Code   ErrorCode
	Reason string
	Err    error
}

func (e *ConnectionError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err != nil {
		return fmt.Sprintf("netsec: connection error %s: %s: %v", e.Code, e.Reason, e.Err)
	}
	if e.Reason != "" {
		return fmt.Sprintf("netsec: connection error %s: %s", e.Code, e.Reason)
	}
	return fmt.Sprintf("netsec: connection error %s", e.Code)
}

func (e *ConnectionError) Unwrap() error { return e.Err }

// StreamError is scoped to one stream. Other streams on the same connection
// remain usable.
type StreamError struct {
	StreamID uint32
	Code     ErrorCode
	Reason   string
	Err      error
}

func (e *StreamError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err != nil {
		return fmt.Sprintf("netsec: stream %d error %s: %s: %v", e.StreamID, e.Code, e.Reason, e.Err)
	}
	if e.Reason != "" {
		return fmt.Sprintf("netsec: stream %d error %s: %s", e.StreamID, e.Code, e.Reason)
	}
	return fmt.Sprintf("netsec: stream %d error %s", e.StreamID, e.Code)
}

func (e *StreamError) Unwrap() error { return e.Err }

// AsConnectionError extracts a ConnectionError from err.
func AsConnectionError(err error) (*ConnectionError, bool) {
	var e *ConnectionError
	ok := errors.As(err, &e)
	return e, ok
}

// AsStreamError extracts a StreamError from err.
func AsStreamError(err error) (*StreamError, bool) {
	var e *StreamError
	ok := errors.As(err, &e)
	return e, ok
}

// IsConnectionError reports whether err is connection-scoped.
func IsConnectionError(err error) bool {
	_, ok := AsConnectionError(err)
	return ok
}

// IsStreamError reports whether err is stream-scoped.
func IsStreamError(err error) bool {
	_, ok := AsStreamError(err)
	return ok
}

func connErr(code ErrorCode, reason string, err error) *ConnectionError {
	return &ConnectionError{Code: code, Reason: reason, Err: err}
}

func streamErr(streamID uint32, code ErrorCode, reason string, err error) *StreamError {
	return &StreamError{StreamID: streamID, Code: code, Reason: reason, Err: err}
}

func compressionErr(reason string, err error) *ConnectionError {
	if err == nil {
		err = ErrCompression
	} else {
		err = fmt.Errorf("%w: %w", ErrCompression, err)
	}
	return connErr(CodeCompression, reason, err)
}
