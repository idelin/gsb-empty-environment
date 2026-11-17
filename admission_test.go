package netsec

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustSource(t *testing.T, raw string) Source {
	t.Helper()
	source, err := ParseSource(raw)
	if err != nil {
		t.Fatalf("ParseSource(%q): %v", raw, err)
	}
	return source
}

func promise(parent, promised uint32, generation uint64, source Source) PromiseSpec {
	authority := source.Authority()
	return PromiseSpec{
		ParentStreamID:       parent,
		PromisedStreamID:     promised,
		ConnectionGeneration: generation,
		Headers: []HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: source.Scheme()},
			{Name: ":authority", Value: authority},
			{Name: ":path", Value: "/asset"},
		},
	}
}

func withPromisePath(spec PromiseSpec, path string) PromiseSpec {
	headers := make([]HeaderField, len(spec.Headers))
	copy(headers, spec.Headers)
	for i := range headers {
		if headers[i].Name == ":path" {
			headers[i].Value = path
		}
	}
	spec.Headers = headers
	return spec
}

func TestSameConnectionMultipleSourcesStayBoundToParent(t *testing.T) {
	const generation = 7
	sourceA := mustSource(t, "https://a.example")
	sourceB := mustSource(t, "https://b.example")

	type delivery struct {
		parent uint32
		push   uint32
		source string
	}
	var (
		mu        sync.Mutex
		delivered []delivery
		allocated int
	)
	admission := NewAdmission(generation,
		WithAllocator(func(_ context.Context, push *Push) error {
			mu.Lock()
			defer mu.Unlock()
			allocated++
			if got := push.State(); got != PushPending {
				t.Errorf("allocator saw push state %s, want pending", got)
			}
			return nil
		}),
		WithPushHandler(func(push *Push) {
			mu.Lock()
			defer mu.Unlock()
			delivered = append(delivered, delivery{
				parent: push.Parent().ID(),
				push:   push.StreamID(),
				source: push.Source().String(),
			})
		}),
	)

	parentA, err := admission.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{sourceA},
	})
	if err != nil {
		t.Fatal(err)
	}
	parentB, err := admission.RegisterParent(ParentSpec{
		StreamID:             3,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{sourceB},
	})
	if err != nil {
		t.Fatal(err)
	}

	// A promise on A's parent cannot borrow B's authentication context.
	if _, err := admission.AdmitPromise(promise(1, 2, generation, sourceB)); !errors.Is(err, ErrUnauthorizedSource) {
		t.Fatalf("cross-source promise error = %v, want ErrUnauthorizedSource", err)
	}
	if _, ok := admission.Push(2); ok {
		t.Fatal("unauthorized promise created a resource-bearing push")
	}
	mu.Lock()
	if allocated != 0 || len(delivered) != 0 {
		t.Fatalf("unauthorized promise reached host: allocated=%d delivered=%d", allocated, len(delivered))
	}
	mu.Unlock()

	// The rejected frame still consumed stream 2. Replaying it with a
	// different source must not turn it into an authorized push.
	if _, err := admission.AdmitPromise(promise(1, 2, generation, sourceA)); !errors.Is(err, ErrStreamIDReused) {
		t.Fatalf("replayed promise error = %v, want ErrStreamIDReused", err)
	}

	pushA, err := admission.AdmitPromise(promise(1, 4, generation, sourceA))
	if err != nil {
		t.Fatalf("A promise: %v", err)
	}
	pushB, err := admission.AdmitPromise(promise(3, 6, generation, sourceB))
	if err != nil {
		t.Fatalf("B promise: %v", err)
	}
	if pushA.Parent() != parentA || pushB.Parent() != parentB {
		t.Fatal("push was bound to the wrong parent")
	}
	if pushA.ConnectionGeneration() != generation || pushB.ConnectionGeneration() != generation {
		t.Fatal("push was bound to the wrong connection generation")
	}

	if _, err := admission.AdmitPromise(promise(3, 8, generation, sourceA)); !errors.Is(err, ErrUnauthorizedSource) {
		t.Fatalf("cross-source promise on B error = %v, want ErrUnauthorizedSource", err)
	}

	if !parentA.Cancel("client reset") {
		t.Fatal("CancelParent returned false")
	}
	if _, err := admission.AdmitPromise(promise(1, 10, generation, sourceA)); !errors.Is(err, ErrParentCanceled) {
		t.Fatalf("late promise error = %v, want ErrParentCanceled", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if allocated != 2 {
		t.Fatalf("allocator calls = %d, want 2", allocated)
	}
	if len(delivered) != 2 {
		t.Fatalf("delivered = %+v, want 2 authorized pushes", delivered)
	}
	if delivered[0] != (delivery{parent: 1, push: 4, source: "https://a.example"}) {
		t.Fatalf("first delivery = %+v", delivered[0])
	}
	if delivered[1] != (delivery{parent: 3, push: 6, source: "https://b.example"}) {
		t.Fatalf("second delivery = %+v", delivered[1])
	}
}

func TestLatePushPromiseAfterParentLifecycleEnd(t *testing.T) {
	const generation = 11
	source := mustSource(t, "https://a.example")
	var handled int
	admission := NewAdmission(generation, WithPushHandler(func(*Push) { handled++ }))

	if _, err := admission.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{source},
	}); err != nil {
		t.Fatal(err)
	}
	push, err := admission.AdmitPromise(promise(1, 2, generation, source))
	if err != nil {
		t.Fatal(err)
	}
	if handled != 1 {
		t.Fatalf("handled = %d, want 1", handled)
	}

	if !admission.EndParent(1, "response complete") {
		t.Fatal("EndParent returned false")
	}
	if got := push.State(); got != PushCanceled {
		t.Fatalf("push state after parent end = %s, want canceled", got)
	}
	if cause := context.Cause(push.Context()); !errors.Is(cause, ErrParentEnded) {
		t.Fatalf("push context cause = %v, want ErrParentEnded", cause)
	}

	if _, err := admission.AdmitPromise(promise(1, 4, generation, source)); !errors.Is(err, ErrParentEnded) {
		t.Fatalf("late promise error = %v, want ErrParentEnded", err)
	}
	if handled != 1 {
		t.Fatalf("late promise reached business callback: handled=%d", handled)
	}
	if _, err := admission.AdmitPromise(promise(1, 4, generation, source)); !errors.Is(err, ErrStreamIDReused) {
		t.Fatalf("replayed late promise error = %v, want ErrStreamIDReused", err)
	}

	stats := admission.Stats()
	if stats.ParentCompletions != 1 {
		t.Fatalf("ParentCompletions = %d, want 1", stats.ParentCompletions)
	}
	if stats.PushesCanceledByParent != 1 {
		t.Fatalf("PushesCanceledByParent = %d, want 1", stats.PushesCanceledByParent)
	}
}

