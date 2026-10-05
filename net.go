package gsb

import (
	"context"
	"errors"
	"fmt"
)

// ErrCancelled is returned when a message is dropped in transit.
var ErrCancelled = errors.New("gsb: message cancelled in transit")

// Direction distinguishes requests from responses for fault injection.
type Direction int

const (
	RequestDir Direction = iota
	ResponseDir
)

// FaultFunc may mutate or drop a message in transit. Returning nil drops
// the message, simulating cancellation or loss.
type FaultFunc func(dir Direction, msg any) any

// Link is a uni-directional, fault-injecting transport to one peer engine.
type Link struct {
	To      *Engine
	Fault   FaultFunc // applied to every message; nil means a clean link
	Dropped int
}

// Send delivers one request and returns the (possibly faulted) response.
func (l *Link) Send(ctx context.Context, req any) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.Fault != nil {
		req = l.Fault(RequestDir, req)
		if req == nil {
			l.Dropped++
			return nil, ErrCancelled
		}
	}
	var resp any
	switch r := req.(type) {
	case DigestRequest:
		resp = l.To.ServeDigest(r)
	case RangeRequest:
		resp = l.To.ServeRange(r)
	default:
		return nil, fmt.Errorf("gsb: unknown request type %T", req)
	}
	if l.Fault != nil {
		m := l.Fault(ResponseDir, resp)
		if m == nil {
			l.Dropped++
			return nil, ErrCancelled
		}
		resp = m
	}
	return resp, nil
}

// Chain composes faults; each sees the output of the previous one.
func Chain(fs ...FaultFunc) FaultFunc {
	return func(dir Direction, msg any) any {
		for _, f := range fs {
			if msg == nil {
				return nil
			}
			msg = f(dir, msg)
		}
		return msg
	}
}

// DropMessages drops the next n messages of any type (transport
// cancellation/loss), then passes traffic through.
func DropMessages(n int) FaultFunc {
	remaining := n
	return func(dir Direction, msg any) any {
		if remaining > 0 {
			remaining--
			return nil
		}
		return msg
	}
}

// ForgeDigests corrupts the first digest of the next n digest responses.
func ForgeDigests(n int) FaultFunc {
	remaining := n
	return func(dir Direction, msg any) any {
		if remaining > 0 && dir == ResponseDir {
			if dr, ok := msg.(DigestResponse); ok && len(dr.Digests) > 0 {
				dr.Digests[0].Hash ^= 0xDEADBEEF
				remaining--
				return dr
			}
		}
		return msg
	}
}

// TruncateRanges makes the next n non-empty range responses incomplete:
// the last record is dropped while Done and the final digest are kept, so
// the receiver's digest verification must catch it.
func TruncateRanges(n int) FaultFunc {
	remaining := n
	return func(dir Direction, msg any) any {
		if remaining > 0 && dir == ResponseDir {
			if rr, ok := msg.(RangeResponse); ok && len(rr.Records) > 0 {
				rr.Records = rr.Records[:len(rr.Records)-1]
				remaining--
				return rr
			}
		}
		return msg
	}
}

// ReplayRanges captures the first range response it sees and replays it in
// place of the next n range responses, simulating duplicated ranges.
func ReplayRanges(n int) FaultFunc {
	var saved *RangeResponse
	remaining := n
	return func(dir Direction, msg any) any {
		if dir != ResponseDir {
			return msg
		}
		rr, ok := msg.(RangeResponse)
		if !ok {
			return msg
		}
		if saved == nil {
			r := rr
			saved = &r
			return msg
		}
		if remaining > 0 {
			remaining--
			return *saved
		}
		return msg
	}
}
