package netsec

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const (
	funcA   = "func-a"
	funcB   = "func-b"
	trigA   = "trig-a"
	trigB   = "trig-b"
	topicA  = "topic-a"
	queueA  = "queue-a"
	idemKey = "idem-1"
)

type harness struct {
	source *Source
	reg    *Registry
	keyA   []byte
	keyB   []byte
}

func newHarness(t *testing.T, functionID string) *harness {
	t.Helper()
	reg := NewRegistry()
	keyA := []byte("signing-key-a")
	keyB := []byte("signing-key-b")

	mustRegister(t, reg, Trigger{
		ID: trigA, FunctionID: funcA, Topic: topicA, Queue: queueA, SigningKey: keyA,
	})
	mustRegister(t, reg, Trigger{
		ID: trigB, FunctionID: funcB, Topic: "topic-b", Queue: "queue-b", SigningKey: keyB,
	})

	return &harness{
		source: NewSource(functionID, reg, NewMemoryIdempotencyStore()),
		reg:    reg,
		keyA:   keyA,
		keyB:   keyB,
	}
}

func mustRegister(t *testing.T, reg *Registry, trig Trigger) {
	t.Helper()
	if err := reg.Register(trig); err != nil {
		t.Fatalf("register %q: %v", trig.ID, err)
	}
}

func goodEvent() InboundEvent {
	return InboundEvent{
		TriggerID:      trigA,
		FunctionID:     funcA,
		Topic:          topicA,
		Queue:          queueA,
		IdempotencyKey: idemKey,
		Object:         []byte(`{"action":"resize","id":42}`),
	}
}

func signed(t *testing.T, e InboundEvent, key []byte) InboundEvent {
	t.Helper()
	SignEvent(&e, key)
	if len(e.Signature) == 0 {
		t.Fatal("signature not applied")
	}
	return e
}

func TestProcessAcceptsLegitimateEvent(t *testing.T) {
	h := newHarness(t, funcA)

	verified, err := h.source.Process(signed(t, goodEvent(), h.keyA))
	if err != nil {
		t.Fatalf("legitimate event rejected: %v", err)
	}
	if verified.TriggerID != trigA || verified.FunctionID != funcA ||
		verified.Topic != topicA || verified.Queue != queueA ||
		verified.IdempotencyKey != idemKey {
		t.Fatalf("verified event fields mismatch: %+v", verified)
	}
	if !bytes.Equal(verified.Object, goodEvent().Object) {
		t.Fatalf("object mismatch: %s", verified.Object)
	}
}

func TestProcessRejectsDirectUnsignedInvocation(t *testing.T) {
	h := newHarness(t, funcA)

	// Attacker calls the endpoint directly and supplies every field, but no
	// trusted signature exists.
	_, err := h.source.Process(goodEvent())
	if !errors.Is(err, ErrMissingSignature) {
		t.Fatalf("want ErrMissingSignature, got %v", err)
	}
}

func TestProcessRejectsForgedSignatureAndTamperedFields(t *testing.T) {
	h := newHarness(t, funcA)

	t.Run("wrong signing key", func(t *testing.T) {
		_, err := h.source.Process(signed(t, goodEvent(), []byte("attacker-key")))
		if !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("want ErrInvalidSignature, got %v", err)
		}
	})

	tamperCases := map[string]func(*InboundEvent){
		"object":      func(e *InboundEvent) { e.Object = []byte(`{"action":"delete"}`) },
		"queue":       func(e *InboundEvent) { e.Queue = "attacker-queue" },
		"topic":       func(e *InboundEvent) { e.Topic = "attacker-topic" },
		"function":    func(e *InboundEvent) { e.FunctionID = funcB },
		"idempotency": func(e *InboundEvent) { e.IdempotencyKey = "forged" },
	}

	for name, tamper := range tamperCases {
		t.Run(name, func(t *testing.T) {
			e := signed(t, goodEvent(), h.keyA)
			tamper(&e) // mutate after signing, as an attacker would

			_, err := h.source.Process(e)
			if !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("want ErrInvalidSignature for tampered %s, got %v", name, err)
			}
		})
	}
}

