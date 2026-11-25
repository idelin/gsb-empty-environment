package revco

import (
	"errors"
	"reflect"
	"sort"
	"sync"
)

// ErrWatermarkRegress is returned when a channel watermark moves backwards;
// watermarks are required to be monotonic.
var ErrWatermarkRegress = errors.New("revco: channel watermark cannot regress")

// Graph is the small, self-contained event-graph interpreter. It ingests
// generational kernel events and per-channel watermarks and materializes the
// holds provable from committed evidence.
type Graph struct {
	mu         sync.RWMutex
	events     map[ChanID]map[Seq]Event
	watermarks map[ChanID]Seq

	holds      map[HoldKey]*Hold
	pending    map[HoldKey][]*Event
	nonapplied []Event
	conflicts  []EventConflict
}

// NewGraph creates an empty interpreter.
func NewGraph() *Graph {
	return &Graph{
		events:     map[ChanID]map[Seq]Event{},
		watermarks: map[ChanID]Seq{},
	}
}

// Observe records an event. Events may arrive out of order and may arrive
// before their channel's watermark reaches them; such early events are buffered
// and ignored as evidence until the watermark catches up. Re-delivery of the
// identical (channel, seq) event is a no-op; a different payload at the same
// position reports EventConflict.
func (g *Graph) Observe(ev Event) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	bucket := g.events[ev.Channel]
	if bucket == nil {
		bucket = map[Seq]Event{}
		g.events[ev.Channel] = bucket
	}
	if prev, ok := bucket[ev.Seq]; ok {
		if !reflect.DeepEqual(prev, ev) {
			c := EventConflict{Channel: ev.Channel, Seq: ev.Seq}
			g.conflicts = append(g.conflicts, c)
			return c
		}
		return nil
	}
	bucket[ev.Seq] = ev
	g.rebuild()
	return nil
}

// Advance sets the monotonic watermark of channel ch. All events on that
// channel with sequence <= w are now guaranteed complete.
func (g *Graph) Advance(ch ChanID, w Seq) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if cur := g.watermarks[ch]; w < cur {
		return ErrWatermarkRegress
	}
	if g.watermarks[ch] == w {
		return nil
	}
	g.watermarks[ch] = w
	g.rebuild()
	return nil
}

// Snapshot is the materialized, evidence-bounded view of the graph.
type Snapshot struct {
	Holds      map[HoldKey]*Hold
	Watermarks map[ChanID]Seq
	Nonapplied []Event // committed events whose effect is not yet resolvable
	Early      []Event // observed events still above their channel watermark
	Conflicts  []EventConflict
}

// Snapshot returns a deep copy of the current materialized state.
func (g *Graph) Snapshot() Snapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()

	holds := make(map[HoldKey]*Hold, len(g.holds))
	for k, h := range g.holds {
		cp := *h
		cp.Rights = cloneRights(h.Rights)
		cp.Sources = append([]HoldKey(nil), h.Sources...)
		if h.Msg != nil {
			m := *h.Msg
			cp.Msg = &m
		}
		holds[k] = &cp
	}
	wm := make(map[ChanID]Seq, len(g.watermarks))
	for k, v := range g.watermarks {
		wm[k] = v
	}
	return Snapshot{
		Holds:      holds,
		Watermarks: wm,
		Nonapplied: append([]Event(nil), g.nonapplied...),
		Early:      append([]Event(nil), g.earlyEvents()...),
		Conflicts:  append([]EventConflict(nil), g.conflicts...),
	}
}

