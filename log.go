// Package viewbuilder maintains a materialized view over an append-only
// change log. A view is built by scanning a fixed snapshot and then
// replaying the changes that arrived while the scan was running; once the
// replay catches up to the snapshot's switch sequence the new view
// atomically replaces the old one.
package viewbuilder

import "sync"

// Event is a single mutation of the source data. Seq is assigned by the
// Log, starts at 1, and is gapless. Del marks a deletion; delete events
// are preserved end to end so the view can drop the key.
type Event struct {
	Seq   uint64
	Key   string
	Value []byte
	Del   bool
}

// update is delivered in-band on a subscription channel: either a change
// event or a barrier marking that every event that existed when the
// subscription started has been delivered.
type update struct {
	ev      Event
	barrier bool
}

// Log is the source of truth: an append-only event log that can serve a
// consistent snapshot plus a change stream, and that expects a switch
// acknowledgement once a view built on a snapshot goes live.
type Log struct {
	mu       sync.Mutex
	events   []Event // events[i].Seq == i+1
	acked    uint64  // highest switch seq acknowledged by a view
	subs     map[chan update]struct{}
	dropAcks int                   // fault injection: acks to silently lose
	jumble   func([]Event) []Event // test hook: reorder the replay batch
	onSnap   func()                // test hook: run inside the scan window
}

func NewLog() *Log {
	return &Log{subs: make(map[chan update]struct{})}
}

// SetJumble installs a hook that reorders each replay batch before
// delivery, simulating out-of-order change transport.
func (l *Log) SetJumble(f func([]Event) []Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.jumble = f
}

// SetSnapshotHook installs a hook that runs after the snapshot's switch
// sequence is fixed but before Snapshot returns, so tests can append
// events that land inside the scan window.
func (l *Log) SetSnapshotHook(f func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onSnap = f
}

// DropAcks makes the next n switch acknowledgements get lost.
func (l *Log) DropAcks(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropAcks += n
}

func (l *Log) append(key string, val []byte, del bool) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	ev := Event{Seq: uint64(len(l.events)) + 1, Key: key, Value: val, Del: del}
	l.events = append(l.events, ev)
	for ch := range l.subs {
		select {
		case ch <- update{ev: ev}:
		default:
			// Slow consumer: drop the subscription. The consumer is
			// expected to reconnect and replay from the last ack.
			close(ch)
			delete(l.subs, ch)
		}
	}
	return ev.Seq
}

// Append records a put and returns its sequence number.
func (l *Log) Append(key string, val []byte) uint64 { return l.append(key, val, false) }

// Delete records a deletion and returns its sequence number.
func (l *Log) Delete(key string) uint64 { return l.append(key, nil, true) }

// Seq returns the current high-water mark: the sequence number of the
// most recently appended event.
func (l *Log) Seq() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return uint64(len(l.events))
}

// Snapshot returns a consistent materialization of the log together with
// the switch sequence: every event with Seq <= switchSeq is reflected in
// the returned map.
func (l *Log) Snapshot() (map[string][]byte, uint64) {
	l.mu.Lock()
	data := make(map[string][]byte, len(l.events))
	for _, ev := range l.events {
		if ev.Del {
			delete(data, ev.Key)
		} else {
			data[ev.Key] = ev.Value
		}
	}
	switchSeq := uint64(len(l.events))
	hook := l.onSnap
	l.mu.Unlock()
	if hook != nil {
		hook()
	}
	return data, switchSeq
}

// subscribe returns a channel that first replays every event the view
// might not have applied yet (from the last acknowledged switch seq, or
// fromSeq, whichever is older), then a barrier, then live events. The
// replay-from-ack behavior is what makes a lost switch acknowledgement
// safe: the events are simply delivered again and the consumer dedupes
// by sequence number.
func (l *Log) subscribe(fromSeq uint64) chan update {
	l.mu.Lock()
	defer l.mu.Unlock()
	start := fromSeq
	if l.acked+1 < start {
		start = l.acked + 1
	}
	ch := make(chan update, 1024)
	batch := make([]Event, 0, uint64(len(l.events))-start+1)
	for seq := start; seq <= uint64(len(l.events)); seq++ {
		batch = append(batch, l.events[seq-1])
	}
	if l.jumble != nil && len(batch) > 0 {
		batch = l.jumble(batch)
	}
	for _, ev := range batch {
		ch <- update{ev: ev}
	}
	ch <- update{barrier: true}
	l.subs[ch] = struct{}{}
	return ch
}

func (l *Log) unsubscribe(ch chan update) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.subs, ch)
}

// ackSwitch records that the view built on the given switch sequence is
// now live. Under fault injection the ack is silently lost, in which case
// the next subscription replays the unacknowledged events.
func (l *Log) ackSwitch(seq uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dropAcks > 0 {
		l.dropAcks--
		return
	}
	if seq > l.acked {
		l.acked = seq
	}
}

// closeSubs drops every live subscription, simulating a connection
// failure. Consumers are expected to reconnect and resubscribe.
func (l *Log) closeSubs() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ch := range l.subs {
		close(ch)
		delete(l.subs, ch)
	}
}

// recompute derives the full state at the given sequence from scratch by
// replaying the raw log. It is the reference every public query is
// checked against.
func (l *Log) recompute(seq uint64) map[string][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	if seq > uint64(len(l.events)) {
		seq = uint64(len(l.events))
	}
	data := make(map[string][]byte, seq)
	for i := uint64(1); i <= seq; i++ {
		ev := l.events[i-1]
		if ev.Del {
			delete(data, ev.Key)
		} else {
			data[ev.Key] = ev.Value
		}
	}
	return data
}
