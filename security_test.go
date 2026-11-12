package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestCallbackNeverReceivesUnauthorizedResource is the end-to-end security
// assertion: over one shared connection carrying multiple authenticated
// origins, plus an adversarial batch of late and replayed PUSH_PROMISEs, the
// business callback receives resources whose origins were authorized by their
// own parent context and nothing else.
func TestCallbackNeverReceivesUnauthorizedResource(t *testing.T) {
	var (
		mu        sync.Mutex
		delivered []PushResource
	)
	controller := NewController(func(resource PushResource) {
		mu.Lock()
		delivered = append(delivered, resource)
		mu.Unlock()
	})

	origins := []Origin{
		mustOrigin(t, "https://a.example"),
		mustOrigin(t, "https://b.example"),
		mustOrigin(t, "https://c.example"),
	}

	// Three parent streams on the same connection, each with its own context.
	const parentA, parentB, parentC uint32 = 1, 3, 5
	mustBind(t, controller, parentA, "https://a.example")
	mustBind(t, controller, parentB, "https://b.example")
	mustBind(t, controller, parentC, "https://c.example")

	// Promised stream IDs are unique within generation 1.
	var promisedID uint32 = 2
	nextID := func() uint32 {
		id := promisedID
		promisedID += 2
		return id
	}

	deliver := func(parent uint32, origin string) {
		t.Helper()
		admission, err := controller.AdmitPromise(PushPromise{
			ParentStreamID:   parent,
			PromisedStreamID: nextID(),
			Headers:          promiseBlock(t, origin),
		})
		if err != nil {
			t.Fatalf("admit parent=%d origin=%s: %v", parent, origin, err)
		}
		if err := admission.Deliver(context.Background()); err != nil {
			t.Fatalf("deliver parent=%d: %v", parent, err)
		}
	}

	// Legitimate, in-context pushes.
	deliver(parentA, "https://a.example")
	deliver(parentB, "https://b.example")
	deliver(parentC, "https://c.example")

	// Hostile matrix: every parent is pushed every foreign origin.
	parents := []uint32{parentA, parentB, parentC}
	for _, parent := range parents {
		for _, origin := range origins {
			target := origin.String()
			authorizedOrigin := originForParent(parent, origins)
			if target == authorizedOrigin {
				continue
			}
			_, err := controller.AdmitPromise(PushPromise{
				ParentStreamID:   parent,
				PromisedStreamID: nextID(),
				Headers:          promiseBlock(t, target),
			})
			if !errors.Is(err, ErrOriginNotAuthorized) {
				t.Fatalf("foreign push parent=%d origin=%s: want ErrOriginNotAuthorized, got %v",
					parent, target, err)
			}
		}
	}

	// Cancel parent A, then attempt late pushes and a canceled-stream delivery.
	if err := controller.CancelParent(parentA, CancelByClient); err != nil {
		t.Fatalf("cancel parent A: %v", err)
	}
	_, err := controller.AdmitPromise(PushPromise{
		ParentStreamID: parentA, PromisedStreamID: nextID(),
		Headers: promiseBlock(t, "https://a.example"),
	})
	if !errors.Is(err, ErrParentCanceled) {
		t.Fatalf("late push after cancel: want ErrParentCanceled, got %v", err)
	}

	// Replay frames across a connection migration.
	controller.AdvanceGeneration()
	_, err = controller.AdmitPromise(PushPromise{
		ParentStreamID: parentB, PromisedStreamID: nextID(),
		Headers: promiseBlock(t, "https://b.example"),
	})
	if !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("replay on new generation: want ErrGenerationMismatch, got %v", err)
	}

	// Re-bound on the new generation and push only B's own origin.
	mustBind(t, controller, parentB, "https://b.example")
	deliver(parentB, "https://b.example")

	mu.Lock()
	got := append([]PushResource(nil), delivered...)
	mu.Unlock()

	if len(got) != 4 {
		t.Fatalf("deliveries: want 4 (3 gen-1 + 1 gen-2), got %d", len(got))
	}

	expected := map[string]int{
		"https://a.example:443": 1,
		"https://b.example:443": 2,
		"https://c.example:443": 1,
	}
	seen := map[string]int{}
	for _, resource := range got {
		origin := resource.Request.Origin
		if !containsOrigin(resource.Authorized, origin) {
			t.Fatalf("resource %s delivered without its authorizing origin in context", origin)
		}
		// The delivered origin must equal the parent's bound context.
		if want := originForParent(resource.ParentStreamID, origins); want != origin.String() {
			t.Fatalf("parent %d received foreign origin %s (want %s)",
				resource.ParentStreamID, origin, want)
		}
		seen[origin.String()]++
	}
	for origin, count := range expected {
		if seen[origin] != count {
			t.Errorf("origin %s deliveries: want %d, got %d", origin, count, seen[origin])
		}
	}

	stats := controller.Stats()
	if stats.Delivered != 4 {
		t.Errorf("delivered counter: want 4, got %d", stats.Delivered)
	}
	if stats.Rejected == 0 {
		t.Error("rejected counter must record every refused promise")
	}
	if stats.ParentCanceled != 1 || stats.PushCanceled != 0 {
		t.Errorf("cancel accounting wrong: %+v", stats)
	}
}

