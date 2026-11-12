package queryplan

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// scriptedResponse is what a pausableUpstream returns once a call is released.
type scriptedResponse struct {
	resp *Response
	err  error
}

// callRecord logs one upstream invocation.
type callRecord struct {
	generation uint64
	candidate  string
	query      Query
}

// pausableUpstream is a deterministic, clock-free test upstream.
//
// Calls can be paused either by delegation generation (PauseGeneration) or by
// individual candidate name (PauseCandidate). A paused call blocks inside
// Query until ReleaseAll (or its context ending) unblocks it, which lets
// tests arrange exact interleavings such as "every old-generation answer
// arrives late" without sleeping on real I/O.
type pausableUpstream struct {
	mu sync.Mutex

	script map[string]scriptedResponse

	pausedGenerations map[uint64]bool
	pausedCandidates  map[string]bool

	// gates holds one signal chan per currently blocked call.
	gates map[chan struct{}]struct{}

	// blockCond signals changes to blocked/totalBlocked.
	blockCond    *sync.Cond
	blocked      int
	totalBlocked int

	calls   []callRecord
	callsMu sync.Mutex
}

func newPausableUpstream() *pausableUpstream {
	u := &pausableUpstream{
		script:            make(map[string]scriptedResponse),
		pausedGenerations: make(map[uint64]bool),
		pausedCandidates:  make(map[string]bool),
		gates:             make(map[chan struct{}]struct{}),
	}
	u.blockCond = sync.NewCond(&u.mu)
	return u
}

// scriptCandidate sets the response for a candidate; unset scripts answer.
func (u *pausableUpstream) scriptCandidate(name string, resp *Response, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.script[name] = scriptedResponse{resp: resp, err: err}
}

func (u *pausableUpstream) PauseGeneration(generation uint64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pausedGenerations[generation] = true
}

func (u *pausableUpstream) PauseCandidate(name string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pausedCandidates[name] = true
}

// ReleaseAll unblocks every paused call. Safe to call when nothing is blocked.
func (u *pausableUpstream) ReleaseAll() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for gate := range u.gates {
		close(gate)
		delete(u.gates, gate)
	}
}

// BlockedCount reports how many calls are currently parked inside Query.
func (u *pausableUpstream) BlockedCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.blocked
}

// waitBlocked parks until at least n calls are blocked at the same time,
// failing on timeout so tests stay deterministic without fixed sleeps.
func (u *pausableUpstream) waitBlocked(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	u.mu.Lock()
	defer u.mu.Unlock()
	for u.blocked < n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d blocked upstream calls, have %d", n, u.blocked)
		}
		u.blockCond.Wait()
	}
}

// waitTotalBlocked parks until at least n calls have parked at some point,
// even if some have already been unblocked by context cancellation.
func (u *pausableUpstream) waitTotalBlocked(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	u.mu.Lock()
	defer u.mu.Unlock()
	for u.totalBlocked < n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d total blocked calls, have %d", n, u.totalBlocked)
		}
		u.blockCond.Wait()
	}
}

func (u *pausableUpstream) Calls() []callRecord {
	u.callsMu.Lock()
	defer u.callsMu.Unlock()
	out := make([]callRecord, len(u.calls))
	copy(out, u.calls)
	return out
}

func (u *pausableUpstream) callsFor(generation uint64) int {
	n := 0
	for _, c := range u.Calls() {
		if c.generation == generation {
			n++
		}
	}
	return n
}

func (u *pausableUpstream) Query(ctx context.Context, c Candidate, q Query) (*Response, error) {
	gen := genFromAddr(c.Addr)

	u.callsMu.Lock()
	u.calls = append(u.calls, callRecord{generation: gen, candidate: c.Name, query: q})
	u.callsMu.Unlock()

	gate := make(chan struct{})
	u.mu.Lock()
	if u.pausedGenerations[gen] || u.pausedCandidates[c.Name] {
		u.gates[gate] = struct{}{}
		u.blocked++
		u.totalBlocked++
		u.blockCond.Broadcast()
		u.mu.Unlock()

		select {
		case <-gate:
		case <-ctx.Done():
			u.mu.Lock()
			u.blocked--
			u.blockCond.Broadcast()
			delete(u.gates, gate)
			u.mu.Unlock()
			return nil, ctx.Err()
		}

		u.mu.Lock()
		u.blocked--
		u.blockCond.Broadcast()
		delete(u.gates, gate)
	}
	u.mu.Unlock()

	if s, ok := u.script[c.Name]; ok {
		return s.resp, s.err
	}
	return &Response{Answer: "answer-from-" + c.Name}, nil
}

// genFromAddr extracts the generation encoded in an address like "gen2-a".
func genFromAddr(addr string) uint64 {
	if !strings.HasPrefix(addr, "gen") {
		return 0
	}
	rest := strings.TrimPrefix(addr, "gen")
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		rest = rest[:i]
	}
	gen, err := strconv.ParseUint(rest, 10, 64)
	if err != nil {
		return 0
	}
	return gen
}

func delegation(gen uint64, names ...string) Delegation {
	candidates := make([]Candidate, len(names))
	for i, name := range names {
		candidates[i] = Candidate{
			Name: name,
			Addr: "gen" + strconv.FormatUint(gen, 10) + "-" + name,
		}
	}
	return Delegation{Generation: gen, Candidates: candidates}
}

func testPlanner(t *testing.T, seed Delegation, timeout time.Duration) (*Planner, *pausableUpstream) {
	t.Helper()
	upstream := newPausableUpstream()
	planner := NewPlanner(upstream, seed, WithCandidateTimeout(timeout))
	return planner, upstream
}
