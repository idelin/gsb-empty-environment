package chanx

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- History model -----------------------------------------------------

type opKind int

const (
	opSend opKind = iota
	opRecv
	opClose
)

type result int

const (
	resOK result = iota
	resClosed
	resCancelled
)

// op is one recorded invocation/response pair. start/end are logical
// timestamps from a single atomic counter, so real-time precedence is
// op[i] before op[j] iff op[i].end < op[j].start.
type op struct {
	kind       opKind
	arg        int // Send: value id
	val        int // Recv: received value id (-1 if none)
	res        result
	start, end int64
}

func (o op) String() string {
	ts := func(r result) string {
		switch r {
		case resOK:
			return "ok"
		case resClosed:
			return "closed"
		default:
			return "cancelled"
		}
	}
	switch o.kind {
	case opSend:
		return fmt.Sprintf("Send(%d)->%s", o.arg, ts(o.res))
	case opRecv:
		if o.res == resOK {
			return fmt.Sprintf("Recv()->%d", o.val)
		}
		return fmt.Sprintf("Recv()->%s", ts(o.res))
	default:
		return "Close()"
	}
}

// ---- Linearizability checker -------------------------------------------
//
// Abstract model: a FIFO queue of the given capacity plus a closed flag.
// Capacity 0 is modelled with a one-slot "pending" rendezvous state: a
// successful Send parks its value in pending, and only the Recv carrying
// that exact value may clear it (the pair is atomic in the real
// implementation, so nothing observable may intervene except no-ops and
// Close, which commute with it).
//
// Cancelled operations are no-ops in the model: per the channel
// contract, a cancelled Send's value is never delivered and a cancelled
// Recv never consumes. The checker enumerates, by backtracking with
// memoization, every sequential permutation of the history that respects
// real-time precedence, and accepts iff some permutation is a legal
// execution of the abstract model.

type linKey struct {
	mask   uint64
	queue  string
	closed bool
	pend   int
}

func applyOp(o op, queue []int, closed bool, pend, capacity int) ([]int, bool, int, bool) {
	switch o.kind {
	case opSend:
		switch o.res {
		case resOK:
			if closed {
				return nil, false, 0, false
			}
			if capacity == 0 {
				if pend != -1 {
					return nil, false, 0, false
				}
				return queue, closed, o.arg, true
			}
			if len(queue) >= capacity {
				return nil, false, 0, false
			}
			nq := make([]int, len(queue)+1)
			copy(nq, queue)
			nq[len(queue)] = o.arg
			return nq, closed, pend, true
		case resClosed:
			if !closed {
				return nil, false, 0, false
			}
			return queue, closed, pend, true
		default: // cancelled: no-op
			return queue, closed, pend, true
		}
	case opRecv:
		switch o.res {
		case resOK:
			if capacity == 0 {
				if pend != o.val {
					return nil, false, 0, false
				}
				return queue, closed, -1, true
			}
			if len(queue) == 0 || queue[0] != o.val {
				return nil, false, 0, false
			}
			return queue[1:], closed, pend, true
		case resClosed:
			if !closed || len(queue) > 0 || pend != -1 {
				return nil, false, 0, false
			}
			return queue, closed, pend, true
		default: // cancelled: no-op
			return queue, closed, pend, true
		}
	default: // opClose: idempotent, always legal
		return queue, true, pend, true
	}
}