func TestProcessRejectsCrossFunctionEvents(t *testing.T) {
	t.Run("trigger bound to another function", func(t *testing.T) {
		h := newHarness(t, funcA)

		// A perfectly valid event signed by trigger B, which is only
		// permitted to invoke func-b.
		e := InboundEvent{
			TriggerID: trigB, FunctionID: funcB, Topic: "topic-b",
			Queue: "queue-b", IdempotencyKey: "idem-b", Object: []byte("{}"),
		}
		_, err := h.source.Process(signed(t, e, h.keyB))
		if !errors.Is(err, ErrCrossFunction) {
			t.Fatalf("want ErrCrossFunction, got %v", err)
		}
	})

	t.Run("event names another function", func(t *testing.T) {
		h := newHarness(t, funcA)

		// Trigger A is bound to func-a, yet the signed envelope targets
		// func-b and is replayed at the func-a endpoint.
		e := goodEvent()
		e.FunctionID = funcB
		_, err := h.source.Process(signed(t, e, h.keyA))
		if !errors.Is(err, ErrFunctionMismatch) {
			t.Fatalf("want ErrFunctionMismatch, got %v", err)
		}
	})
}

func TestProcessRejectsBindingMismatchesWithValidSignature(t *testing.T) {
	h := newHarness(t, funcA)

	t.Run("topic mismatch", func(t *testing.T) {
		e := goodEvent()
		e.Topic = "other-topic"
		_, err := h.source.Process(signed(t, e, h.keyA))
		if !errors.Is(err, ErrTopicMismatch) {
			t.Fatalf("want ErrTopicMismatch, got %v", err)
		}
	})

	t.Run("queue mismatch", func(t *testing.T) {
		e := goodEvent()
		e.Queue = "other-queue"
		_, err := h.source.Process(signed(t, e, h.keyA))
		if !errors.Is(err, ErrQueueMismatch) {
			t.Fatalf("want ErrQueueMismatch, got %v", err)
		}
	})
}

func TestProcessRejectsUnknownTrigger(t *testing.T) {
	h := newHarness(t, funcA)

	t.Run("unregistered trigger id", func(t *testing.T) {
		e := goodEvent()
		e.TriggerID = "trig-unknown"
		_, err := h.source.Process(signed(t, e, h.keyA))
		if !errors.Is(err, ErrTriggerNotRegistered) {
			t.Fatalf("want ErrTriggerNotRegistered, got %v", err)
		}
	})

	t.Run("missing trigger id", func(t *testing.T) {
		e := goodEvent()
		e.TriggerID = ""
		_, err := h.source.Process(signed(t, e, h.keyA))
		if !errors.Is(err, ErrMissingTriggerID) {
			t.Fatalf("want ErrMissingTriggerID, got %v", err)
		}
	})
}

func TestProcessRejectsMissingIdempotencyKey(t *testing.T) {
	h := newHarness(t, funcA)

	e := goodEvent()
	e.IdempotencyKey = ""
	_, err := h.source.Process(signed(t, e, h.keyA))
	if !errors.Is(err, ErrMissingIdempotencyKey) {
		t.Fatalf("want ErrMissingIdempotencyKey, got %v", err)
	}
}

func TestProcessDeduplicatesRepeatDelivery(t *testing.T) {
	h := newHarness(t, funcA)

	first, err := h.source.Process(signed(t, goodEvent(), h.keyA))
	if err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	// Exact redelivery of the same envelope must never run twice.
	_, err = h.source.Process(signed(t, goodEvent(), h.keyA))
	if !errors.Is(err, ErrDuplicateEvent) {
		t.Fatalf("want ErrDuplicateEvent, got %v", err)
	}

	// A distinct idempotency key represents new work and is accepted.
	other := goodEvent()
	other.IdempotencyKey = "idem-2"
	second, err := h.source.Process(signed(t, other, h.keyA))
	if err != nil {
		t.Fatalf("second distinct event: %v", err)
	}
	if second.IdempotencyKey == first.IdempotencyKey {
		t.Fatal("distinct event was treated as duplicate")
	}
}

func TestProcessConcurrentDuplicatesProcessedOnce(t *testing.T) {
	h := newHarness(t, funcA)
	e := signed(t, goodEvent(), h.keyA)

	const goroutines = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted, duplicates := 0, 0

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := h.source.Process(e)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				accepted++
			case errors.Is(err, ErrDuplicateEvent):
				duplicates++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if accepted != 1 {
		t.Fatalf("want exactly 1 accepted delivery, got %d", accepted)
	}
	if duplicates != goroutines-1 {
		t.Fatalf("want %d duplicates, got %d", goroutines-1, duplicates)
	}
}

