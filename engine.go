package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// queuedMsg is one entry in a bounded channel queue.
type queuedMsg struct {
	Seq int
	Tag string
}

// endpointRuntime holds the mutable state of one endpoint instance.
type endpointRuntime struct {
	state    string
	terminal bool
	// received is the set of sequence numbers dequeued from the incoming
	// channel; membership, not order, is what the success binding needs.
	received map[int]bool
	// tagAt records the message tag delivered at each received sequence.
	tagAt map[int]string
	// evicted records sequence numbers that entered the incoming channel and
	// were then discarded by a drop-old overflow. A drop-new discard never
	// enters the queue and is not recorded here, because it cannot displace a
	// message the endpoint was entitled to receive.
	evicted map[int]bool
}

// channelRuntime holds the mutable state of one bounded channel.
type channelRuntime struct {
	queue []queuedMsg
	// nextSeq is the sequence number of the next send attempt. Sends that
	// are dropped still consume a number.
	nextSeq int
}

// globalState is one reachable configuration of the whole system.
type globalState struct {
	endpoints map[string]*endpointRuntime
	channels  map[string]*channelRuntime
}

func newInitialState(spec Spec) globalState {
	gs := globalState{
		endpoints: make(map[string]*endpointRuntime, len(spec.Endpoints)),
		channels:  make(map[string]*channelRuntime, len(spec.Channels)),
	}
	for _, ch := range spec.Channels {
		gs.channels[ch.Name] = &channelRuntime{nextSeq: 1}
	}
	for _, ep := range spec.Endpoints {
		gs.endpoints[ep.Name] = &endpointRuntime{
			state:    ep.Init,
			received: map[int]bool{},
			tagAt:    map[int]string{},
			evicted:  map[int]bool{},
		}
	}
	return gs
}

func (gs globalState) clone() globalState {
	out := globalState{
		endpoints: make(map[string]*endpointRuntime, len(gs.endpoints)),
		channels:  make(map[string]*channelRuntime, len(gs.channels)),
	}
	for name, rt := range gs.endpoints {
		recCopy := make(map[int]bool, len(rt.received))
		for seq := range rt.received {
			recCopy[seq] = true
		}
		tagCopy := make(map[int]string, len(rt.tagAt))
		for seq, tag := range rt.tagAt {
			tagCopy[seq] = tag
		}
		evictCopy := make(map[int]bool, len(rt.evicted))
		for seq := range rt.evicted {
			evictCopy[seq] = true
		}
		out.endpoints[name] = &endpointRuntime{
			state:    rt.state,
			terminal: rt.terminal,
			received: recCopy,
			tagAt:    tagCopy,
			evicted:  evictCopy,
		}
	}
	for name, rt := range gs.channels {
		out.channels[name] = &channelRuntime{
			queue:   append([]queuedMsg(nil), rt.queue...),
			nextSeq: rt.nextSeq,
		}
	}
	return out
}

// key produces a canonical, order-independent encoding so semantically equal
// states collapse in the visited set.
func (gs globalState) key() string {
	epNames := make([]string, 0, len(gs.endpoints))
	for name := range gs.endpoints {
		epNames = append(epNames, name)
	}
	sort.Strings(epNames)

	var b strings.Builder
	for _, name := range epNames {
		rt := gs.endpoints[name]
		seqs := make([]int, 0, len(rt.received))
		for seq := range rt.received {
			seqs = append(seqs, seq)
		}
		sort.Ints(seqs)
		b.WriteString("E:")
		b.WriteString(name)
		b.WriteString("|")
		b.WriteString(rt.state)
		b.WriteString("|R[")
		for i, seq := range seqs {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%d", seq)
		}
		b.WriteString("];")
	}

	chNames := make([]string, 0, len(gs.channels))
	for name := range gs.channels {
		chNames = append(chNames, name)
	}
	sort.Strings(chNames)
	for _, name := range chNames {
		rt := gs.channels[name]
		b.WriteString("C:")
		b.WriteString(name)
		b.WriteString("|Q[")
		for i, msg := range rt.queue {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%d:%s", msg.Seq, msg.Tag)
		}
		b.WriteString("]|N")
		fmt.Fprintf(&b, "%d;", rt.nextSeq)
	}
	return b.String()
}