func TestParentAndPushCancellationAreRecordedSeparately(t *testing.T) {
	const generation = 21
	source := mustSource(t, "https://a.example")
	admission := NewAdmission(generation)

	for _, parentID := range []uint32{1, 3} {
		if _, err := admission.RegisterParent(ParentSpec{
			StreamID:             parentID,
			ConnectionGeneration: generation,
			AuthorizedSources:    []Source{source},
		}); err != nil {
			t.Fatal(err)
		}
	}
	push1, err := admission.AdmitPromise(promise(1, 2, generation, source))
	if err != nil {
		t.Fatal(err)
	}
	push2, err := admission.AdmitPromise(promise(3, 4, generation, source))
	if err != nil {
		t.Fatal(err)
	}

	if !admission.CancelPush(2, "client reset push stream") {
		t.Fatal("CancelPush returned false")
	}
	stats := admission.Stats()
	if stats.PushCancellations != 1 || stats.ParentCancellations != 0 || stats.PushesCanceledByParent != 0 {
		t.Fatalf("direct push cancellation stats = %+v", stats)
	}
	if cause := context.Cause(push1.Context()); !errors.Is(cause, ErrPushCanceled) {
		t.Fatalf("push1 cause = %v, want ErrPushCanceled", cause)
	}
	if parent, _ := admission.Parent(1); parent.State() != ParentActive {
		t.Fatal("direct push cancellation affected its parent")
	}

	if !admission.CancelParent(3, "parent request canceled") {
		t.Fatal("CancelParent returned false")
	}
	stats = admission.Stats()
	if stats.PushCancellations != 1 {
		t.Fatalf("PushCancellations = %d, want 1 direct cancellation", stats.PushCancellations)
	}
	if stats.ParentCancellations != 1 || stats.PushesCanceledByParent != 1 {
		t.Fatalf("parent cancellation stats = %+v", stats)
	}
	if cause := context.Cause(push2.Context()); !errors.Is(cause, ErrParentCanceled) {
		t.Fatalf("push2 cause = %v, want ErrParentCanceled", cause)
	}
	if admission.CancelPush(4, "too late") {
		t.Fatal("CancelPush reported a second direct cancellation for a parent-canceled push")
	}

	var parentCanceledEvents, directPushCanceledEvents, propagatedPushCanceledEvents int
	for _, event := range admission.Events() {
		switch event.Kind {
		case EventParentCanceled:
			parentCanceledEvents++
		case EventPushCanceled:
			switch {
			case event.PushStreamID == 2 && event.Reason == "client reset push stream":
				directPushCanceledEvents++
			case event.PushStreamID == 4 && strings.Contains(event.Reason, "parent request canceled"):
				propagatedPushCanceledEvents++
			}
		}
	}
	if parentCanceledEvents != 1 || directPushCanceledEvents != 1 || propagatedPushCanceledEvents != 1 {
		t.Fatalf("cancel events: parent=%d direct=%d propagated=%d",
			parentCanceledEvents, directPushCanceledEvents, propagatedPushCanceledEvents)
	}
}