func linearizable(ops []op, capacity int) bool {
	n := len(ops)
	if n > 60 {
		panic("history too large for exhaustive checker")
	}
	// pred[i]: bitmask of ops that must be placed before op i.
	pred := make([]uint64, n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j && ops[j].end < ops[i].start {
				pred[i] |= 1 << uint(j)
			}
		}
	}
	full := uint64(1)<<uint(n) - 1
	memo := map[linKey]bool{}
	var dfs func(mask uint64, queue []int, closed bool, pend int) bool
	dfs = func(mask uint64, queue []int, closed bool, pend int) bool {
		if mask == full {
			return true
		}
		k := linKey{mask, fmt.Sprint(queue), closed, pend}
		if v, ok := memo[k]; ok {
			return v
		}
		res := false
		for i := 0; i < n && !res; i++ {
			bit := uint64(1) << uint(i)
			if mask&bit != 0 || pred[i]&^mask != 0 {
				continue
			}
			nq, nc, np, ok := applyOp(ops[i], queue, closed, pend, capacity)
			if !ok {
				continue
			}
			res = dfs(mask|bit, nq, nc, np)
		}
		memo[k] = res
		return res
	}
	return dfs(0, nil, false, -1)
}

// ---- Checker self-tests -------------------------------------------------

func TestCheckerAcceptsAndRejects(t *testing.T) {
	// Simple legal rendezvous: overlapping Send/Recv pair.
	good := []op{
		{kind: opSend, arg: 1, res: resOK, start: 1, end: 4},
		{kind: opRecv, val: 1, res: resOK, start: 2, end: 3},
	}
	if !linearizable(good, 0) {
		t.Fatal("rejected legal rendezvous history")
	}
	// Recv of a value that was never sent.
	bad := []op{
		{kind: opRecv, val: 7, res: resOK, start: 1, end: 2},
	}
	if linearizable(bad, 0) {
		t.Fatal("accepted recv of unsent value")
	}
	// Recv strictly before its Send on a capacity-0 channel: impossible.
	bad2 := []op{
		{kind: opRecv, val: 1, res: resOK, start: 1, end: 2},
		{kind: opSend, arg: 1, res: resOK, start: 3, end: 4},
	}
	if linearizable(bad2, 0) {
		t.Fatal("accepted recv strictly before send (cap 0)")
	}
	// Same history is legal with capacity 1? No: buffer starts empty.
	if linearizable(bad2, 1) {
		t.Fatal("accepted recv strictly before send (cap 1)")
	}
	// Send strictly before Recv is legal with capacity 1 (buffering).
	good2 := []op{
		{kind: opSend, arg: 1, res: resOK, start: 1, end: 2},
		{kind: opRecv, val: 1, res: resOK, start: 3, end: 4},
	}
	if !linearizable(good2, 1) {
		t.Fatal("rejected legal buffered history")
	}
	// Successful Send strictly after Close: impossible.
	bad3 := []op{
		{kind: opClose, start: 1, end: 2},
		{kind: opSend, arg: 1, res: resOK, start: 3, end: 4},
	}
	if linearizable(bad3, 1) {
		t.Fatal("accepted send completing after close")
	}
	// FIFO violation: two buffered sends received out of order.
	bad4 := []op{
		{kind: opSend, arg: 1, res: resOK, start: 1, end: 2},
		{kind: opSend, arg: 2, res: resOK, start: 3, end: 4},
		{kind: opRecv, val: 2, res: resOK, start: 5, end: 6},
		{kind: opRecv, val: 1, res: resOK, start: 7, end: 8},
	}
	if linearizable(bad4, 2) {
		t.Fatal("accepted FIFO violation")
	}
}

// ---- Concurrent workload + history validation ---------------------------

type recorder struct {
	clk atomic.Int64
	mu  sync.Mutex
	ops []op
}

func (r *recorder) begin() int64 { return r.clk.Add(1) }

func (r *recorder) end(kind opKind, arg, val int, res result, start int64) {
	end := r.clk.Add(1)
	r.mu.Lock()
	r.ops = append(r.ops, op{kind: kind, arg: arg, val: val, res: res, start: start, end: end})
	r.mu.Unlock()
}