func TestCanonicalFramingIsUnambiguous(t *testing.T) {
	key := []byte("signing-key-a")

	// Under naive concatenation "topic|queue" both pairs below collapse to
	// "xy". Length-prefixed framing must keep them distinct.
	first := goodEvent()
	first.Topic, first.Queue = "x", "y"
	second := goodEvent()
	second.Topic, second.Queue = "xy", ""

	if bytes.Equal(canonicalEvent(first), canonicalEvent(second)) {
		t.Fatal("length-prefixed canonical framing collided")
	}
	if validSignature(key, sign(key, first), second) {
		t.Fatal("signature for one event validated a different event")
	}
}

func TestRegistryValidation(t *testing.T) {
	reg := NewRegistry()

	invalid := []Trigger{
		{ID: "", FunctionID: funcA, Topic: topicA, Queue: queueA, SigningKey: []byte("k")},
		{ID: "x", FunctionID: "", Topic: topicA, Queue: queueA, SigningKey: []byte("k")},
		{ID: "x", FunctionID: funcA, Topic: "", Queue: queueA, SigningKey: []byte("k")},
		{ID: "x", FunctionID: funcA, Topic: topicA, Queue: "", SigningKey: []byte("k")},
		{ID: "x", FunctionID: funcA, Topic: topicA, Queue: queueA, SigningKey: nil},
	}
	for i, trig := range invalid {
		if err := reg.Register(trig); !errors.Is(err, ErrInvalidTrigger) {
			t.Fatalf("case %d: want ErrInvalidTrigger, got %v", i, err)
		}
	}

	valid := Trigger{ID: trigA, FunctionID: funcA, Topic: topicA, Queue: queueA, SigningKey: []byte("k")}
	if err := reg.Register(valid); err != nil {
		t.Fatalf("valid trigger: %v", err)
	}
	if err := reg.Register(valid); !errors.Is(err, ErrTriggerAlreadyRegistered) {
		t.Fatalf("want ErrTriggerAlreadyRegistered, got %v", err)
	}
	if _, ok := reg.Lookup("missing"); ok {
		t.Fatal("lookup of unknown trigger succeeded")
	}
}

func TestMemoryIdempotencyStoreNamespacesKeys(t *testing.T) {
	store := NewMemoryIdempotencyStore()

	fresh, err := store.CheckAndStore(trigA + ":" + idemKey)
	if err != nil || !fresh {
		t.Fatalf("first claim: fresh=%v err=%v", fresh, err)
	}
	fresh, err = store.CheckAndStore(trigA + ":" + idemKey)
	if err != nil || fresh {
		t.Fatalf("second claim: fresh=%v err=%v", fresh, err)
	}
	// Same business key under a different trigger is independent.
	fresh, err = store.CheckAndStore(trigB + ":" + idemKey)
	if err != nil || !fresh {
		t.Fatalf("other trigger claim: fresh=%v err=%v", fresh, err)
	}
}

func newHTTPRequest(t *testing.T, e InboundEvent, key []byte, includeHeaders bool) *http.Request {
	t.Helper()
	SignEvent(&e, key)
	body, err := json.Marshal(e) // Signature is json:"-" and travels in a header
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/invoke", bytes.NewReader(body))
	if includeHeaders {
		req.Header.Set(HeaderTrigger, e.TriggerID)
		req.Header.Set(HeaderFunction, e.FunctionID)
		req.Header.Set(HeaderTopic, e.Topic)
		req.Header.Set(HeaderQueue, e.Queue)
		req.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(e.Signature))
	}
	return req
}

