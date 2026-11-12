package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustOrigin(t *testing.T, raw string) Origin {
	t.Helper()
	origin, err := ParseOrigin(raw)
	if err != nil {
		t.Fatalf("ParseOrigin(%q): %v", raw, err)
	}
	return origin
}

// promiseBlock builds a well-formed pushed GET header block for target.
func promiseBlock(t *testing.T, target string) []HeaderField {
	t.Helper()
	origin := mustOrigin(t, target)
	return []HeaderField{
		{Name: ":method", Value: "GET"},
		{Name: ":scheme", Value: origin.Scheme()},
		{Name: ":authority", Value: authorityOf(origin)},
		{Name: ":path", Value: "/resource"},
	}
}

func authorityOf(o Origin) string {
	if o.Port() == 80 && o.Scheme() == "http" {
		return o.Host()
	}
	if o.Port() == 443 && o.Scheme() == "https" {
		return o.Host()
	}
	return fmt.Sprintf("%s:%d", o.Host(), o.Port())
}

type recordingHandler struct {
	mu        sync.Mutex
	resources []PushResource
}

func (h *recordingHandler) handle(r PushResource) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.resources = append(h.resources, r)
}

func (h *recordingHandler) snapshot() []PushResource {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]PushResource, len(h.resources))
	copy(out, h.resources)
	return out
}

func newTestController() (*Controller, *recordingHandler) {
	handler := &recordingHandler{}
	return NewController(handler.handle), handler
}

// TestMultiOriginAuthorization drives several parent requests with disjoint
// authorization sets over one connection. A promise is admitted only when its
// canonical origin is present in its own parent's set; a promise carrying
// another parent's context is refused and never reaches the handler.
func TestMultiOriginAuthorization(t *testing.T) {
	controller, handler := newTestController()

	acme := mustOrigin(t, "https://acme.example")
	cdn := mustOrigin(t, "https://cdn.example")
	partner := mustOrigin(t, "https://partner.example")

	if err := controller.BindParent(ParentRequest{
		StreamID:   1,
		Authorized: []Origin{acme, cdn},
	}); err != nil {
		t.Fatalf("BindParent stream 1: %v", err)
	}
	if err := controller.BindParent(ParentRequest{
		StreamID:   3,
		Authorized: []Origin{partner},
	}); err != nil {
		t.Fatalf("BindParent stream 3: %v", err)
	}

	// Authorized push for stream 1 -> acme origin.
	admit1, err := controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 2, Headers: promiseBlock(t, "https://acme.example"),
	})
	if err != nil {
		t.Fatalf("admit acme push: %v", err)
	}
	if err := admit1.Deliver(context.Background()); err != nil {
		t.Fatalf("deliver acme push: %v", err)
	}

	// Second authorized origin on the same parent (CDN).
	admit2, err := controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 4, Headers: promiseBlock(t, "https://cdn.example"),
	})
	if err != nil {
		t.Fatalf("admit cdn push: %v", err)
	}
	if err := admit2.Deliver(context.Background()); err != nil {
		t.Fatalf("deliver cdn push: %v", err)
	}

	// Cross-context theft: stream 3's parent only authorizes partner.example,
	// so pushing acme.example under it must fail even though stream 1 may do so.
	_, err = controller.AdmitPromise(PushPromise{
		ParentStreamID: 3, PromisedStreamID: 6, Headers: promiseBlock(t, "https://acme.example"),
	})
	if !errors.Is(err, ErrOriginNotAuthorized) {
		t.Fatalf("cross-origin push: want ErrOriginNotAuthorized, got %v", err)
	}

	// partner.example is valid for stream 3 but must not leak to stream 1.
	_, err = controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 8, Headers: promiseBlock(t, "https://partner.example"),
	})
	if !errors.Is(err, ErrOriginNotAuthorized) {
		t.Fatalf("foreign-origin push: want ErrOriginNotAuthorized, got %v", err)
	}

	// Sub-domain is a distinct origin and is not covered by a bare parent set.
	_, err = controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 10, Headers: promiseBlock(t, "https://api.acme.example"),
	})
	if !errors.Is(err, ErrOriginNotAuthorized) {
		t.Fatalf("subdomain push: want ErrOriginNotAuthorized, got %v", err)
	}

	// Scheme/port differences change the origin identity.
	_, err = controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 12, Headers: promiseBlock(t, "http://acme.example"),
	})
	if !errors.Is(err, ErrOriginNotAuthorized) {
		t.Fatalf("scheme-downgrade push: want ErrOriginNotAuthorized, got %v", err)
	}

	// The legitimate partner push still works and is correctly attributed.
	admit3, err := controller.AdmitPromise(PushPromise{
		ParentStreamID: 3, PromisedStreamID: 14, Headers: promiseBlock(t, "https://partner.example"),
	})
	if err != nil {
		t.Fatalf("admit partner push: %v", err)
	}
	if err := admit3.Deliver(context.Background()); err != nil {
		t.Fatalf("deliver partner push: %v", err)
	}

	delivered := handler.snapshot()
	if len(delivered) != 3 {
		t.Fatalf("handler deliveries: want 3, got %d", len(delivered))
	}
	wantOrigins := []string{"https://acme.example:443", "https://cdn.example:443", "https://partner.example:443"}
	for i, resource := range delivered {
		if got := resource.Request.Origin.String(); got != wantOrigins[i] {
			t.Errorf("delivery %d origin: want %s, got %s", i, wantOrigins[i], got)
		}
		if resource.Generation != 1 {
			t.Errorf("delivery %d generation: want 1, got %d", i, resource.Generation)
		}
		if !containsOrigin(resource.Authorized, resource.Request.Origin) {
			t.Errorf("delivery %d does not carry its own authorizing origin", i)
		}
	}

	stats := controller.Stats()
	if stats.Admitted != 3 || stats.Delivered != 3 || stats.Rejected != 4 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	// Every rejected promise must appear in the audit trail with its error.
	rejections := 0
	for _, event := range controller.Events() {
		if event.Kind == EventPromiseRejected {
			rejections++
			if event.Err == nil {
				t.Errorf("rejection event missing error: %+v", event)
			}
		}
	}
	if rejections != 4 {
		t.Fatalf("rejection events: want 4, got %d", rejections)
	}

}

func containsOrigin(set []Origin, target Origin) bool {
	for _, origin := range set {
		if origin == target {
			return true
		}
	}
	return false
}
