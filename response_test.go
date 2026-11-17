package netsec

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"
)

type stageRecorder struct {
	mu   sync.Mutex
	byID map[RequestID][]Stage
}

func newStageRecorder() *stageRecorder {
	return &stageRecorder{byID: make(map[RequestID][]Stage)}
}

func (r *stageRecorder) Emit(stage Stage) error {
	stage.Header = stage.Header.Clone()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[stage.RequestID] = append(r.byID[stage.RequestID], stage)
	return nil
}

func (r *stageRecorder) stages(id RequestID) []Stage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Stage, len(r.byID[id]))
	copy(out, r.byID[id])
	return out
}

func linkHeader(value string) http.Header {
	return http.Header{"Link": {value}}
}

func TestInterleavedRequestsKeepHeaderSequences(t *testing.T) {
	rec := newStageRecorder()
	a, err := New(context.Background(), "tenant-a", rec)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(context.Background(), "tenant-b", rec)
	if err != nil {
		t.Fatal(err)
	}

	aHint1 := http.Header{
		"Link":          {"</a1.css>; rel=preload"},
		"Authorization": {"Bearer tenant-a-secret"},
		"Set-Cookie":    {"tenant-a-session=secret"},
		"X-Tenant":      {"tenant-a"},
	}
	if err := a.Hint(aHint1); err != nil {
		t.Fatalf("a hint 1: %v", err)
	}
	// The response must own its snapshot. Reusing the caller's map must not
	// rewrite the stage already delivered to tenant A.
	aHint1.Set("Link", "</stale-request.css>; rel=preload")

	bHint1 := http.Header{
		"link":       {"</b1.css>; rel=preload"},
		"Cookie":     {"tenant-b-session=secret"},
		"X-Tenant":   {"tenant-b"},
		"User-Agent": {"tenant-b-agent"},
	}
	if err := b.Hint(bHint1); err != nil {
		t.Fatalf("b hint 1: %v", err)
	}

	if err := a.Hint(linkHeader("</a2.js>; rel=preload")); err != nil {
		t.Fatalf("a hint 2: %v", err)
	}
	if err := b.Final(http.StatusOK, http.Header{
		"Content-Type": {"text/html"},
		"X-Tenant":     {"tenant-b"},
	}); err != nil {
		t.Fatalf("b final: %v", err)
	}
	if err := a.Final(http.StatusCreated, http.Header{
		"Content-Type": {"application/json"},
		"X-Tenant":     {"tenant-a"},
	}); err != nil {
		t.Fatalf("a final: %v", err)
	}

	if err := a.Hint(linkHeader("</a3-late.css>; rel=preload")); !errors.Is(err, ErrLateHint) {
		t.Fatalf("late a hint error = %v, want ErrLateHint", err)
	}
	if err := b.Hint(linkHeader("</b2-late.css>; rel=preload")); !errors.Is(err, ErrLateHint) {
		t.Fatalf("late b hint error = %v, want ErrLateHint", err)
	}
	if !errors.Is(ErrLateHint, ErrFinalized) {
		t.Fatal("ErrLateHint must also match ErrFinalized")
	}
	if err := a.Final(http.StatusOK, nil); !errors.Is(err, ErrFinalized) {
		t.Fatalf("second a final error = %v, want ErrFinalized", err)
	}
	a.Cancel()
	if got := a.State(); got != StateFinalized {
		t.Fatalf("cancel after final changed state to %v", got)
	}

	stagesA := rec.stages("tenant-a")
	stagesB := rec.stages("tenant-b")
	if len(stagesA) != 3 {
		t.Fatalf("tenant-a stages = %d, want 3", len(stagesA))
	}
	if len(stagesB) != 2 {
		t.Fatalf("tenant-b stages = %d, want 2", len(stagesB))
	}

	assertStage(t, stagesA[0], "tenant-a", StageHint, StatusEarlyHints, 1)
	assertStage(t, stagesA[1], "tenant-a", StageHint, StatusEarlyHints, 2)
	assertStage(t, stagesA[2], "tenant-a", StageFinal, http.StatusCreated, 3)
	assertStage(t, stagesB[0], "tenant-b", StageHint, StatusEarlyHints, 1)
	assertStage(t, stagesB[1], "tenant-b", StageFinal, http.StatusOK, 2)

	if got, want := stagesA[0].Header.Values("Link"), []string{"</a1.css>; rel=preload"}; !equalStrings(got, want) {
		t.Fatalf("tenant-a hint 1 Link = %v, want %v", got, want)
	}
	if got, want := stagesA[1].Header.Values("Link"), []string{"</a2.js>; rel=preload"}; !equalStrings(got, want) {
		t.Fatalf("tenant-a hint 2 Link = %v, want %v", got, want)
	}
	if got, want := stagesB[0].Header.Values("Link"), []string{"</b1.css>; rel=preload"}; !equalStrings(got, want) {
		t.Fatalf("tenant-b hint 1 Link = %v, want %v", got, want)
	}

	for _, stage := range []Stage{stagesA[0], stagesA[1], stagesB[0]} {
		assertOnlyFields(t, stage.Header, "Link")
		for _, forbidden := range []string{"Authorization", "Set-Cookie", "Cookie", "X-Tenant", "User-Agent"} {
			if got := stage.Header.Get(forbidden); got != "" {
				t.Fatalf("%s hint leaked %s = %q", stage.RequestID, forbidden, got)
			}
		}
	}

	assertFinalHeader(t, stagesA[2], "tenant-a")
	assertFinalHeader(t, stagesB[1], "tenant-b")
}

