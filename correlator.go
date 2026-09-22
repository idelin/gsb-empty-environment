package correlator

import (
	"fmt"
	"sort"
	"time"
)

// EventKind describes what the correlator concluded about a message.
type EventKind int

const (
	EventAccepted         EventKind = iota // new request; tracking started
	EventDuplicateRequest                  // replayed request; original start time kept
	EventRejected                          // pending table full (bounded storage)
	EventMatched                           // response paired with a pending request
	EventLate                              // response arrived after the request expired
	EventOrphan                            // response with no known request
	EventExpired                           // request exceeded Timeout
)

// Event is emitted for every message the correlator processes.
type Event struct {
	Kind    EventKind
	Epoch   uint64
	Seq     uint64
	Latency time.Duration // set for EventMatched
}

func (e Event) String() string {
	id := fmt.Sprintf("epoch=%d seq=%d", e.Epoch, e.Seq)
	switch e.Kind {
	case EventAccepted:
		return "ACCEPTED   " + id
	case EventDuplicateRequest:
		return "DUP-REQ    " + id + " (start time unchanged)"
	case EventRejected:
		return "REJECTED   " + id + " (pending table full)"
	case EventMatched:
		return fmt.Sprintf("MATCHED    %s latency=%s", id, e.Latency.Round(time.Millisecond))
	case EventLate:
		return "LATE       " + id + " (request already expired)"
	case EventOrphan:
		return "ORPHAN     " + id + " (no known request)"
	case EventExpired:
		return "EXPIRED    " + id + " (timeout)"
	}
	return "UNKNOWN    " + id
}

type key struct {
	epoch uint64
	seq   uint64
}

type requestState struct {
	start time.Time
}

// Config tunes a Correlator.
type Config struct {
	Timeout       time.Duration    // request lifetime before expiry
	MaxPending    int              // bound on in-flight requests
	MaxTombstones int              // bound on remembered expired keys (Late detection)
	Now           func() time.Time // optional clock; defaults to time.Now
}

// Correlator pairs responses with requests by (epoch, seq). The epoch
// scopes sequence numbers: a response from an old epoch can never match a
// request issued in a newer one. All state is bounded by Config.
type Correlator struct {
	now           func() time.Time
	timeout       time.Duration
	maxPending    int
	maxTombstones int

	pending    map[key]requestState
	tombstones map[key]time.Time // expired key -> expiry time
	tombOrder  []key             // FIFO eviction order for tombstones
}

// New returns a Correlator with the given (bounded) configuration.
func New(cfg Config) *Correlator {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxPending <= 0 {
		cfg.MaxPending = 1024
	}
	if cfg.MaxTombstones <= 0 {
		cfg.MaxTombstones = cfg.MaxPending
	}
	return &Correlator{
		now:           cfg.Now,
		timeout:       cfg.Timeout,
		maxPending:    cfg.MaxPending,
		maxTombstones: cfg.MaxTombstones,
		pending:       make(map[key]requestState),
		tombstones:    make(map[key]time.Time),
	}
}

// HandleMessage dispatches a decoded frame by kind.
func (c *Correlator) HandleMessage(m Message) Event {
	if m.Kind == KindRequest {
		return c.HandleRequest(m.Epoch, m.Seq)
	}
	return c.HandleResponse(m.Epoch, m.Seq)
}

// HandleRequest starts tracking a request. A replayed request for a key
// that is still pending is reported as a duplicate and does NOT refresh
// the original start time. Once a key has completed or expired, its
// sequence number may be reused and is tracked as a fresh request.
func (c *Correlator) HandleRequest(epoch, seq uint64) Event {
	k := key{epoch: epoch, seq: seq}
	if _, ok := c.pending[k]; ok {
		return Event{Kind: EventDuplicateRequest, Epoch: epoch, Seq: seq}
	}
	if len(c.pending) >= c.maxPending {
		return Event{Kind: EventRejected, Epoch: epoch, Seq: seq}
	}
	c.removeTombstone(k) // reuse after expiry: the key lives again
	c.pending[k] = requestState{start: c.now()}
	return Event{Kind: EventAccepted, Epoch: epoch, Seq: seq}
}

// HandleResponse pairs a response with its pending request and reports
// the measured latency. Responses for expired keys are Late (never a new
// success); responses for unknown keys are Orphan.
func (c *Correlator) HandleResponse(epoch, seq uint64) Event {
	k := key{epoch: epoch, seq: seq}
	if st, ok := c.pending[k]; ok {
		delete(c.pending, k)
		return Event{Kind: EventMatched, Epoch: epoch, Seq: seq, Latency: c.now().Sub(st.start)}
	}
	if _, ok := c.tombstones[k]; ok {
		return Event{Kind: EventLate, Epoch: epoch, Seq: seq}
	}
	return Event{Kind: EventOrphan, Epoch: epoch, Seq: seq}
}

// Expire moves every request older than Timeout into the bounded
// tombstone set and returns one EventExpired per expired request.
// Call it periodically (or before processing a batch) to enforce timeouts.
func (c *Correlator) Expire() []Event {
	now := c.now()
	var expired []key
	for k, st := range c.pending {
		if now.Sub(st.start) >= c.timeout {
			expired = append(expired, k)
		}
	}
	// Sort so tombstone FIFO order (and event order) is deterministic
	// instead of depending on map iteration.
	sort.Slice(expired, func(i, j int) bool {
		return expired[i].epoch < expired[j].epoch ||
			(expired[i].epoch == expired[j].epoch && expired[i].seq < expired[j].seq)
	})
	events := make([]Event, 0, len(expired))
	for _, k := range expired {
		delete(c.pending, k)
		c.addTombstone(k, now)
		events = append(events, Event{Kind: EventExpired, Epoch: k.epoch, Seq: k.seq})
	}
	return events
}

// Pending reports the number of in-flight requests.
func (c *Correlator) Pending() int { return len(c.pending) }

func (c *Correlator) addTombstone(k key, at time.Time) {
	if _, ok := c.tombstones[k]; ok {
		return
	}
	for len(c.tombstones) >= c.maxTombstones && len(c.tombOrder) > 0 {
		oldest := c.tombOrder[0]
		c.tombOrder = c.tombOrder[1:]
		delete(c.tombstones, oldest)
	}
	c.tombstones[k] = at
	c.tombOrder = append(c.tombOrder, k)
}

func (c *Correlator) removeTombstone(k key) {
	if _, ok := c.tombstones[k]; !ok {
		return
	}
	delete(c.tombstones, k)
	for i, tk := range c.tombOrder {
		if tk == k {
			c.tombOrder = append(c.tombOrder[:i], c.tombOrder[i+1:]...)
			break
		}
	}
}
