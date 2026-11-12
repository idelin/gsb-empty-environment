package netsec

import (
	"errors"
	"net/http"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// Frame is one observed delivery to a receiver: either a 103 hint or the
// terminal response. Frames retain append order so tests can assert the exact
// header sequence each receiver gets.
type Frame struct {
	Kind   string // "hint" or "final"
	Status int
	Header http.Header
	Body   string
}

// recordingSink is a Sink that records every frame without touching the wire.
type recordingSink struct {
	mu     sync.Mutex
	frames []Frame
	err    error
}

func (r *recordingSink) Hint(h http.Header) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.frames = append(r.frames, Frame{Kind: "hint", Status: 103, Header: h.Clone()})
	return nil
}

func (r *recordingSink) Final(status int, h http.Header, body []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.frames = append(r.frames, Frame{Kind: "final", Status: status, Header: h.Clone(), Body: string(body)})
	return nil
}

func (r *recordingSink) Frames() []Frame {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Frame, len(r.frames))
	copy(out, r.frames)
	return out
}

func newRequest() *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "http://tenant.example/", nil)
	return r
}

func headerKeys(h http.Header) []string {
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func linkHeader(v string) http.Header {
	h := http.Header{}
	h.Set("Link", v)
	return h
}

func bindPair(t *testing.T, p *Pipeline) (*http.Request, *recordingSink, *Stream) {
	t.Helper()
	req := newRequest()
	sink := &recordingSink{}
	stream, err := p.Bind(req, sink)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	return req, sink, stream
}

func TestHintWhitelistAndAuthBlock(t *testing.T) {
	p := NewPipeline()
	req, sink, stream := bindPair(t, p)

	in := http.Header{}
	in.Add("Link", "</style.css>; rel=preload")
	in.Add("X-Custom-Tenant", "tenant-a-secret")
	in.Add("Authorization", "Bearer tenant-a-token")
	in.Add("Proxy-Authorization", "Basic abc")
	in.Add("Cookie", "sid=tenant-a")
	in.Add("Set-Cookie", "sid=tenant-a")

	if err := stream.Hint(in); err != nil {
		t.Fatalf("Hint: %v", err)
	}

	frames := sink.Frames()
	if len(frames) != 1 || frames[0].Kind != "hint" || frames[0].Status != 103 {
		t.Fatalf("want one 103 hint frame, got %+v", frames)
	}
	if got, want := headerKeys(frames[0].Header), []string{"Link"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hint headers = %v, want only %v", got, want)
	}
	if got := frames[0].Header.Get("Link"); got != "</style.css>; rel=preload" {
		t.Fatalf("Link = %q", got)
	}

	// The input map must not be mutated by sanitization.
	if got := in.Get("Authorization"); got != "Bearer tenant-a-token" {
		t.Fatalf("input header mutated, Authorization = %q", got)
	}
	if got := len(in); got != 6 {
		t.Fatalf("input header mutated, key count = %d want 6", got)
	}

	// Pipeline-level routing uses request identity too.
	in2 := http.Header{}
	in2.Set("link", "</x.js>; rel=preload") // lowercase key still canonicalized
	if err := p.Hint(req, in2); err != nil {
		t.Fatalf("pipeline Hint: %v", err)
	}
	frames = sink.Frames()
	if len(frames) != 2 || frames[1].Header.Get("Link") != "</x.js>; rel=preload" {
		t.Fatalf("second hint missing or wrong: %+v", frames)
	}
}

func TestCustomWhitelistStillBlocksAuth(t *testing.T) {
	p := NewPipeline(WithHintWhitelist("X-Trace-Id", "Authorization"))
	_, sink, stream := bindPair(t, p)

	in := http.Header{}
	in.Set("X-Trace-Id", "trace-1")
	in.Set("Authorization", "Bearer leaked")
	in.Set("X-Off-List", "drop-me")

	if err := stream.Hint(in); err != nil {
		t.Fatalf("Hint: %v", err)
	}
	frames := sink.Frames()
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	want := http.Header{}
	want.Set("X-Trace-Id", "trace-1")
	if !reflect.DeepEqual(frames[0].Header, want) {
		t.Fatalf("hint = %v, want %v (Authorization blocked even when whitelisted)", frames[0].Header, want)
	}
}

func TestLateHintRejectedAfterFinal(t *testing.T) {
	p := NewPipeline()
	req, sink, stream := bindPair(t, p)

	if err := stream.Hint(linkHeader("</a.css>; rel=preload")); err != nil {
		t.Fatalf("Hint: %v", err)
	}

	finalHeader := http.Header{}
	finalHeader.Set("Content-Type", "text/html")
	if err := stream.Final(http.StatusOK, finalHeader, []byte("body")); err != nil {
		t.Fatalf("Final: %v", err)
	}

	lateHint := linkHeader("</late.css>; rel=preload")
	if err := stream.Hint(lateHint); !errors.Is(err, ErrLateHint) {
		t.Fatalf("late hint err = %v, want ErrLateHint", err)
	}
	if err := p.Hint(req, lateHint); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("pipeline-level late hint err = %v, want ErrUnknownRequest (stream unbound)", err)
	}
	if err := stream.Final(http.StatusOK, finalHeader, nil); !errors.Is(err, ErrFinalized) {
		t.Fatalf("second Final err = %v, want ErrFinalized", err)
	}

	frames := sink.Frames()
	if len(frames) != 2 {
		t.Fatalf("want hint+final only, got %d frames: %+v", len(frames), frames)
	}
	if frames[0].Kind != "hint" || frames[1].Kind != "final" || frames[1].Status != 200 || frames[1].Body != "body" {
		t.Fatalf("frame sequence wrong: %+v", frames)
	}
	if p.Len() != 0 {
		t.Fatalf("pipeline still holds %d streams after final", p.Len())
	}
}