func TestPseudoHeaderValidationPrecedesAllocation(t *testing.T) {
	const generation = 31
	source := mustSource(t, "https://a.example")

	tests := []struct {
		name   string
		mutate func(PromiseSpec) PromiseSpec
	}{
		{
			name: "missing path",
			mutate: func(spec PromiseSpec) PromiseSpec {
				spec.Headers = spec.Headers[:3]
				return spec
			},
		},
		{
			name: "duplicate authority",
			mutate: func(spec PromiseSpec) PromiseSpec {
				spec.Headers = append(spec.Headers, HeaderField{Name: ":authority", Value: "b.example"})
				return spec
			},
		},
		{
			name: "unsafe method",
			mutate: func(spec PromiseSpec) PromiseSpec {
				spec.Headers[0].Value = "POST"
				return spec
			},
		},
		{
			name: "connection header",
			mutate: func(spec PromiseSpec) PromiseSpec {
				spec.Headers = append(spec.Headers, HeaderField{Name: "connection", Value: "close"})
				return spec
			},
		},
		{
			name: "host header",
			mutate: func(spec PromiseSpec) PromiseSpec {
				spec.Headers = append(spec.Headers, HeaderField{Name: "host", Value: "b.example"})
				return spec
			},
		},
		{
			name: "pseudo after regular",
			mutate: func(spec PromiseSpec) PromiseSpec {
				spec.Headers = []HeaderField{
					spec.Headers[0],
					{Name: "accept", Value: "*/*"},
					spec.Headers[1],
					spec.Headers[2],
					spec.Headers[3],
				}
				return spec
			},
		},
		{
			name: "control character",
			mutate: func(spec PromiseSpec) PromiseSpec {
				spec.Headers = append(spec.Headers, HeaderField{Name: "accept", Value: "*/*\n"})
				return spec
			},
		},
		{
			name: "bad path",
			mutate: func(spec PromiseSpec) PromiseSpec {
				return withPromisePath(spec, "no-leading-slash")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var allocations, deliveries int
			admission := NewAdmission(generation,
				WithAllocator(func(context.Context, *Push) error {
					allocations++
					return nil
				}),
				WithPushHandler(func(*Push) { deliveries++ }),
			)
			if _, err := admission.RegisterParent(ParentSpec{
				StreamID:             1,
				ConnectionGeneration: generation,
				AuthorizedSources:    []Source{source},
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := admission.AdmitPromise(test.mutate(promise(1, 2, generation, source))); !errors.Is(err, ErrMalformedPromise) {
				t.Fatalf("error = %v, want ErrMalformedPromise", err)
			}
			if allocations != 0 || deliveries != 0 {
				t.Fatalf("invalid pseudo-headers reached host: allocations=%d deliveries=%d", allocations, deliveries)
			}
		})
	}

	var allocations, deliveries int
	admission := NewAdmission(generation,
		WithAllocator(func(_ context.Context, push *Push) error {
			allocations++
			if push.Request().Headers.Get("Authorization") != "" || push.Request().Headers.Get("Cookie") != "" {
				t.Fatal("allocator received credential headers from the promise")
			}
			return nil
		}),
		WithPushHandler(func(*Push) { deliveries++ }),
	)
	if _, err := admission.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{source},
	}); err != nil {
		t.Fatal(err)
	}
	valid := promise(1, 2, generation, source)
	valid.Headers = append(valid.Headers,
		HeaderField{Name: "authorization", Value: "Bearer other-source"},
		HeaderField{Name: "cookie", Value: "session=other-source"},
	)
	if _, err := admission.AdmitPromise(valid); err != nil {
		t.Fatalf("valid promise: %v", err)
	}
	if allocations != 1 || deliveries != 1 {
		t.Fatalf("allocations=%d deliveries=%d, want 1 and 1", allocations, deliveries)
	}
}