func TestWhitelistRejectsSensitiveAndUnknownFields(t *testing.T) {
	rec := newStageRecorder()
	resp, err := New(context.Background(), "tenant-a", rec)
	if err != nil {
		t.Fatal(err)
	}

	h := http.Header{
		"link":             {"</mixed.css>; rel=preload"},
		"LINK":             {"</upper.css>; rel=preload"},
		"Alt-Svc":          {`h3=":443"; ma=3600`},
		"authorization":    {"Bearer secret"},
		"AUTHORIZATION":    {"Bearer another-secret"},
		"Set-Cookie":       {"sid=secret"},
		"cookie":           {"sid=secret"},
		"WWW-Authenticate": {`Basic realm="private"`},
		"X-Api-Key":        {"secret"},
		"X-Tenant":         {"tenant-b"},
	}
	if err := resp.Hint(h); err != nil {
		t.Fatalf("hint: %v", err)
	}

	stages := rec.stages("tenant-a")
	if len(stages) != 1 {
		t.Fatalf("stages = %d, want 1", len(stages))
	}
	assertOnlyFields(t, stages[0].Header, "Alt-Svc", "Link")
	gotLinks := append([]string(nil), stages[0].Header.Values("Link")...)
	sort.Strings(gotLinks)
	if want := []string{"</mixed.css>; rel=preload", "</upper.css>; rel=preload"}; !equalStrings(gotLinks, want) {
		t.Fatalf("Link = %v, want %v", gotLinks, want)
	}
	_, dropped := DefaultHintPolicy().Filter(h)
	if want := []string{
		"Authorization", "Cookie", "Set-Cookie", "Www-Authenticate", "X-Api-Key", "X-Tenant",
	}; !equalStrings(dropped, want) {
		t.Fatalf("dropped fields = %v, want %v", dropped, want)
	}
	if !IsSensitiveHintField("www-authenticate") || IsSensitiveHintField("Link") {
		t.Fatal("sensitive-field classification is wrong")
	}

	if _, err := NewHintPolicy("Authorization"); !errors.Is(err, ErrSensitiveHintField) {
		t.Fatalf("sensitive policy error = %v, want ErrSensitiveHintField", err)
	}
	if _, err := NewHintPolicy("X-Auth-Resource"); !errors.Is(err, ErrSensitiveHintField) {
		t.Fatalf("marked sensitive policy error = %v, want ErrSensitiveHintField", err)
	}
	if _, err := NewHintPolicy("Content-Security-Policy"); err != nil {
		t.Fatalf("non-sensitive custom policy: %v", err)
	}

	custom, err := NewHintPolicy("X-Resource")
	if err != nil {
		t.Fatal(err)
	}
	customResp, err := New(context.Background(), "tenant-custom", rec, WithHintPolicy(custom))
	if err != nil {
		t.Fatal(err)
	}
	if err := customResp.Hint(http.Header{
		"X-Resource":    {"/custom.js"},
		"Link":          {"</not-allowed.css>"},
		"Authorization": {"Bearer secret"},
	}); err != nil {
		t.Fatalf("custom hint: %v", err)
	}
	customStages := rec.stages("tenant-custom")
	if len(customStages) != 1 {
		t.Fatalf("custom stages = %d, want 1", len(customStages))
	}
	assertOnlyFields(t, customStages[0].Header, "X-Resource")

	emptyResp, err := New(context.Background(), "tenant-empty", rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := emptyResp.Hint(http.Header{
		"Authorization": {"Bearer secret"},
		"Set-Cookie":    {"sid=secret"},
	}); !errors.Is(err, ErrNoHintFields) {
		t.Fatalf("auth-only hint error = %v, want ErrNoHintFields", err)
	}
	if got := len(rec.stages("tenant-empty")); got != 0 {
		t.Fatalf("auth-only hint emitted %d stages", got)
	}

	if err := emptyResp.Hint(http.Header{
		"Link": {"</ok.css>\r\nSet-Cookie: injected=1"},
	}); !errors.Is(err, ErrNoHintFields) {
		t.Fatalf("CRLF hint error = %v, want ErrNoHintFields", err)
	}
}

func TestCancellationIsPerRequest(t *testing.T) {
	rec := newStageRecorder()
	ctxA, cancelA := context.WithCancel(context.Background())
	a, err := New(ctxA, "tenant-a", rec)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(context.Background(), "tenant-b", rec)
	if err != nil {
		t.Fatal(err)
	}

	if err := a.Hint(linkHeader("</a1.css>")); err != nil {
		t.Fatalf("a hint 1: %v", err)
	}
	if err := b.Hint(linkHeader("</b1.css>")); err != nil {
		t.Fatalf("b hint 1: %v", err)
	}

	cancelA()
	select {
	case <-a.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("tenant-a handle did not observe cancellation")
	}
	if got := a.State(); got != StateCanceled {
		t.Fatalf("tenant-a state = %v, want canceled", got)
	}
	if err := a.Hint(linkHeader("</a2-late.css>")); !errors.Is(err, ErrCanceled) {
		t.Fatalf("a hint after cancel = %v, want ErrCanceled", err)
	}
	if err := a.Final(http.StatusOK, nil); !errors.Is(err, ErrCanceled) {
		t.Fatalf("a final after cancel = %v, want ErrCanceled", err)
	}

	if err := b.Hint(linkHeader("</b2.css>")); err != nil {
		t.Fatalf("b hint 2: %v", err)
	}
	if err := b.Final(http.StatusNoContent, http.Header{"X-Tenant": {"tenant-b"}}); err != nil {
		t.Fatalf("b final: %v", err)
	}

	stagesA := rec.stages("tenant-a")
	stagesB := rec.stages("tenant-b")
	if len(stagesA) != 1 {
		t.Fatalf("tenant-a stages = %d, want only the pre-cancel hint", len(stagesA))
	}
	if len(stagesB) != 3 {
		t.Fatalf("tenant-b stages = %d, want 3", len(stagesB))
	}
	if got, want := stagesB[1].Header.Values("Link"), []string{"</b2.css>"}; !equalStrings(got, want) {
		t.Fatalf("tenant-b hint 2 Link = %v, want %v", got, want)
	}
	if got := stagesB[2].Status; got != http.StatusNoContent {
		t.Fatalf("tenant-b final status = %d, want 204", got)
	}

	c, err := New(context.Background(), "tenant-c", rec)
	if err != nil {
		t.Fatal(err)
	}
	c.Cancel()
	c.Cancel()
	if got := c.State(); got != StateCanceled {
		t.Fatalf("explicitly canceled state = %v", got)
	}
	if err := c.Hint(linkHeader("</c.css>")); !errors.Is(err, ErrCanceled) {
		t.Fatalf("explicitly canceled hint = %v, want ErrCanceled", err)
	}
}

func TestConcurrentRequestIsolation(t *testing.T) {
	rec := newStageRecorder()
	a, err := New(context.Background(), "tenant-a", rec)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(context.Background(), "tenant-b", rec)
	if err != nil {
		t.Fatal(err)
	}

	const hintsPerRequest = 64
	var wg sync.WaitGroup
	run := func(resp *MultiStageResponse, tenant string) {
		defer wg.Done()
		for i := 0; i < hintsPerRequest; i++ {
			if err := resp.Hint(http.Header{
				"Link":          {fmt.Sprintf("</%s-%d.css>; rel=preload", tenant, i)},
				"Authorization": {"Bearer " + tenant + "-secret"},
			}); err != nil {
				t.Errorf("%s hint %d: %v", tenant, i, err)
				return
			}
		}
		if err := resp.Final(http.StatusOK, http.Header{"X-Tenant": {tenant}}); err != nil {
			t.Errorf("%s final: %v", tenant, err)
		}
	}
	wg.Add(2)
	go run(a, "tenant-a")
	go run(b, "tenant-b")
	wg.Wait()

	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		id := RequestID(tenant)
		stages := rec.stages(id)
		if len(stages) != hintsPerRequest+1 {
			t.Fatalf("%s stages = %d, want %d", tenant, len(stages), hintsPerRequest+1)
		}
		for i := 0; i < hintsPerRequest; i++ {
			stage := stages[i]
			if stage.Kind != StageHint || stage.Sequence != uint64(i+1) {
				t.Fatalf("%s stage %d = kind %v seq %d", tenant, i, stage.Kind, stage.Sequence)
			}
			want := fmt.Sprintf("</%s-%d.css>; rel=preload", tenant, i)
			if got := stage.Header.Get("Link"); got != want {
				t.Fatalf("%s stage %d Link = %q, want %q", tenant, i, got, want)
			}
			if got := stage.Header.Get("Authorization"); got != "" {
				t.Fatalf("%s stage %d leaked Authorization", tenant, i)
			}
		}
		final := stages[hintsPerRequest]
		if final.Kind != StageFinal || final.Sequence != hintsPerRequest+1 {
			t.Fatalf("%s final = kind %v seq %d", tenant, final.Kind, final.Sequence)
		}
		if got := final.Header.Get("X-Tenant"); got != tenant {
			t.Fatalf("%s final tenant = %q", tenant, got)
		}
		if got := final.Header.Get("Link"); got != "" {
			t.Fatalf("%s final retained hint Link = %q", tenant, got)
		}
	}
}