func TestFinalHeaderNeverMergesHintFields(t *testing.T) {
	p := NewPipeline()
	_, sink, stream := bindPair(t, p)

	hintHeader := http.Header{}
	hintHeader.Set("Link", "</preload.js>; rel=preload")
	hintHeader.Set("Authorization", "Bearer hint-token")
	if err := stream.Hint(hintHeader); err != nil {
		t.Fatalf("Hint: %v", err)
	}

	finalHeader := http.Header{}
	finalHeader.Set("Content-Type", "application/json")
	finalHeader.Set("X-Tenant", "tenant-a")
	if err := stream.Final(http.StatusCreated, finalHeader, nil); err != nil {
		t.Fatalf("Final: %v", err)
	}

	frames := sink.Frames()
	finalFrame := frames[len(frames)-1]
	if got := headerKeys(finalFrame.Header); !reflect.DeepEqual(got, []string{"Content-Type", "X-Tenant"}) {
		t.Fatalf("final header = %v, must not contain Link or auth fields from hint", got)
	}
	if finalFrame.Header.Get("Authorization") != "" || finalFrame.Header.Get("Link") != "" {
		t.Fatalf("hint field leaked into final header: %v", finalFrame.Header)
	}

	// Final header map is a copy: later caller mutation must not reach sink.
	finalHeader.Set("X-Tenant", "mutated")
	if got := sink.Frames()[1].Header.Get("X-Tenant"); got != "tenant-a" {
		t.Fatalf("sink final header aliases caller map: %q", got)
	}
}

