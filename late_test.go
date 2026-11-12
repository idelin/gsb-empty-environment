package netsec

import (
	"context"
	"errors"
	"testing"
)

// TestLatePushPromise covers promises that arrive after their parent's
// authorization context is no longer usable: unknown parents, canceled or
// completed parents, and parents left behind on a prior connection
// generation. None of these promises may reach the business handler.
func TestLatePushPromise(t *testing.T) {
	t.Run("unknown parent", func(t *testing.T) {
		controller, handler := newTestController()
		_, err := controller.AdmitPromise(PushPromise{
			ParentStreamID: 99, PromisedStreamID: 2,
			Headers: promiseBlock(t, "https://acme.example"),
		})
		if !errors.Is(err, ErrUnknownParent) {
			t.Fatalf("want ErrUnknownParent, got %v", err)
		}
		if len(handler.snapshot()) != 0 {
			t.Fatal("handler must not see a promise with no bound parent")
		}
	})

	t.Run("promise after parent canceled", func(t *testing.T) {
		controller, handler := newTestController()
		mustBind(t, controller, 1, "https://acme.example")
		if err := controller.CancelParent(1, CancelByClient); err != nil {
			t.Fatalf("CancelParent: %v", err)
		}
		_, err := controller.AdmitPromise(PushPromise{
			ParentStreamID: 1, PromisedStreamID: 2,
			Headers: promiseBlock(t, "https://acme.example"),
		})
		if !errors.Is(err, ErrParentCanceled) {
			t.Fatalf("want ErrParentCanceled, got %v", err)
		}
		if len(handler.snapshot()) != 0 {
			t.Fatal("handler must not see a promise after parent cancel")
		}
	})

	t.Run("promise after parent completed", func(t *testing.T) {
		controller, handler := newTestController()
		mustBind(t, controller, 1, "https://acme.example")
		if err := controller.CompleteParent(1); err != nil {
			t.Fatalf("CompleteParent: %v", err)
		}
		_, err := controller.AdmitPromise(PushPromise{
			ParentStreamID: 1, PromisedStreamID: 2,
			Headers: promiseBlock(t, "https://acme.example"),
		})
		if !errors.Is(err, ErrParentCompleted) {
			t.Fatalf("want ErrParentCompleted, got %v", err)
		}
		if len(handler.snapshot()) != 0 {
			t.Fatal("handler must not see a late promise after completion")
		}
	})

	t.Run("late promise from prior generation", func(t *testing.T) {
		controller, handler := newTestController()
		mustBind(t, controller, 1, "https://acme.example")

		admission, err := controller.AdmitPromise(PushPromise{
			ParentStreamID: 1, PromisedStreamID: 2,
			Headers: promiseBlock(t, "https://acme.example"),
		})
		if err != nil {
			t.Fatalf("admit before migration: %v", err)
		}

		// Physical connection re-establishes: new generation, new stream space.
		if got := controller.AdvanceGeneration(); got != 2 {
			t.Fatalf("generation after advance: want 2, got %d", got)
		}

		// A frame replaying the old parent/promise identifiers is stale.
		_, err = controller.AdmitPromise(PushPromise{
			ParentStreamID: 1, PromisedStreamID: 4,
			Headers: promiseBlock(t, "https://acme.example"),
		})
		if !errors.Is(err, ErrGenerationMismatch) {
			t.Fatalf("stale parent promise: want ErrGenerationMismatch, got %v", err)
		}

		// Even an already-admitted promise from the old generation cannot
		// deliver once the connection incarnation changes underneath it.
		if err := admission.Deliver(context.Background()); !errors.Is(err, ErrGenerationMismatch) {
			t.Fatalf("stale admission delivery: want ErrGenerationMismatch, got %v", err)
		}

		// Stream IDs may be reused, but only after rebinding on the new
		// generation. The old binding does not authorize the new stream.
		mustBind(t, controller, 1, "https://acme.example")
		readmitted, err := controller.AdmitPromise(PushPromise{
			ParentStreamID: 1, PromisedStreamID: 2,
			Headers: promiseBlock(t, "https://acme.example"),
		})
		if err != nil {
			t.Fatalf("readmit on new generation: %v", err)
		}
		if readmitted.Resource().Generation != 2 {
			t.Fatalf("readmitted generation: want 2, got %d", readmitted.Resource().Generation)
		}
		if err := readmitted.Deliver(context.Background()); err != nil {
			t.Fatalf("deliver readmitted: %v", err)
		}

		delivered := handler.snapshot()
		if len(delivered) != 1 || delivered[0].Generation != 2 {
			t.Fatalf("only the generation-2 resource may be delivered, got %+v", delivered)
		}
	})
}

func mustBind(t *testing.T, controller *Controller, streamID uint32, origins ...string) {
	t.Helper()
	authorized := make([]Origin, 0, len(origins))
	for _, raw := range origins {
		authorized = append(authorized, mustOrigin(t, raw))
	}
	if err := controller.BindParent(ParentRequest{StreamID: streamID, Authorized: authorized}); err != nil {
		t.Fatalf("BindParent(%d): %v", streamID, err)
	}
}
