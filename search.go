package netsec

import (
	"context"
	"fmt"
	"strings"
)

// Fixed identities used by the trace search. Client B deliberately reuses
// client A's process id and start time (PID reuse); only the token differs.
var (
	IdentityA    = Identity{ProcessID: 4242, StartTime: 1700000000, TokenID: 0xAAAA}
	IdentityB    = Identity{ProcessID: 4242, StartTime: 1700000000, TokenID: 0xBBBB}
	HostIdentity = Identity{ProcessID: 1, StartTime: 1, TokenID: 0xFFFF}
)

// PayloadA is the request content that must never run under any identity
// other than client A's.
var PayloadA = []byte("req-A")

const requestIDA = 1

// Alphabet of events explored by the search. Server steps keep their
// program order (read, then impersonate, then call); client events may be
// interleaved anywhere.
var Alphabet = []string{
	"A.connect",
	"A.write",
	"A.disconnect",
	"B.connect",
	"B.disconnect",
	"svc.listen",
	"svc.read",
	"svc.impersonate",
	"svc.call",
}

// world is a deterministic, replayable configuration of one pipe instance,
// two clients and one in-flight server transaction, in either the secure
// or the reviewed (vulnerable) variant.
type world struct {
	secure bool
	host   *Host

	sp *PipeInstance
	ss *Service
	st *Transaction

	vp *VulnerablePipe
	vs *VulnerableService
	vt *VulnerableTransaction

	connected  string
	wroteA     bool
	stage      int
	callDone   bool
	boundToken uint64
	lastCall   *CallOutcome
}

func newWorld(secure bool) *world {
	w := &world{secure: secure, host: NewHost(HostIdentity)}
	if secure {
		w.sp = w.host.NewInstance()
		w.ss = NewService(w.host)
	} else {
		w.vp = w.host.NewVulnerableInstance()
		w.vs = NewVulnerableService(w.host)
	}
	return w
}

func (w *world) pipeState() State {
	if w.secure {
		return w.sp.State()
	}
	return w.vp.State()
}

func (w *world) clientPipe() ClientPipe {
	if w.secure {
		return w.sp
	}
	return w.vp
}

// event applies one alphabet event if it is currently applicable, reporting
// whether anything happened.
func (w *world) event(name string) bool {
	switch name {
	case "A.connect":
		return w.connect(IdentityA, "A")
	case "B.connect":
		return w.connect(IdentityB, "B")
	case "A.write":
		if w.connected != "A" || w.wroteA {
			return false
		}
		if NewClient("A", IdentityA, w.clientPipe()).Send(requestIDA, PayloadA) != nil {
			return false
		}
		w.wroteA = true
		return true
	case "A.disconnect":
		return w.disconnect("A")
	case "B.disconnect":
		return w.disconnect("B")
	case "svc.listen":
		if w.pipeState() != StateDisconnected {
			return false
		}
		return w.clientPipe().Listen() == nil
	case "svc.read":
		if w.stage != 0 {
			return false
		}
		if w.secure {
			t, err := w.ss.Begin(w.sp)
			if err != nil {
				return false
			}
			w.st = t
		} else {
			t, err := w.vs.Begin(w.vp)
			if err != nil {
				return false
			}
			w.vt = t
		}
		w.stage = 1
		return true
	case "svc.impersonate":
		if w.stage != 1 {
			return false
		}
		var err error
		if w.secure {
			err = w.st.Impersonate()
		} else {
			err = w.vt.Impersonate()
		}
		if err != nil {
			w.stage = 3
			return true
		}
		w.stage = 2
		return true
	case "svc.call":
		if w.stage != 2 || w.callDone {
			return false
		}
		w.callDone = true
		var (
			out CallOutcome
			err error
		)
		if w.secure {
			out, err = w.st.Call(context.Background())
		} else {
			out, err = w.vt.Call()
		}
		if err != nil {
			return true
		}
		w.boundToken = out.Identity.TokenID
		w.lastCall = &out
		return true
	}
	return false
}

func (w *world) connect(id Identity, who string) bool {
	if w.connected != "" || w.pipeState() != StateListening {
		return false
	}
	if NewClient(who, id, w.clientPipe()).Connect() != nil {
		return false
	}
	w.connected = who
	return true
}

func (w *world) disconnect(who string) bool {
	if w.connected != who {
		return false
	}
	w.clientPipe().Disconnect()
	w.connected = ""
	return true
}

// violation reports whether request A's payload was executed under an
// identity other than client A's (another client, or the host process).
func (w *world) violation() bool {
	if w.lastCall == nil {
		return false
	}
	return string(w.lastCall.Payload) == string(PayloadA) && w.lastCall.Identity != IdentityA
}

// key is the canonical abstract state used to deduplicate the search.
func (w *world) key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%v|%s|wrote:%t|stage:%d|call:%t|tok:%x",
		w.pipeState(), w.connected, w.wroteA, w.stage, w.callDone, w.boundToken)
	if w.secure {
		fmt.Fprintf(&b, "|gen:%d", w.sp.Generation())
	}
	return b.String()
}

// replay rebuilds a world by applying a trace from scratch, keeping the
// search free of mutable shared state.
func replay(secure bool, trace []string) *world {
	w := newWorld(secure)
	for _, ev := range trace {
		w.event(ev)
	}
	return w
}

// SearchShortestViolation breadth-first searches interleavings of the
// alphabet for the shortest trace in which request A's payload is called
// under a foreign identity. It returns the trace and whether one was found
// within maxDepth events.
func SearchShortestViolation(secure bool, maxDepth int) ([]string, bool) {
	type node struct {
		trace []string
	}
	visited := map[string]bool{newWorld(secure).key(): true}
	queue := []node{{}}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, ev := range Alphabet {
			w := replay(secure, n.trace)
			if !w.event(ev) {
				continue
			}
			next := append(append([]string{}, n.trace...), ev)
			if w.violation() {
				return next, true
			}
			if len(next) >= maxDepth {
				continue
			}
			k := w.key()
			if visited[k] {
				continue
			}
			visited[k] = true
			queue = append(queue, node{trace: next})
		}
	}
	return nil, false
}
