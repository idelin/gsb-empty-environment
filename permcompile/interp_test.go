package permcompile

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeClock 是解释器使用的可控时钟。
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// 小计划全轨迹解释器：把事件、授权判定与提交按步喂给 Runtime，
// 每一步断言三态结论并核对全部可能世界的剩余配额。
type interp struct {
	t        *testing.T
	rt       *Runtime
	cp       *CompiledPlan
	session  SessionID
	nonceSeq int
}

func newInterp(t *testing.T, plan *Plan, opts ...Option) *interp {
	t.Helper()
	cp, err := Compile(plan)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	auth := func(ev Event) bool {
		return ev.Digest == cp.digest && ev.Session == "sess-A"
	}
	rt := NewRuntime(cp, "sess-A", auth, opts...)
	return &interp{t: t, rt: rt, cp: cp, session: "sess-A"}
}

func (it *interp) nonce() string {
	it.nonceSeq++
	return fmt.Sprintf("n-%d", it.nonceSeq)
}

func (it *interp) decision(gate, option string) Event {
	return Event{
		Kind:    EventDecision,
		Session: it.session,
		Digest:  it.cp.digest,
		Gate:    gate,
		Option:  option,
		Nonce:   it.nonce(),
	}
}

func (it *interp) fallback(gate string) Event {
	return Event{Kind: EventFallback, Session: it.session, Digest: it.cp.digest, Gate: gate, Nonce: it.nonce()}
}

func (it *interp) cancel(gate string) Event {
	return Event{Kind: EventCancel, Session: it.session, Digest: it.cp.digest, Gate: gate, Nonce: it.nonce()}
}

func (it *interp) ingest(evs ...Event) {
	it.t.Helper()
	var err error
	if len(evs) == 1 {
		err = it.rt.Ingest(evs[0])
	} else {
		err = it.rt.IngestBatch(evs)
	}
	if err != nil {
		it.t.Fatalf("ingest %+v: %v", evs, err)
	}
}

func (it *interp) ingestFail(ev Event, want error) {
	it.t.Helper()
	if err := it.rt.Ingest(ev); !errors.Is(err, want) {
		it.t.Fatalf("ingest err = %v, want %v", err, want)
	}
}

func req(obj ObjectID, gen Generation, lo, hi uint64) ReadRequest {
	return ReadRequest{Object: obj, Gen: gen, Range: ByteRange{Lo: lo, Hi: hi}}
}

func (it *interp) check(r ReadRequest, want Verdict) Decision {
	it.t.Helper()
	d := it.rt.Authorize(r)
	it.rt.RecordAuth(r, d)
	if d.Verdict != want {
		it.t.Fatalf("authorize %s = %s (%s), want %s; waiting=%v",
			r.Object, d.Verdict, d.Reason, want, d.Waiting)
	}
	return d
}

func (it *interp) allow(r ReadRequest) {
	it.t.Helper()
	it.check(r, VerdictAllow)
	if err := it.rt.Commit(r); err != nil {
		it.t.Fatalf("commit: %v", err)
	}
}

func (it *interp) deny(r ReadRequest) { it.check(r, VerdictDeny) }

func (it *interp) wait(r ReadRequest) []WaitEvent {
	it.t.Helper()
	d := it.check(r, VerdictWait)
	if len(d.Waiting) == 0 {
		it.t.Fatalf("wait verdict without wait events")
	}
	return d.Waiting
}

// expectWorlds 断言可能世界数量，并检查每个世界中是否包含给定叶子描述。
func (it *interp) expectWorlds(wantCount int, leaves ...string) {
	it.t.Helper()
	snap := it.rt.Snapshot()
	if len(snap) != wantCount {
		it.t.Fatalf("world count = %d, want %d", len(snap), wantCount)
	}
	joinedWorlds := make([]string, len(snap))
	for wi, ws := range snap {
		var got []string
		for _, l := range ws.Leaves {
			got = append(got, fmt.Sprintf("L%d[%s]b=%sr=%s",
				l.Leaf, l.NodeState, l.Bytes.key(), l.Reads.key()))
		}
		joined := strings.Join(got, " ")
		joinedWorlds[wi] = joined
	}
	for _, want := range leaves {
		found := false
		for _, joined := range joinedWorlds {
			if strings.Contains(joined, want) {
				found = true
				break
			}
		}
		if !found {
			it.t.Fatalf("no world contains %q; worlds: %s", want, strings.Join(joinedWorlds, " | "))
		}
	}
}

func leafGrant(obj ObjectID, gen Generation, lo, hi, maxReads uint64) *Node {
	return &Node{
		Kind:  KindLeaf,
		Grant: &Grant{Object: obj, Gen: gen, Scope: ByteRange{Lo: lo, Hi: hi}, MaxReads: maxReads},
	}
}