func TestHTTPHandlerAllowsOnlyTrustedEvents(t *testing.T) {
	h := newHarness(t, funcA)

	t.Run("legitimate event reaches handler with verified payload", func(t *testing.T) {
		var got VerifiedEvent
		called := false
		handler := h.source.HTTPHandler(func(w http.ResponseWriter, r *http.Request) {
			called = true
			var ok bool
			got, ok = EventFromContext(r.Context())
			if !ok {
				t.Error("verified event missing from context")
			}
			w.WriteHeader(http.StatusAccepted)
		})

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newHTTPRequest(t, goodEvent(), h.keyA, true))

		if rec.Code != http.StatusAccepted || !called {
			t.Fatalf("legitimate event not delivered, status=%d called=%v", rec.Code, called)
		}
		if got.IdempotencyKey != idemKey || !bytes.Equal(got.Object, goodEvent().Object) {
			t.Fatalf("verified payload mismatch: %+v", got)
		}
	})

	t.Run("direct invocation without headers is forbidden", func(t *testing.T) {
		called := false
		handler := h.source.HTTPHandler(func(http.ResponseWriter, *http.Request) {
			called = true
		})

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newHTTPRequest(t, goodEvent(), h.keyA, false))

		if called {
			t.Fatal("untrusted direct invocation reached the handler")
		}
		if rec.Code != http.StatusForbidden {
			t.Fatalf("want 403 for unsigned direct invocation, got %d", rec.Code)
		}
	})

	t.Run("forged event with bogus signature is forbidden", func(t *testing.T) {
		called := false
		handler := h.source.HTTPHandler(func(http.ResponseWriter, *http.Request) {
			called = true
		})

		req := newHTTPRequest(t, goodEvent(), h.keyA, true)
		req.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString([]byte("forged")))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if called {
			t.Fatal("forged event reached the handler")
		}
		if rec.Code != http.StatusForbidden {
			t.Fatalf("want 403 for forged signature, got %d", rec.Code)
		}
	})

	t.Run("cross-function trigger is forbidden", func(t *testing.T) {
		called := false
		handler := h.source.HTTPHandler(func(http.ResponseWriter, *http.Request) {
			called = true
		})

		e := InboundEvent{
			TriggerID: trigB, FunctionID: funcB, Topic: "topic-b",
			Queue: "queue-b", IdempotencyKey: "idem-b", Object: []byte("{}"),
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newHTTPRequest(t, e, h.keyB, true))

		if called {
			t.Fatal("cross-function event reached the handler")
		}
		if rec.Code != http.StatusForbidden {
			t.Fatalf("want 403 for cross-function event, got %d", rec.Code)
		}
	})

	t.Run("duplicate redelivery reports conflict", func(t *testing.T) {
		fresh := newHarness(t, funcA) // isolate idempotency state from sibling subtests
		calls := 0
		handler := fresh.source.HTTPHandler(func(http.ResponseWriter, *http.Request) {
			calls++
		})

		first := httptest.NewRecorder()
		handler.ServeHTTP(first, newHTTPRequest(t, goodEvent(), fresh.keyA, true))
		second := httptest.NewRecorder()
		handler.ServeHTTP(second, newHTTPRequest(t, goodEvent(), fresh.keyA, true))

		if calls != 1 {
			t.Fatalf("want handler invoked once, got %d", calls)
		}
		if second.Code != http.StatusConflict {
			t.Fatalf("want 409 for duplicate, got %d", second.Code)
		}
	})

	t.Run("forged routing fields in body are overridden by trusted headers", func(t *testing.T) {
		fresh := newHarness(t, funcA)
		called := false
		handler := fresh.source.HTTPHandler(func(w http.ResponseWriter, r *http.Request) {
			called = true
			got, _ := EventFromContext(r.Context())
			if got.Queue != queueA || got.Topic != topicA {
				t.Errorf("forged body fields leaked into verified event: %+v", got)
			}
			w.WriteHeader(http.StatusAccepted)
		})

		// The bus signs the trusted values and places them in headers; the
		// attacker overwrites the queue/topic inside the JSON body.
		trusted := goodEvent()
		SignEvent(&trusted, fresh.keyA)
		body, err := json.Marshal(trusted)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		body = bytes.ReplaceAll(body, []byte(`"queue":"`+queueA+`"`), []byte(`"queue":"attacker-queue"`))
		body = bytes.ReplaceAll(body, []byte(`"topic":"`+topicA+`"`), []byte(`"topic":"attacker-topic"`))

		req := httptest.NewRequest(http.MethodPost, "/invoke", bytes.NewReader(body))
		req.Header.Set(HeaderTrigger, trusted.TriggerID)
		req.Header.Set(HeaderFunction, trusted.FunctionID)
		req.Header.Set(HeaderTopic, trusted.Topic)
		req.Header.Set(HeaderQueue, trusted.Queue)
		req.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(trusted.Signature))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !called || rec.Code != http.StatusAccepted {
			t.Fatalf("trusted header event rejected, called=%v status=%d", called, rec.Code)
		}
	})
}