func (g *Graph) earlyEvents() []Event {
	var out []Event
	for ch, bucket := range g.events {
		w := g.watermarks[ch]
		for seq, ev := range bucket {
			if seq > w {
				out = append(out, ev)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Channel != out[j].Channel {
			return out[i].Channel < out[j].Channel
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}

// sendDestChan returns the destination channel recorded on the send event that
// birthed msg, or "" when no such event has been observed.
func (g *Graph) sendDestChan(msg HoldKey) ChanID {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, bucket := range g.events {
		for _, ev := range bucket {
			if ev.Type == EvSend && ev.newKey() == msg && ev.DestChan != "" {
				return ev.DestChan
			}
		}
	}
	return ""
}

func (g *Graph) rebuild() {
	g.holds = map[HoldKey]*Hold{}
	g.pending = map[HoldKey][]*Event{}
	g.nonapplied = nil

	var committed []Event
	for ch, bucket := range g.events {
		w := g.watermarks[ch]
		for seq, ev := range bucket {
			if seq <= w {
				committed = append(committed, ev)
			}
		}
	}
	sort.SliceStable(committed, func(i, j int) bool {
		if committed[i].Channel != committed[j].Channel {
			return committed[i].Channel < committed[j].Channel
		}
		return committed[i].Seq < committed[j].Seq
	})

	progress := true
	queued := make(map[ChanID]map[Seq]bool)
	for progress && len(committed) > 0 {
		progress = false
		for i := range committed {
			ev := committed[i]
			b := queued[ev.Channel]
			if b != nil && b[ev.Seq] {
				continue
			}
			if g.apply(ev) {
				if queued[ev.Channel] == nil {
					queued[ev.Channel] = map[Seq]bool{}
				}
				queued[ev.Channel][ev.Seq] = true
				progress = true
			}
		}
	}

	seen := map[ChanID]map[Seq]bool{}
	for _, evs := range g.pending {
		for _, ev := range evs {
			if seen[ev.Channel] == nil {
				seen[ev.Channel] = map[Seq]bool{}
			}
			if !seen[ev.Channel][ev.Seq] {
				seen[ev.Channel][ev.Seq] = true
				g.nonapplied = append(g.nonapplied, *ev)
			}
		}
	}
	sort.SliceStable(g.nonapplied, func(i, j int) bool {
		if g.nonapplied[i].Channel != g.nonapplied[j].Channel {
			return g.nonapplied[i].Channel < g.nonapplied[j].Channel
		}
		return g.nonapplied[i].Seq < g.nonapplied[j].Seq
	})
}

// apply materializes one committed event. It reports false when a referenced
// source hold is still unknown, in which case the event stays queued for a
// later fixpoint pass.
func (g *Graph) apply(ev Event) bool {
	switch ev.Type {
	case EvIssue:
		g.birth(ev, nil, ev.Range, rightsSet(ev.Rights), ev.Owner, nil)
		return true
	case EvCopy, EvSlice, EvDerive:
		src, ok := g.holds[ev.srcKey()]
		if !ok {
			g.stall(ev)
			return false
		}
		r := ev.Range
		if r.Empty() {
			r = src.Range
		} else if ev.Type == EvSlice {
			r = intersect(r, src.Range)
		}
		rights := rightsSet(ev.Rights)
		if len(rights) == 0 {
			rights = cloneRights(src.Rights)
		} else {
			rights = intersectRights(rights, src.Rights)
		}
		g.birth(ev, []HoldKey{src.Key}, r, rights, ev.Owner, nil)
		return true
	case EvSend:
		src, ok := g.holds[ev.srcKey()]
		if !ok {
			g.stall(ev)
			return false
		}
		r := ev.Range
		if r.Empty() {
			r = src.Range
		}
		rights := rightsSet(ev.Rights)
		if len(rights) == 0 {
			rights = cloneRights(src.Rights)
		} else {
			rights = intersectRights(rights, src.Rights)
		}
		g.birth(ev, []HoldKey{src.Key}, r, rights, ev.Receiver, &Message{
			Sender:   ev.Owner,
			Receiver: ev.Receiver,
		})
		return true
	case EvReceive:
		msg, ok := g.holds[ev.srcKey()]
		if !ok {
			g.stall(ev)
			return false
		}
		if msg.Msg == nil {
			// A stale receive for an already consumed message has no effect.
			return true
		}
		received := &Hold{
			Key:     ev.newKey(),
			Object:  msg.Object,
			Range:   msg.Range,
			Rights:  cloneRights(msg.Rights),
			Owner:   ev.Owner,
			Sources: []HoldKey{msg.Key},
		}
		g.holds[ev.newKey()] = received
		if !msg.Dead {
			msg.Msg = nil
		} else {
			// The in-flight right was revoked before collection: the delivered hold
			// is born already dead, never granting access.
			received.Dead = true
			received.DeadReason = "revoked-before-receive"
			msg.Msg = nil
		}
		return true
	case EvClose, EvRevoke:
		target, ok := g.holds[ev.srcKey()]
		if !ok {
			g.stall(ev)
			return false
		}
		if !target.Dead {
			target.Dead = true
			target.DeadReason = string(ev.Type)
		}
		return true
	default:
		return true
	}
}

func (g *Graph) birth(ev Event, sources []HoldKey, r Range, rights map[Right]bool, owner Owner, msg *Message) {
	key := ev.newKey()
	g.holds[key] = &Hold{
		Key:     key,
		Object:  ev.Object,
		Range:   r,
		Rights:  rights,
		Owner:   owner,
		Sources: append([]HoldKey(nil), sources...),
		Msg:     msg,
	}
}

func (g *Graph) stall(ev Event) {
	key := ev.srcKey()
	g.pending[key] = append(g.pending[key], &ev)
}