func TestFinalDoesNotMergeHintHeaders(t *testing.T) {
	rec := newStageRecorder()
	resp, err := New(context.Background(), "tenant-a", rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Hint(http.Header{
		"Link":          {"</early.css>; rel=preload"},
		"Alt-Svc":       {`h3=":443"`},
		"Authorization": {"Bearer secret"},
	}); err != nil {
		t.Fatal(err)
	}

	finalHeader := http.Header{
		"Content-Type": {"text/plain"},
		"X-Request":    {"tenant-a"},
	}
	if err := resp.Final(http.StatusOK, finalHeader); err != nil {
		t.Fatal(err)
	}
	finalHeader.Set("Content-Type", "mutated/after-emit")
	finalHeader.Set("Link", "</should-not-appear.css>")

	stages := rec.stages("tenant-a")
	if len(stages) != 2 {
		t.Fatalf("stages = %d, want 2", len(stages))
	}
	assertOnlyFields(t, stages[1].Header, "Content-Type", "X-Request")
	if got := stages[1].Header.Get("Content-Type"); got != "text/plain" {
		t.Fatalf("final Content-Type = %q, want snapshot", got)
	}
	for _, forbidden := range []string{"Link", "Alt-Svc", "Authorization"} {
		if got := stages[1].Header.Get(forbidden); got != "" {
			t.Fatalf("final leaked %s = %q", forbidden, got)
		}
	}
}

func TestNoHintCanBeEmittedAfterFinalUnderConcurrency(t *testing.T) {
	rec := newStageRecorder()
	resp, err := New(context.Background(), "tenant-a", rec)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < 5000; j++ {
				err := resp.Hint(linkHeader(fmt.Sprintf("</hint-%d-%d.css>", i, j)))
				if errors.Is(err, ErrLateHint) {
					return
				}
				if err != nil {
					t.Errorf("hint %d/%d: %v", i, j, err)
					return
				}
				runtime.Gosched()
			}
			t.Errorf("hint goroutine %d never observed the final response", i)
		}(i)
	}
	close(start)
	if err := resp.Final(http.StatusNoContent, nil); err != nil {
		t.Fatalf("final: %v", err)
	}
	wg.Wait()

	stages := rec.stages("tenant-a")
	if len(stages) == 0 {
		t.Fatal("no stages recorded")
	}
	if stages[len(stages)-1].Kind != StageFinal {
		t.Fatalf("last stage = %v, want final", stages[len(stages)-1].Kind)
	}
	for i, stage := range stages {
		if stage.Kind == StageFinal && i != len(stages)-1 {
			t.Fatalf("final stage at %d of %d", i, len(stages))
		}
	}
}

