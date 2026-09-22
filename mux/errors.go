package mux

import "errors"

// ErrCode is a protocol error code carried by RST_STREAM frames and used in
// connection-level protocol errors.
type ErrCode uint32

const (
	ErrCodeNo ErrCode = iota
	ErrCodeProtocol
	ErrCodeFlowControl
	ErrCodeStreamClosed
	ErrCodeFrameSize
)

func (c ErrCode) String() string {
	switch c {
	case ErrCodeNo:
		return "NO_ERROR"
	case ErrCodeProtocol:
		return "PROTOCOL_ERROR"
	case ErrCodeFlowControl:
		return "FLOW_CONTROL_ERROR"
	case ErrCodeStreamClosed:
		return "STREAM_CLOSED"
	case ErrCodeFrameSize:
		return "FRAME_SIZE_ERROR"
	}
	return "UNKNOWN"
}

// ProtocolError is a connection-level error: the peer sent malformed data.
// It is distinct from ErrNeedMore, which only means the buffer is incomplete.
type ProtocolError struct {
	Code ErrCode
	Msg  string
}

func (e *ProtocolError) Error() string {
	return "mux: connection error " + e.Code.String() + ": " + e.Msg
}

// StreamError is a stream-level error: the stream is torn down but the
// connection stays alive.
type StreamError struct {
	Code ErrCode
	Msg  string
}

func (e *StreamError) Error() string {
	return "mux: stream error " + e.Code.String() + ": " + e.Msg
}

// ErrNeedMore is returned by Parser.Next when the buffered bytes do not yet
// contain a complete frame. It is not a protocol violation.
var ErrNeedMore = errors.New("mux: incomplete frame, need more data")

// ErrStreamClosed is returned by Write after the local send side is closed.
var ErrStreamClosed = errors.New("mux: stream closed")

// ErrConnClosed is returned when the connection has been closed.
var ErrConnClosed = errors.New("mux: connection closed")
