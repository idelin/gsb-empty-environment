package reassemble

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func testConfig() Config {
	return Config{
		MaxMessageSize:   1024,
		MaxBufferedBytes: 4096,
		TTL:              time.Minute,
		CompletionTTL:    time.Minute,
	}
}

type frag struct {
	off  int
	data []byte
}

// feed adds fragments and returns the completed data (must complete exactly
// once, on the last fragment that finishes coverage).
func feed(t *testing.T, r *Reassembler, id uint64, total int, frags []frag) []byte {
	t.Helper()
	var got []byte
	done := 0
	for _, f := range frags {
		data, complete, err := r.Add(id, total, f.off, f.data)
		if err != nil {
			t.Fatalf("Add(off=%d): %v", f.off, err)
		}
		if complete && data != nil {
			done++
			got = data
		}
	}
	if done != 1 {
		t.Fatalf("completed %d times, want 1", done)
	}
	return got
}

func TestOutOfOrderReassembly(t *testing.T) {
	msg := []byte("the quick brown fox jumps over the lazy dog")
	frags := []frag{
		{0, msg[0:10]},
		{10, msg[10:25]},
		{25, msg[25:43]},
	}
	// Every permutation of the same fragments must yield the same result.
	perm := []int{0, 1, 2}
	for {
		c := &clock{t: time.Now()}
		r := New(testConfig(), c.now)
		shuffled := make([]frag, 3)
		for i, p := range perm {
			shuffled[i] = frags[p]
		}
		got := feed(t, r, 7, len(msg), shuffled)
		if !bytes.Equal(got, msg) {
			t.Fatalf("perm %v: got %q want %q", perm, got, msg)
		}
		if r.Buffered() != 0 || r.InFlight() != 0 {
			t.Fatalf("state not released after completion")
		}
		if !permNext(perm) {
			break
		}
	}
}

func permNext(p []int) bool {
	for i := len(p) - 2; i >= 0; i-- {
		if p[i] < p[i+1] {
			for j := len(p) - 1; ; j-- {
				if p[j] > p[i] {
					p[i], p[j] = p[j], p[i]
					for l, r := i+1, len(p)-1; l < r; l, r = l+1, r-1 {
						p[l], p[r] = p[r], p[l]
					}
					return true
				}
			}
		}
	}
	return false
}

func TestRandomFragmentationSameResult(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	msg := make([]byte, 500)
	rng.Read(msg)
	var reference []byte
	for trial := 0; trial < 50; trial++ {
		// Random overlapping fragments, random order.
		var frags []frag
		for covered := 0; covered < len(msg); {
			n := 1 + rng.Intn(60)
			if covered+n > len(msg) {
				n = len(msg) - covered
			}
			frags = append(frags, frag{covered, msg[covered : covered+n]})
			covered += n
		}
		// Add redundant overlapping copies.
		for i := 0; i < 10; i++ {
			off := rng.Intn(len(msg) - 1)
			n := 1 + rng.Intn(len(msg)-off)
			frags = append(frags, frag{off, msg[off : off+n]})
		}
		rng.Shuffle(len(frags), func(i, j int) { frags[i], frags[j] = frags[j], frags[i] })

		c := &clock{t: time.Now()}
		r := New(testConfig(), c.now)
		got := feed(t, r, 99, len(msg), frags)
		if reference == nil {
			reference = got
		} else if !bytes.Equal(got, reference) {
			t.Fatalf("trial %d: different order gave different result", trial)
		}
		if !bytes.Equal(got, msg) {
			t.Fatalf("trial %d: wrong content", trial)
		}
	}
}

func TestConsistentOverlapAndDuplicates(t *testing.T) {
	c := &clock{t: time.Now()}
	r := New(testConfig(), c.now)
	msg := []byte("aaaaaaaaaabbbbbbbbbb")
	got := feed(t, r, 1, len(msg), []frag{
		{0, msg[0:12]},
		{8, msg[8:20]}, // 4-byte consistent overlap
		{8, msg[8:20]}, // exact duplicate
		{0, msg[0:12]}, // another duplicate
	})
	if !bytes.Equal(got, msg) {
		t.Fatalf("got %q", got)
	}
}