func TestInvalidConstructionAndStatus(t *testing.T) {
	if _, err := New(context.Background(), "", EmitterFunc(func(Stage) error { return nil })); !errors.Is(err, ErrInvalidRequestID) {
		t.Fatalf("empty id error = %v", err)
	}
	if _, err := New(context.Background(), "tenant-a", nil); !errors.Is(err, ErrNilEmitter) {
		t.Fatalf("nil emitter error = %v", err)
	}
	var typedNilEmitter *stageRecorder
	if _, err := New(context.Background(), "tenant-a", typedNilEmitter); !errors.Is(err, ErrNilEmitter) {
		t.Fatalf("typed nil emitter error = %v", err)
	}

	resp, err := New(context.Background(), "tenant-a", EmitterFunc(func(Stage) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Final(103, nil); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("1xx final error = %v, want ErrInvalidStatus", err)
	}
	if got := resp.State(); got != StateOpen {
		t.Fatalf("state after rejected final = %v, want open", got)
	}
	if err := resp.Final(http.StatusOK, http.Header{"Bad\nName": {"x"}}); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("bad final header error = %v, want ErrInvalidHeader", err)
	}
}

func assertStage(t *testing.T, stage Stage, id RequestID, kind StageKind, status int, sequence uint64) {
	t.Helper()
	if stage.RequestID != id {
		t.Fatalf("stage RequestID = %q, want %q", stage.RequestID, id)
	}
	if stage.Kind != kind {
		t.Fatalf("%s stage kind = %v, want %v", id, stage.Kind, kind)
	}
	if stage.Status != status {
		t.Fatalf("%s stage status = %d, want %d", id, stage.Status, status)
	}
	if stage.Sequence != sequence {
		t.Fatalf("%s stage sequence = %d, want %d", id, stage.Sequence, sequence)
	}
}

func assertOnlyFields(t *testing.T, header http.Header, fields ...string) {
	t.Helper()
	allowed := make(map[string]bool, len(fields))
	for _, field := range fields {
		allowed[http.CanonicalHeaderKey(field)] = true
	}
	for field := range header {
		if !allowed[http.CanonicalHeaderKey(field)] {
			t.Fatalf("unexpected header field %q in %v", field, header)
		}
	}
	if len(header) != len(fields) {
		t.Fatalf("header fields = %v, want %v", header, fields)
	}
}

func assertFinalHeader(t *testing.T, stage Stage, tenant string) {
	t.Helper()
	assertOnlyFields(t, stage.Header, "Content-Type", "X-Tenant")
	if got := stage.Header.Get("X-Tenant"); got != tenant {
		t.Fatalf("%s final X-Tenant = %q", tenant, got)
	}
	for _, forbidden := range []string{"Link", "Authorization", "Set-Cookie", "Cookie"} {
		if got := stage.Header.Get(forbidden); got != "" {
			t.Fatalf("%s final leaked hint field %s = %q", tenant, forbidden, got)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