func TestGenerationBindingAndStreamTombstones(t *testing.T) {
	const generation = 41
	source := mustSource(t, "https://a.example")
	admission := NewAdmission(generation)

	if _, err := admission.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation - 1,
		AuthorizedSources:    []Source{source},
	}); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("stale parent error = %v, want ErrGenerationMismatch", err)
	}
	if _, err := admission.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{source},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := admission.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{source},
	}); !errors.Is(err, ErrParentStreamReused) {
		t.Fatalf("parent reuse error = %v, want ErrParentStreamReused", err)
	}

	// A wrong-generation promise is not a frame on this connection, so it does
	// not consume stream 2.
	if _, err := admission.AdmitPromise(promise(1, 2, generation-1, source)); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("stale promise error = %v, want ErrGenerationMismatch", err)
	}
	if _, err := admission.AdmitPromise(promise(1, 2, generation, source)); err != nil {
		t.Fatalf("stream 2 after stale generation: %v", err)
	}
	if _, err := admission.AdmitPromise(promise(1, 5, generation, source)); !errors.Is(err, ErrInvalidStreamID) {
		t.Fatalf("odd promised stream error = %v, want ErrInvalidStreamID", err)
	}
	if _, err := admission.AdmitPromise(promise(1, 6, generation, source)); err != nil {
		t.Fatalf("stream 6: %v", err)
	}
	if _, err := admission.AdmitPromise(promise(1, 4, generation, source)); !errors.Is(err, ErrStreamIDOutOfOrder) {
		t.Fatalf("out-of-order stream error = %v, want ErrStreamIDOutOfOrder", err)
	}
	if _, err := admission.AdmitPromise(promise(1, 4, generation, source)); !errors.Is(err, ErrStreamIDReused) {
		t.Fatalf("out-of-order replay error = %v, want ErrStreamIDReused", err)
	}
	if !admission.CancelPush(2, "client reset") {
		t.Fatal("CancelPush returned false")
	}
	if _, err := admission.AdmitPromise(promise(1, 2, generation, source)); !errors.Is(err, ErrStreamIDReused) {
		t.Fatalf("canceled stream replay error = %v, want ErrStreamIDReused", err)
	}
	if !admission.CompletePush(6, "response complete") {
		t.Fatal("CompletePush returned false")
	}
	if admission.CompletePush(6, "duplicate completion") {
		t.Fatal("CompletePush returned true for an already closed push")
	}
	if _, err := admission.AdmitPromise(promise(1, 6, generation, source)); !errors.Is(err, ErrStreamIDReused) {
		t.Fatalf("completed stream replay error = %v, want ErrStreamIDReused", err)
	}

	// Stream IDs may restart only with a new connection generation.
	nextGeneration := NewAdmission(generation + 1)
	if _, err := nextGeneration.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation + 1,
		AuthorizedSources:    []Source{source},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := nextGeneration.AdmitPromise(promise(1, 2, generation+1, source)); err != nil {
		t.Fatalf("stream ID restart on new generation: %v", err)
	}
}