func TestConflictingOverlapRejected(t *testing.T) {
	c := &clock{t: time.Now()}
	r := New(testConfig(), c.now)
	if _, _, err := r.Add(1, 10, 0, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	// Overlaps fully, but disagrees by one byte at offset 4: 'o' vs 'O'.
	// The byte must not be silently chosen from either fragment.
	_, _, err := r.Add(1, 10, 0, []byte("hellO"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v, want ErrConflict", err)
	}
	// The original message state must be untouched by the conflict.
	data, complete, err := r.Add(1, 10, 5, []byte("world"))
	if err != nil || !complete {
		t.Fatalf("after conflict: complete=%v err=%v", complete, err)
	}
	if string(data) != "helloworld" {
		t.Fatalf("got %q", data)
	}
}

func TestTotalLenMismatch(t *testing.T) {
	c := &clock{t: time.Now()}
	r := New(testConfig(), c.now)
	if _, _, err := r.Add(1, 10, 0, []byte("ab")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Add(1, 11, 2, []byte("c")); !errors.Is(err, ErrTotalLenMismatch) {
		t.Fatalf("got %v, want ErrTotalLenMismatch", err)
	}
}

func TestInvalidFragment(t *testing.T) {
	c := &clock{t: time.Now()}
	r := New(testConfig(), c.now)
	if _, _, err := r.Add(1, 5, 3, []byte("abc")); !errors.Is(err, ErrInvalidFragment) {
		t.Fatalf("past-end: got %v", err)
	}
	if _, _, err := r.Add(1, 5, -1, []byte("a")); !errors.Is(err, ErrInvalidFragment) {
		t.Fatalf("negative: got %v", err)
	}
}

func TestPerMessageQuotaCheckedBeforeAllocation(t *testing.T) {
	c := &clock{t: time.Now()}
	cfg := testConfig()
	cfg.MaxMessageSize = 8
	r := New(cfg, c.now)
	if _, _, err := r.Add(1, 9, 0, []byte("x")); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("got %v, want ErrMessageTooLarge", err)
	}
	if r.Buffered() != 0 || r.InFlight() != 0 {
		t.Fatalf("quota rejection must not allocate or retain state")
	}
	// Same ID at a legal size still works.
	if _, _, err := r.Add(1, 8, 0, []byte("x")); err != nil {
		t.Fatal(err)
	}
}

func TestGlobalQuotaCheckedBeforeAllocation(t *testing.T) {
	c := &clock{t: time.Now()}
	cfg := testConfig()
	cfg.MaxBufferedBytes = 20
	r := New(cfg, c.now)
	if _, _, err := r.Add(1, 12, 0, []byte("x")); err != nil {
		t.Fatal(err)
	}
	// 12 + 12 > 20: rejected before allocation.
	if _, _, err := r.Add(2, 12, 0, []byte("x")); !errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("got %v, want ErrGlobalQuota", err)
	}
	if r.Buffered() != 12 || r.InFlight() != 1 {
		t.Fatalf("rejected message leaked quota: buffered=%d inflight=%d", r.Buffered(), r.InFlight())
	}
	// 12 + 8 == 20 fits exactly.
	if _, _, err := r.Add(2, 8, 0, []byte("x")); err != nil {
		t.Fatal(err)
	}
	// Completing message 1 frees its quota for message 3.
	if _, complete, err := r.Add(1, 12, 1, make([]byte, 11)); err != nil || !complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
	if _, _, err := r.Add(3, 12, 0, []byte("x")); err != nil {
		t.Fatalf("quota was not freed after completion: %v", err)
	}
}

func TestExpiryAndLateFragments(t *testing.T) {
	c := &clock{t: time.Now()}
	cfg := testConfig()
	cfg.TTL = 10 * time.Second
	r := New(cfg, c.now)
	if _, _, err := r.Add(1, 10, 0, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	c.advance(11 * time.Second) // message 1 expires
	// A late fragment must not borrow the stale buffer: it starts a new
	// message, so the old "hello" bytes are gone and nothing completes.
	data, complete, err := r.Add(1, 10, 5, []byte("world"))
	if err != nil {
		t.Fatal(err)
	}
	if complete {
		t.Fatalf("late fragment completed from stale buffer: %q", data)
	}
	if r.Buffered() != 10 || r.InFlight() != 1 {
		t.Fatalf("expired buffer not replaced: buffered=%d inflight=%d", r.Buffered(), r.InFlight())
	}
	// The new message completes only from post-expiry fragments.
	data, complete, err = r.Add(1, 10, 0, []byte("HELLO"))
	if err != nil || !complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
	if string(data) != "HELLOworld" {
		t.Fatalf("stale bytes leaked into result: %q", data)
	}
}

func TestExpireSweep(t *testing.T) {
	c := &clock{t: time.Now()}
	cfg := testConfig()
	cfg.TTL = 10 * time.Second
	r := New(cfg, c.now)
	for id := uint64(1); id <= 3; id++ {
		if _, _, err := r.Add(id, 100, 0, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	c.advance(11 * time.Second)
	if dropped := r.Expire(); dropped != 3 {
		t.Fatalf("dropped %d, want 3", dropped)
	}
	if r.Buffered() != 0 || r.InFlight() != 0 {
		t.Fatalf("quota not freed by Expire")
	}
}

func TestCompletionDedup(t *testing.T) {
	c := &clock{t: time.Now()}
	cfg := testConfig()
	cfg.CompletionTTL = time.Minute
	r := New(cfg, c.now)
	got := feed(t, r, 1, 4, []frag{{0, []byte("ab")}, {2, []byte("cd")}})
	if string(got) != "abcd" {
		t.Fatalf("got %q", got)
	}
	// Late duplicate fragments are deduplicated, not reassembled again.
	data, complete, err := r.Add(1, 4, 0, []byte("ab"))
	if err != nil || !complete || data != nil {
		t.Fatalf("dedup: data=%q complete=%v err=%v", data, complete, err)
	}
	if r.InFlight() != 0 {
		t.Fatalf("dedup fragment must not allocate a new message")
	}
	// After the dedup window expires, the ID can be reused.
	c.advance(2 * time.Minute)
	r.Expire()
	got = feed(t, r, 1, 4, []frag{{0, []byte("WXYZ")}})
	if string(got) != "WXYZ" {
		t.Fatalf("ID reuse after dedup expiry: got %q", got)
	}
}

func TestZeroLengthMessage(t *testing.T) {
	c := &clock{t: time.Now()}
	r := New(testConfig(), c.now)
	data, complete, err := r.Add(1, 0, 0, nil)
	if err != nil || !complete || len(data) != 0 {
		t.Fatalf("data=%q complete=%v err=%v", data, complete, err)
	}
}