// runWorkload drives a small randomized concurrent workload against a
// channel of the given capacity and records the resulting history.
func runWorkload(capacity, nSenders, nReceivers int, rng *rand.Rand) []op {
	c := New[int](capacity)
	var rec recorder
	var senders, all sync.WaitGroup
	for i := 0; i < nSenders; i++ {
		senders.Add(1)
		all.Add(1)
		grng := rand.New(rand.NewSource(rng.Int63()))
		go func(id int) {
			defer senders.Done()
			defer all.Done()
			time.Sleep(time.Duration(grng.Intn(300)) * time.Microsecond)
			ctx := context.Background()
			if grng.Intn(2) == 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Duration(grng.Intn(500))*time.Microsecond)
				defer cancel()
			}
			start := rec.begin()
			err := c.Send(ctx, id)
			res := resOK
			switch {
			case errors.Is(err, ErrClosed):
				res = resClosed
			case err != nil:
				res = resCancelled
			}
			rec.end(opSend, id, -1, res, start)
		}(i)
	}
	for i := 0; i < nReceivers; i++ {
		all.Add(1)
		grng := rand.New(rand.NewSource(rng.Int63()))
		go func() {
			defer all.Done()
			for {
				time.Sleep(time.Duration(grng.Intn(300)) * time.Microsecond)
				start := rec.begin()
				v, err := c.Recv(context.Background())
				if errors.Is(err, ErrClosed) {
					rec.end(opRecv, -1, -1, resClosed, start)
					return
				}
				rec.end(opRecv, -1, v, resOK, start)
			}
		}()
	}
	senders.Wait()
	// One late send that must observe the closed channel.
	start := rec.begin()
	c.Close()
	rec.end(opClose, -1, -1, resOK, start)
	start = rec.begin()
	err := c.Send(context.Background(), 999)
	if !errors.Is(err, ErrClosed) {
		panic("late send did not observe close")
	}
	rec.end(opSend, 999, -1, resClosed, start)
	all.Wait()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	ops := append([]op(nil), rec.ops...)
	sort.Slice(ops, func(i, j int) bool { return ops[i].start < ops[j].start })
	return ops
}

// validateHistory checks history-level invariants that the sequential
// checker does not cover: unique ids, no loss, no duplication, and the
// cancellation guarantees.
func validateHistory(t *testing.T, ops []op) {
	t.Helper()
	sentOK := map[int]bool{}
	sentCancelled := map[int]bool{}
	recvCount := map[int]int{}
	for _, o := range ops {
		switch {
		case o.kind == opSend && o.res == resOK:
			if sentOK[o.arg] {
				t.Fatalf("value %d sent successfully twice", o.arg)
			}
			sentOK[o.arg] = true
		case o.kind == opSend && o.res == resCancelled:
			sentCancelled[o.arg] = true
		case o.kind == opRecv && o.res == resOK:
			recvCount[o.val]++
		}
	}
	for v, n := range recvCount {
		if n > 1 {
			t.Fatalf("value %d received %d times (duplication)", v, n)
		}
		if !sentOK[v] {
			t.Fatalf("value %d received but never successfully sent", v)
		}
	}
	for v := range sentCancelled {
		if recvCount[v] > 0 {
			t.Fatalf("cancelled send of %d was received", v)
		}
	}
	// No loss: every successfully sent value is eventually received
	// (receivers drain until the closed marker).
	for v := range sentOK {
		if recvCount[v] == 0 {
			t.Fatalf("value %d sent successfully but never received (loss)", v)
		}
	}
}

func testLinearizableWorkload(t *testing.T, capacity int) {
	t.Helper()
	for iter := 0; iter < 5; iter++ {
		rng := rand.New(rand.NewSource(int64(iter)*7919 + int64(capacity)))
		ops := runWorkload(capacity, 4, 2, rng)
		validateHistory(t, ops)
		if !linearizable(ops, capacity) {
			for _, o := range ops {
				t.Logf("  [%d,%d] %s", o.start, o.end, o)
			}
			t.Fatalf("iter %d: history not linearizable (capacity %d)", iter, capacity)
		}
	}
}

func TestLinearizableCap0(t *testing.T) { testLinearizableWorkload(t, 0) }
func TestLinearizableCap2(t *testing.T) { testLinearizableWorkload(t, 2) }