func TestInterleavedRequestsStayIsolated(t *testing.T) {
	p := NewPipeline()
	reqB, sinkA, streamA := bindPair(t, p)
	_, sinkB, streamB := bindPair(t, p)
	_, sinkC, streamC := bindPair(t, p)
	if p.Len() != 3 {
		t.Fatalf("Len = %d, want 3", p.Len())
	}

	// Interleave frames from three tenants. Each receiver must only see its
	// own frames and in the exact order they were emitted.
	mustHint := func(s *Stream, v string) {
		t.Helper()
		if err := s.Hint(linkHeader(v)); err != nil {
			t.Fatalf("Hint: %v", err)
		}
	}
	mustFinal := func(s *Stream, tenant string, body ...byte) {
		t.Helper()
		h := http.Header{}
		h.Set("X-Tenant", tenant)
		if err := s.Final(http.StatusOK, h, body); err != nil {
			t.Fatalf("Final: %v", err)
		}
	}

	mustHint(streamA, "</a1>")
	mustHint(streamB, "</b1>")
	mustHint(streamA, "</a2>")
	mustHint(streamC, "</c1>")
	mustFinal(streamB, "b")
	mustFinal(streamA, "a", 'A')
	mustFinal(streamC, "c")

	assertSequence := func(name string, sink *recordingSink, wantLinks []string, wantTenant string) {
		t.Helper()
		frames := sink.Frames()
		if len(frames) != len(wantLinks)+1 {
			t.Fatalf("%s: frame count = %d, want %d", name, len(frames), len(wantLinks)+1)
		}
		for i, want := range wantLinks {
			if frames[i].Kind != "hint" || frames[i].Header.Get("Link") != want {
				t.Fatalf("%s: hint %d = %+v, want Link %q", name, i, frames[i], want)
			}
			if len(frames[i].Header) != 1 {
				t.Fatalf("%s: hint %d carries extra fields: %v", name, i, frames[i].Header)
			}
		}
		last := frames[len(frames)-1]
		if last.Kind != "final" || last.Header.Get("X-Tenant") != wantTenant {
			t.Fatalf("%s: final frame wrong: %+v", name, last)
		}
	}
	assertSequence("A", sinkA, []string{"</a1>", "</a2>"}, "a")
	assertSequence("B", sinkB, []string{"</b1>"}, "b")
	assertSequence("C", sinkC, []string{"</c1>"}, "c")

	// A request instance never bound (and every stream is now unbound) must
	// not be deliverable through the pipeline router.
	stranger := newRequest()
	if err := p.Hint(stranger, linkHeader("</x>")); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("stranger Hint err = %v, want ErrUnknownRequest", err)
	}
	if err := p.Final(reqB, 200, nil, nil); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("final after unbind err = %v, want ErrUnknownRequest", err)
	}
	if p.Len() != 0 {
		t.Fatalf("Len = %d, want 0", p.Len())
	}
}

func TestCancelRejectsFurtherFrames(t *testing.T) {
	p := NewPipeline()
	reqA, sinkA, streamA := bindPair(t, p)
	_, sinkB, streamB := bindPair(t, p)

	if err := streamA.Hint(linkHeader("</a1>")); err != nil {
		t.Fatal(err)
	}
	streamA.Cancel()

	if err := streamA.Hint(linkHeader("</late>")); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("hint after cancel err = %v, want ErrStreamClosed", err)
	}
	if err := streamA.Final(200, nil, nil); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("final after cancel err = %v, want ErrStreamClosed", err)
	}
	if err := p.Hint(reqA, linkHeader("</x>")); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("pipeline hint after cancel err = %v, want ErrUnknownRequest", err)
	}

	// Cancel is idempotent and must not touch a different tenant's stream.
	streamA.Cancel()
	if err := streamB.Hint(linkHeader("</b1>")); err != nil {
		t.Fatalf("tenant B hint after A cancel: %v", err)
	}
	h := http.Header{}
	h.Set("X-Tenant", "b")
	if err := streamB.Final(http.StatusOK, h, nil); err != nil {
		t.Fatalf("tenant B final: %v", err)
	}
	p.Cancel(reqA) // no-op on an already canceled stream

	if frames := sinkA.Frames(); len(frames) != 1 || frames[0].Header.Get("Link") != "</a1>" {
		t.Fatalf("canceled receiver must keep exactly pre-cancel frames, got %+v", frames)
	}
	if frames := sinkB.Frames(); len(frames) != 2 {
		t.Fatalf("tenant B frames = %d, want 2", len(frames))
	}
}

func TestBindAndStatusValidation(t *testing.T) {
	p := NewPipeline()
	req, sink, stream := bindPair(t, p)

	if _, err := p.Bind(req, sink); !errors.Is(err, ErrAlreadyBound) {
		t.Fatalf("rebind err = %v, want ErrAlreadyBound", err)
	}
	if _, err := p.Bind(nil, sink); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("nil request bind err = %v, want ErrUnknownRequest", err)
	}
	if _, err := p.Bind(newRequest(), nil); err == nil {
		t.Fatal("nil sink bind must fail")
	}

	for _, bad := range []int{0, 100, 103, 199, 600, -1} {
		if err := stream.Final(bad, nil, nil); !errors.Is(err, ErrInvalidStatus) {
			t.Fatalf("Final(%d) err = %v, want ErrInvalidStatus", bad, err)
		}
	}
	if frames := sink.Frames(); len(frames) != 0 {
		t.Fatalf("invalid status must not deliver frames, got %+v", frames)
	}
}

