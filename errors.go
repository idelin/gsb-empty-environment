package netsec

import "errors"

// ErrDropped is returned by Decision.Err for stanzas that must be discarded
// without sending a stream error.
var ErrDropped = errors.New("stanza dropped")

// StreamError represents an RFC 6120 stream error that requires the stream to
// be closed.
type StreamError struct {
	Condition string
}

func (e *StreamError) Error() string {
	if e == nil {
		return ""
	}
	return "XMPP stream error: " + e.Condition
}