func originForParent(parent uint32, origins []Origin) string {
	index := int((parent - 1) / 2)
	if index < 0 || index >= len(origins) {
		return ""
	}
	return origins[index].String()
}

// TestConcurrentAdmission exercises the controller under concurrent frame
// dispatch and business goroutines; run with -race for the memory model.
func TestConcurrentAdmission(t *testing.T) {
	handler := &recordingHandler{}
	controller := NewController(handler.handle)

	const parents = 8
	originList := make([]Origin, parents)
	for i := 0; i < parents; i++ {
		streamID := uint32(1 + 2*i)
		origin, err := ParseOrigin(fmt.Sprintf("https://origin%d.example", i))
		if err != nil {
			t.Fatalf("origin %d: %v", i, err)
		}
		originList[i] = origin
		if err := controller.BindParent(ParentRequest{
			StreamID: streamID, Authorized: []Origin{origin},
		}); err != nil {
			t.Fatalf("bind %d: %v", streamID, err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < parents; i++ {
		parentID := uint32(1 + 2*i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for push := 0; push < 20; push++ {
				promisedID := uint32(2 + (i*40+push)*2)
				admission, err := controller.AdmitPromise(PushPromise{
					ParentStreamID:   parentID,
					PromisedStreamID: promisedID,
					Headers:          promiseBlock(t, originList[i].String()),
				})
				if err != nil {
					t.Errorf("admit parent=%d push=%d: %v", parentID, push, err)
					return
				}
				if err := admission.Deliver(context.Background()); err != nil {
					t.Errorf("deliver parent=%d push=%d: %v", parentID, push, err)
				}
			}
		}()
	}

	wg.Wait()

	// Phase two: concurrent idempotent cancellation of every parent, plus
	// concurrent reads, while pushes are no longer being admitted.
	var cancelWG sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		cancelWG.Add(1)
		go func() {
			defer cancelWG.Done()
			for i := 0; i < parents; i++ {
				_ = controller.CancelParent(uint32(1+2*i), CancelByTimeout)
				_ = controller.Stats()
				_ = controller.Events()
			}
		}()
	}
	cancelWG.Wait()

	for _, resource := range handler.snapshot() {
		want := originForParent(resource.ParentStreamID, originList)
		if resource.Request.Origin.String() != want {
			t.Fatalf("race: parent %d got origin %s, want %s",
				resource.ParentStreamID, resource.Request.Origin, want)
		}
	}

	stats := controller.Stats()
	if stats.Admitted != parents*20 {
		t.Fatalf("admitted: want %d, got %d", parents*20, stats.Admitted)
	}
	if stats.Delivered != parents*20 {
		t.Fatalf("delivered: want %d, got %d", parents*20, stats.Delivered)
	}
	if stats.ParentCanceled != parents {
		t.Fatalf("parent canceled (idempotent count): want %d, got %d", parents, stats.ParentCanceled)
	}
}
