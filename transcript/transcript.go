// Package transcript implements handshake transcript state for the Go
// protocol host. A recording proxy may deliver the same handshake message
// more than once (retransmission) or out of order; the transcript must
// still extend exactly once per message, in sequence order. Two different
// bodies carrying the same sequence number are a transcript fork and are
// reported as a conflict. Renegotiation restarts are explicit: the host
// opens a fresh context per epoch and stale messages cannot leak in.
package transcript

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
)

// Message is a single handshake message observed by the recording proxy.
type Message struct {
	Epoch uint64 // negotiation epoch the message belongs to
	Seq   uint64 // handshake message sequence number within the epoch
	Body  []byte // handshake message body
}

// Outcome describes what Record did with a message.
type Outcome int

const (
	// Appended means the message extended the transcript in order.
	Appended Outcome = iota
	// Buffered means the message arrived early and is held until the
	// sequence gap before it is filled.
	Buffered
	// Duplicate means the message is an exact retransmission of an
	// already recorded sequence number and was ignored.
	Duplicate
)

func (o Outcome) String() string {
	switch o {
	case Appended:
		return "Appended"
	case Buffered:
		return "Buffered"
	case Duplicate:
		return "Duplicate"
	default:
		return fmt.Sprintf("Outcome(%d)", int(o))
	}
}

// ConflictError reports a transcript fork: two different bodies were
// recorded under the same sequence number within one epoch.
type ConflictError struct {
	Epoch  uint64
	Seq    uint64
	First  [sha256.Size]byte // digest of the body recorded first
	Second [sha256.Size]byte // digest of the conflicting body
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("transcript: epoch %d seq %d conflict: body digest %x already recorded, got %x",
		e.Epoch, e.Seq, e.First[:8], e.Second[:8])
}

var (
	// ErrStaleEpoch is returned when a message whose epoch does not match
	// the context epoch is offered for recording. After a renegotiation
	// restart, replayed messages from the old epoch are rejected with
	// this error and can never leak into the new transcript.
	ErrStaleEpoch = errors.New("transcript: message from stale epoch")

	// ErrSealed is returned when recording into a context that has been
	// superseded by a renegotiation restart.
	ErrSealed = errors.New("transcript: context sealed by restart")
)

// Context is the transcript state for one negotiation epoch. It is not
// safe for concurrent use.
type Context struct {
	epoch   uint64
	hash    hash.Hash
	seen    map[uint64][sha256.Size]byte // digest of every recorded seq
	pending map[uint64][]byte            // out-of-order bodies awaiting gaps
	next    uint64                       // next sequence number to append
	n       int                          // messages appended so far
	sealed  bool
}

func newContext(epoch uint64) *Context {
	return &Context{
		epoch:   epoch,
		hash:    sha256.New(),
		seen:    make(map[uint64][sha256.Size]byte),
		pending: make(map[uint64][]byte),
	}
}

// Record folds one observed message into the transcript. Exact
// retransmissions are deduplicated, out-of-order messages are buffered
// until their predecessors arrive, and a body that disagrees with an
// already recorded sequence number yields a *ConflictError.
func (c *Context) Record(m Message) (Outcome, error) {
	if c.sealed {
		return 0, ErrSealed
	}
	if m.Epoch != c.epoch {
		return 0, fmt.Errorf("%w: message epoch %d, context epoch %d", ErrStaleEpoch, m.Epoch, c.epoch)
	}
	digest := sha256.Sum256(m.Body)
	if first, ok := c.seen[m.Seq]; ok {
		if first != digest {
			return 0, &ConflictError{Epoch: c.epoch, Seq: m.Seq, First: first, Second: digest}
		}
		return Duplicate, nil
	}
	c.seen[m.Seq] = digest
	if m.Seq == c.next {
		c.append(m.Seq, m.Body)
		for body, ok := c.pending[c.next]; ok; body, ok = c.pending[c.next] {
			delete(c.pending, c.next)
			c.append(c.next, body)
		}
		return Appended, nil
	}
	c.pending[m.Seq] = append([]byte(nil), m.Body...)
	return Buffered, nil
}

func (c *Context) append(seq uint64, body []byte) {
	var hdr [8]byte
	binary.BigEndian.PutUint64(hdr[:], seq)
	c.hash.Write(hdr[:])
	c.hash.Write(body)
	c.next = seq + 1
	c.n++
}

// Sum returns the current transcript digest over all messages appended
// so far, in sequence order.
func (c *Context) Sum() []byte { return c.hash.Sum(nil) }

// Len reports how many distinct messages have been appended in order.
func (c *Context) Len() int { return c.n }

// Epoch reports the negotiation epoch this context belongs to.
func (c *Context) Epoch() uint64 { return c.epoch }

// Sealed reports whether the context has been closed by a restart.
func (c *Context) Sealed() bool { return c.sealed }

// Host is the protocol host holding the live transcript context. A
// renegotiation restart must go through Restart, which seals the old
// context and opens a fresh one; there is no implicit carry-over.
type Host struct {
	epoch uint64
	ctx   *Context
}

// NewHost returns a host whose first transcript context is epoch 0.
func NewHost() *Host {
	return &Host{ctx: newContext(0)}
}

// Restart explicitly begins a new negotiation epoch: the current context
// is sealed and a fresh, empty context becomes current. Messages recorded
// before the restart stay in the old context and cannot enter the new
// transcript.
func (h *Host) Restart() {
	h.ctx.sealed = true
	h.epoch++
	h.ctx = newContext(h.epoch)
}

// Record folds a message into the current epoch's transcript context.
func (h *Host) Record(m Message) (Outcome, error) { return h.ctx.Record(m) }

// Context returns the host's current transcript context.
func (h *Host) Context() *Context { return h.ctx }