func TestParentContextCancellationBlocksLatePromise(t *testing.T) {
	const generation = 51
	source := mustSource(t, "https://a.example")
	parentCtx, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()

	var delivered int
	admission := NewAdmission(generation, WithPushHandler(func(*Push) { delivered++ }))
	if _, err := admission.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{source},
		Context:              parentCtx,
	}); err != nil {
		t.Fatal(err)
	}
	push, err := admission.AdmitPromise(promise(1, 2, generation, source))
	if err != nil {
		t.Fatal(err)
	}
	if delivered != 1 {
		t.Fatalf("delivered = %d, want 1", delivered)
	}

	cancelParent()
	select {
	case <-push.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("push context was not canceled with its parent")
	}
	if _, err := admission.AdmitPromise(promise(1, 4, generation, source)); !errors.Is(err, ErrParentCanceled) {
		t.Fatalf("late promise error = %v, want ErrParentCanceled", err)
	}
	if delivered != 1 {
		t.Fatalf("late promise reached callback: delivered=%d", delivered)
	}
	parent, _ := admission.Parent(1)
	if parent.State() != ParentCanceled {
		t.Fatalf("parent state = %s, want canceled", parent.State())
	}
	stats := admission.Stats()
	if stats.ParentCancellations != 1 || stats.PushesCanceledByParent != 1 {
		t.Fatalf("context cancellation stats = %+v", stats)
	}
}

func TestAllocationFailureNeverReachesHandler(t *testing.T) {
	const generation = 61
	source := mustSource(t, "https://a.example")
	var delivered int
	admission := NewAdmission(generation,
		WithAllocator(func(context.Context, *Push) error {
			return errors.New("pool exhausted")
		}),
		WithPushHandler(func(*Push) { delivered++ }),
	)
	if _, err := admission.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{source},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := admission.AdmitPromise(promise(1, 2, generation, source)); !errors.Is(err, ErrAllocationFailed) {
		t.Fatalf("error = %v, want ErrAllocationFailed", err)
	}
	if delivered != 0 {
		t.Fatalf("allocation failure reached handler %d times", delivered)
	}
	push, ok := admission.Push(2)
	if !ok || push.State() != PushCanceled {
		t.Fatalf("failed push record = %v, %s", ok, push.State())
	}
	if _, err := admission.AdmitPromise(promise(1, 2, generation, source)); !errors.Is(err, ErrStreamIDReused) {
		t.Fatalf("replay after allocation failure error = %v, want ErrStreamIDReused", err)
	}
	if stats := admission.Stats(); stats.AllocationFailures != 1 {
		t.Fatalf("AllocationFailures = %d, want 1", stats.AllocationFailures)
	}
}

