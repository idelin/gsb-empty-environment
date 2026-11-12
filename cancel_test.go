package netsec

import (
	"context"
	"errors"
	"testing"
)

// TestSeparateCancelRecording verifies that parent-request cancellation and
// pushed-stream cancellation are recorded as distinct events and distinct
// counters, and that both operations are idempotent.
func TestSeparateCancelRecording(t *testing.T) {
	controller, handler := newTestController()
	mustBind(t, controller, 1, "https://acme.example")
	admission, err := controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 2,
		Headers: promiseBlock(t, "https://acme.example"),
	})
	if err != nil {
		t.Fatalf("admit: %v", err)
	}

	// Canceling the push stream alone must not cancel the parent.
	if err := controller.CancelPush(2, CancelByServer); err != nil {
		t.Fatalf("CancelPush: %v", err)
	}
	if err := controller.CancelPush(2, CancelByServer); err != nil {
		t.Fatalf("idempotent CancelPush: %v", err)
	}
	if err := admission.Deliver(context.Background()); !errors.Is(err, ErrPushCanceled) {
		t.Fatalf("delivery after push cancel: want ErrPushCanceled, got %v", err)
	}

	// The parent remains usable and can still admit other pushes.
	other, err := controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 4,
		Headers: promiseBlock(t, "https://acme.example"),
	})
	if err != nil {
		t.Fatalf("admit second push after first canceled: %v", err)
	}

	// Now cancel the parent; its accounting is independent of the push.
	if err := controller.CancelParent(1, CancelByClient); err != nil {
		t.Fatalf("CancelParent: %v", err)
	}
	if err := controller.CancelParent(1, CancelByClient); err != nil {
		t.Fatalf("idempotent CancelParent: %v", err)
	}
	if err := other.Deliver(context.Background()); !errors.Is(err, ErrParentCanceled) {
		t.Fatalf("delivery after parent cancel: want ErrParentCanceled, got %v", err)
	}

	stats := controller.Stats()
	if stats.ParentCanceled != 1 {
		t.Errorf("ParentCanceled: want 1, got %d", stats.ParentCanceled)
	}
	if stats.PushCanceled != 1 {
		t.Errorf("PushCanceled: want 1, got %d", stats.PushCanceled)
	}
	if stats.Delivered != 0 {
		t.Errorf("Delivered: want 0, got %d", stats.Delivered)
	}

	var parentCancels, pushCancels int
	for _, event := range controller.Events() {
		switch event.Kind {
		case EventParentCanceled:
			parentCancels++
			if event.Reason != CancelByClient {
				t.Errorf("parent cancel reason: want client, got %s", event.Reason)
			}
		case EventPushCanceled:
			pushCancels++
			if event.Reason != CancelByServer {
				t.Errorf("push cancel reason: want server, got %s", event.Reason)
			}
			if event.PromisedID != 2 {
				t.Errorf("push cancel stream: want 2, got %d", event.PromisedID)
			}
		}
	}
	if parentCancels != 1 || pushCancels != 1 {
		t.Fatalf("cancel events parent=%d push=%d, want 1/1", parentCancels, pushCancels)
	}
	if len(handler.snapshot()) != 0 {
		t.Fatal("handler must not receive any canceled resource")
	}

	// Canceling unknown streams is an error rather than a silent no-op.
	if err := controller.CancelParent(7, CancelByClient); !errors.Is(err, ErrUnknownParent) {
		t.Errorf("unknown parent cancel: want ErrUnknownParent, got %v", err)
	}
	if err := controller.CancelPush(8, CancelByServer); !errors.Is(err, ErrUnknownAdmission) {
		t.Errorf("unknown push cancel: want ErrUnknownAdmission, got %v", err)
	}
}

// TestStreamIDReuse verifies that a promised (even) stream identifier cannot
// be reserved twice within one connection generation, while reuse after a
// generation advance is legitimate. It also proves malformed promises do not
// consume an identifier because they allocate nothing.
func TestStreamIDReuse(t *testing.T) {
	controller, _ := newTestController()
	mustBind(t, controller, 1, "https://acme.example")

	first, err := controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 2,
		Headers: promiseBlock(t, "https://acme.example"),
	})
	if err != nil {
		t.Fatalf("first admit: %v", err)
	}

	_, err = controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 2,
		Headers: promiseBlock(t, "https://acme.example"),
	})
	if !errors.Is(err, ErrStreamInUse) {
		t.Fatalf("stream reuse: want ErrStreamInUse, got %v", err)
	}

	// Client streams are odd; a promised stream must be even.
	if err := controller.BindParent(ParentRequest{StreamID: 2}); !errors.Is(err, ErrInvalidStreamID) {
		t.Errorf("even parent: want ErrInvalidStreamID, got %v", err)
	}
	_, err = controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 3,
		Headers: promiseBlock(t, "https://acme.example"),
	})
	if !errors.Is(err, ErrInvalidStreamID) {
		t.Errorf("odd promise: want ErrInvalidStreamID, got %v", err)
	}

	// A malformed block on a fresh ID must not reserve that ID: retrying with a
	// valid block on the same identifier has to succeed.
	bad := promiseBlock(t, "https://acme.example")
	bad[0].Value = "POST"
	if _, err := controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 6, Headers: bad,
	}); !errors.Is(err, ErrInvalidPseudoHeader) {
		t.Fatalf("malformed promise: want ErrInvalidPseudoHeader, got %v", err)
	}
	_, err = controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 6,
		Headers: promiseBlock(t, "https://acme.example"),
	})
	if err != nil {
		t.Fatalf("retry after malformed block should not hit reuse: %v", err)
	}

	// Generation advance opens a fresh identifier space.
	controller.AdvanceGeneration()
	mustBind(t, controller, 1, "https://acme.example")
	reborn, err := controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 2,
		Headers: promiseBlock(t, "https://acme.example"),
	})
	if err != nil {
		t.Fatalf("same ID on new generation should be allowed: %v", err)
	}
	if reborn.Resource().Generation == first.Resource().Generation {
		t.Fatal("reused stream must belong to the new generation")
	}
}