func TestEmptyHintDeliversNothing(t *testing.T) {
	p := NewPipeline()
	_, sink, stream := bindPair(t, p)

	noisy := http.Header{}
	noisy.Set("Authorization", "Bearer x")
	noisy.Set("X-Off-List", "y")
	if err := stream.Hint(noisy); err != nil {
		t.Fatalf("Hint: %v", err)
	}
	if err := stream.Hint(nil); err != nil {
		t.Fatalf("nil Hint: %v", err)
	}
	if got := stream.HintsSent(); got != 0 {
		t.Fatalf("HintsSent = %d, want 0 for fully filtered hints", got)
	}

	h := http.Header{}
	h.Set("X-Tenant", "a")
	if err := stream.Final(http.StatusOK, h, nil); err != nil {
		t.Fatalf("Final: %v", err)
	}
	frames := sink.Frames()
	if len(frames) != 1 || frames[0].Kind != "final" {
		t.Fatalf("want only final frame, got %+v", frames)
	}
}

func TestConcurrentInterleavingRace(t *testing.T) {
	p := NewPipeline()
	const n = 24
	type tenant struct {
		req    *http.Request
		sink   *recordingSink
		stream *Stream
		hints  int // decided up-front, read after wg.Wait (no concurrent access)
	}
	tenants := make([]tenant, n)
	for i := range tenants {
		req, sink, stream := bindPair(t, p)
		tenants[i] = tenant{req: req, sink: sink, stream: stream, hints: 1 + (i % 4)}
	}
	if p.Len() != n {
		t.Fatalf("Len = %d, want %d", p.Len(), n)
	}

	var wg sync.WaitGroup
	for i := range tenants {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			tn := tenants[i]
			for j := 0; j < tn.hints; j++ {
				v := "</t" + itoa(i) + "-h" + itoa(j) + ">"
				_ = tn.stream.Hint(linkHeader(v))
			}
			if i%5 == 0 {
				tn.stream.Cancel()
				if err := tn.stream.Hint(linkHeader("</late>")); !errors.Is(err, ErrStreamClosed) {
					t.Errorf("tenant %d post-cancel hint err = %v", i, err)
				}
				return
			}
			h := http.Header{}
			h.Set("X-Tenant", itoa(i))
			_ = tn.stream.Final(http.StatusOK, h, []byte(itoa(i)))
			if err := tn.stream.Hint(linkHeader("</late>")); !errors.Is(err, ErrLateHint) {
				t.Errorf("tenant %d late hint err = %v, want ErrLateHint", i, err)
			}
		}()
	}
	wg.Wait()

	if p.Len() != 0 {
		t.Fatalf("all streams must be unbound, Len = %d", p.Len())
	}
	for i, tn := range tenants {
		frames := tn.sink.Frames()
		delivered := tn.hints
		if i%5 == 0 {
			// Canceled tenant: only frames emitted before Cancel, no final.
			for _, f := range frames {
				if f.Kind == "final" {
					t.Fatalf("canceled tenant %d received a final frame", i)
				}
			}
			if len(frames) != delivered {
				t.Fatalf("canceled tenant %d frames = %d, want pre-cancel hints %d", i, len(frames), delivered)
			}
			continue
		}
		if len(frames) != delivered+1 {
			t.Fatalf("tenant %d frames = %d, want %d", i, len(frames), delivered+1)
		}
		for j := 0; j < delivered; j++ {
			want := "</t" + itoa(i) + "-h" + itoa(j) + ">"
			if frames[j].Kind != "hint" || frames[j].Header.Get("Link") != want {
				t.Fatalf("tenant %d hint %d = %+v, want %q", i, j, frames[j], want)
			}
			// No other tenant's Link value may appear in this receiver.
			for k := range tenants {
				if k == i {
					continue
				}
				otherMax := 1 + 3 // hints range 1..4
				for m := 0; m < otherMax; m++ {
					foreign := "</t" + itoa(k) + "-h" + itoa(m) + ">"
					if frames[j].Header.Get("Link") == foreign {
						t.Fatalf("tenant %d received foreign hint %q", i, foreign)
					}
				}
			}
		}
		last := frames[len(frames)-1]
		if last.Kind != "final" || last.Header.Get("X-Tenant") != itoa(i) || last.Body != itoa(i) {
			t.Fatalf("tenant %d final frame wrong: %+v", i, last)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