func TestCancellationDuringAllocationNeverReachesHandler(t *testing.T) {
	const generation = 71
	source := mustSource(t, "https://a.example")
	started := make(chan struct{})
	release := make(chan struct{})
	var delivered int
	admission := NewAdmission(generation,
		WithAllocator(func(_ context.Context, _ *Push) error {
			close(started)
			<-release
			return nil
		}),
		WithPushHandler(func(*Push) { delivered++ }),
	)
	if _, err := admission.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{source},
	}); err != nil {
		t.Fatal(err)
	}

	type result struct {
		push *Push
		err  error
	}
	done := make(chan result, 1)
	go func() {
		push, err := admission.AdmitPromise(promise(1, 2, generation, source))
		done <- result{push: push, err: err}
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("allocator was not called")
	}
	if !admission.CancelPush(2, "client canceled while allocating") {
		t.Fatal("CancelPush returned false during allocation")
	}
	close(release)

	select {
	case got := <-done:
		if got.push != nil {
			t.Fatalf("canceled admission returned push %v", got.push)
		}
		if !errors.Is(got.err, ErrPushCanceled) {
			t.Fatalf("admission error = %v, want ErrPushCanceled", got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("AdmitPromise did not return")
	}
	if delivered != 0 {
		t.Fatalf("canceled allocation reached handler %d times", delivered)
	}
}

func TestCloseShutsDownConnectionGeneration(t *testing.T) {
	const generation = 81
	source := mustSource(t, "https://a.example")
	admission := NewAdmission(generation)
	if _, err := admission.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{source},
	}); err != nil {
		t.Fatal(err)
	}
	push, err := admission.AdmitPromise(promise(1, 2, generation, source))
	if err != nil {
		t.Fatal(err)
	}

	admission.Close("test done")
	if push.State() != PushCanceled {
		t.Fatalf("push state after close = %s", push.State())
	}
	if _, err := admission.RegisterParent(ParentSpec{
		StreamID:             3,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{source},
	}); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("register after close error = %v, want ErrAdmissionClosed", err)
	}
	if _, err := admission.AdmitPromise(promise(1, 4, generation, source)); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("promise after close error = %v, want ErrAdmissionClosed", err)
	}
	if stats := admission.Stats(); stats.AdmissionClosures != 1 {
		t.Fatalf("AdmissionClosures = %d, want 1", stats.AdmissionClosures)
	}
}

func TestAdmissionInputValidation(t *testing.T) {
	const generation = 91
	source := mustSource(t, "https://a.example")
	admission := NewAdmission(generation)

	if _, err := admission.RegisterParent(ParentSpec{
		StreamID:             2,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{source},
	}); !errors.Is(err, ErrInvalidStreamID) {
		t.Fatalf("even parent stream error = %v, want ErrInvalidStreamID", err)
	}
	if _, err := admission.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{{}},
	}); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("invalid source error = %v, want ErrInvalidSource", err)
	}

	parent, err := admission.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{source, source},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := parent.AuthorizedSources(); len(got) != 1 || got[0] != source {
		t.Fatalf("authorized source snapshot = %v", got)
	}
	if !parent.Authorizes(source) {
		t.Fatal("parent does not authorize its snapshot")
	}
	if parent.Authorizes(mustSource(t, "https://b.example")) {
		t.Fatal("parent authorized a foreign source")
	}

	if _, err := admission.AdmitPromise(promise(5, 2, generation, source)); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("unknown parent error = %v, want ErrParentNotFound", err)
	}
	if _, err := admission.AdmitPromise(promise(2, 4, generation, source)); !errors.Is(err, ErrInvalidStreamID) {
		t.Fatalf("even parent reference error = %v, want ErrInvalidStreamID", err)
	}
	if admission.CancelParent(99, "missing") {
		t.Fatal("CancelParent accepted an unknown parent")
	}
	if admission.CancelPush(98, "missing") {
		t.Fatal("CancelPush accepted an unknown push")
	}
	if admission.CompletePush(98, "missing") {
		t.Fatal("CompletePush accepted an unknown push")
	}
}

func TestEventLogRetentionKeepsNewestEvents(t *testing.T) {
	const generation = 101
	source := mustSource(t, "https://a.example")
	admission := NewAdmission(generation, WithEventCapacity(2))
	if _, err := admission.RegisterParent(ParentSpec{
		StreamID:             1,
		ConnectionGeneration: generation,
		AuthorizedSources:    []Source{source},
	}); err != nil {
		t.Fatal(err)
	}
	push, err := admission.AdmitPromise(promise(1, 2, generation, source))
	if err != nil {
		t.Fatal(err)
	}
	request := push.Request()
	request.Headers.Set("X-Test", "mutated")
	if got := push.Request().Headers.Get("X-Test"); got != "" {
		t.Fatalf("Push.Request exposed mutable headers: %q", got)
	}
	if !admission.CancelPush(2, "client reset") {
		t.Fatal("CancelPush returned false")
	}

	events := admission.Events()
	if len(events) != 2 {
		t.Fatalf("retained events = %d, want 2", len(events))
	}
	if events[0].Kind != EventPushAdmitted || events[1].Kind != EventPushCanceled {
		t.Fatalf("retained event kinds = %s, %s", events[0].Kind, events[1].Kind)
	}
}
