// Package revco implements revocation coordination for endpoint isolation.
//
// The coordinator consumes an abstract kernel event graph of generational
// object holds, together with per-channel monotonic watermarks. Every event at
// or below a channel watermark is guaranteed complete; events above the
// watermark may have arrived early and are never treated as committed evidence.
// The coordinator traces provenance of holds (issue/copy/slice/derive/send/
// receive), revokes every live hold that descends from a requested isolation
// range, and reports exactly which byte ranges remain unprovable because a
// participating channel has not advanced far enough.
package revco

// ObjID identifies a mapped object (file mapping).
type ObjID string

// ChanID identifies an event channel. Each actor typically owns one channel;
// message receive events appear on the receiver's channel.
type ChanID string

// Right is a single access right over a mapping range.
type Right string

const (
	Read  Right = "read"
	Write Right = "write"
	Exec  Right = "exec"
)

// HoldID is the per-generation identifier of a hold. A hold's identity is the
// (Generation, HoldID) pair: reusing HoldID in a later generation names a
// different hold.
type HoldID string

// Generation is the object-generation stamp carried by every event. Events in
// different generations never alias one another even when HoldIDs repeat.
type Generation int64

// Seq is the per-channel monotonic event sequence number.
type Seq int64

// Range is a half-open byte interval [Off, Off+Len). Len 0 denotes an empty
// range.
type Range struct {
	Off int64
	Len int64
}

// End returns the exclusive upper bound of r.
func (r Range) End() int64 { return r.Off + r.Len }

// Empty reports whether r contains no bytes.
func (r Range) Empty() bool { return r.Len <= 0 }

func (r Range) contains(off int64) bool {
	return r.Len > 0 && off >= r.Off && off < r.End()
}

// Owner names the process (and optional background request) that currently
// holds a hold.
type Owner struct {
	Process string
	Request string
}

// HoldKey uniquely names a hold across generational reuse.
type HoldKey struct {
	Gen Generation
	ID  HoldID
}

// Hold is one materialized, sourced access grant reconstructed from events.
type Hold struct {
	Key     HoldKey
	Object  ObjID
	Range   Range
	Rights  map[Right]bool
	Owner   Owner
	Sources []HoldKey // provenance edges; empty for the initial issue
	// Msg is non-nil while the hold is a sent message that no receive event has
	// yet consumed. Such a hold is a live, revocable receive right in flight.
	Msg        *Message
	Dead       bool
	DeadReason string
}

// Message describes an in-flight send waiting to be received.
type Message struct {
	Sender   Owner
	Receiver Owner
}

// EventType enumerates the abstract kernel events.
type EventType string

const (
	EvIssue   EventType = "issue"
	EvCopy    EventType = "copy"
	EvSlice   EventType = "slice"
	EvDerive  EventType = "derive"
	EvSend    EventType = "send"
	EvReceive EventType = "receive"
	EvClose   EventType = "close"
	EvRevoke  EventType = "revoke"
)

// Event is one generational object event on a channel.
//
// NewID names the hold produced by issue/copy/slice/derive/send/receive.
// Source/SrcGen name the consumed (parent) hold for copy/slice/derive/send and
// the in-flight message hold for receive. SrcGen zero means "the same
// generation as the event itself". Range/Rights describe the produced hold;
// zero Range on a send means "the full source range". Close/revoke ignore them.
type Event struct {
	Type     EventType
	Channel  ChanID
	Seq      Seq
	Gen      Generation
	Object   ObjID
	NewID    HoldID
	Owner    Owner
	Source   HoldID
	SrcGen   Generation
	Range    Range
	Rights   []Right
	Sender   Owner
	Receiver Owner
	DestChan ChanID // channel on which the matching receive is expected
}

func (e Event) srcGen() Generation {
	if e.SrcGen != 0 {
		return e.SrcGen
	}
	return e.Gen
}

func (e Event) srcKey() HoldKey {
	return HoldKey{Gen: e.srcGen(), ID: e.Source}
}

func (e Event) newKey() HoldKey { return HoldKey{Gen: e.Gen, ID: e.NewID} }

// EventConflict describes two events on one channel claiming the same sequence
// number with different payloads.
type EventConflict struct {
	Channel ChanID
	Seq     Seq
}

func (c EventConflict) Error() string {
	return "revco: conflicting events on channel " + string(c.Channel) + " at seq " + itoa(int64(c.Seq))
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
