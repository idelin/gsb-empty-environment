package netsec

import (
	"encoding/binary"
	"errors"
	"sync"
	"testing"
)

func frame(streamID uint32, value string) []byte {
	payload := make([]byte, 4+len(value))
	binary.BigEndian.PutUint32(payload[:4], streamID)
	copy(payload[4:], value)
	return payload
}

func TestInvalidStreamID(t *testing.T) {
	state := NewState()

	invalidIDs := []uint32{0, 2, 4, 100, 1 << 31}
	for _, streamID := range invalidIDs {
		err := state.Update(streamID, "u=1")
		if !errors.Is(err, ErrConnectionProtocol) {
			t.Fatalf("Update(%d): want ErrConnectionProtocol, got %v", streamID, err)
		}
	}

	// A bad stream identifier must never create state.
	if got := state.PendingCount(); got != 0 {
		t.Fatalf("PendingCount: want 0, got %d", got)
	}

	// Even identifier through the full frame path.
	if err := state.UpdateFrame(frame(2, "u=1")); !errors.Is(err, ErrConnectionProtocol) {
		t.Fatalf("UpdateFrame even id: want ErrConnectionProtocol, got %v", err)
	}

	// Truncated payloads are also connection errors.
	if err := state.UpdateFrame([]byte{0x00, 0x01}); !errors.Is(err, ErrConnectionProtocol) {
		t.Fatalf("UpdateFrame short payload: want ErrConnectionProtocol, got %v", err)
	}
}

func TestFieldValueValidation(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr error
	}{
		{"urgency too high", "u=8", ErrFieldValue},
		{"urgency negative", "u=-1", ErrFieldValue},
		{"urgency decimal", "u=2.0", ErrFieldValue},
		{"urgency boolean", "u=?1", ErrFieldValue},
		{"incremental integer", "i=1", ErrFieldValue},
		{"incremental string", `i="true"`, ErrFieldValue},
		{"malformed dictionary", "u=1,", ErrFieldValue},
		{"unparseable item", "u=", ErrFieldValue},
		{"bad string", `u=1, i="x`, ErrFieldValue},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewState()
			err := state.Update(1, tc.value)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Update(%q): want %v, got %v", tc.value, tc.wantErr, err)
			}
			if state.PendingCount() != 0 {
				t.Fatalf("rejected field value created state: %d entries", state.PendingCount())
			}
		})
	}
}

func TestDuplicateKeyLastWins(t *testing.T) {
	state := NewState()

	if err := state.Update(1, "u=0, u=5"); err != nil {
		t.Fatalf("duplicate u: %v", err)
	}
	priority, ok := state.Priority(1)
	if !ok || priority.Urgency != 5 || priority.Incremental != false {
		t.Fatalf("duplicate u: got %+v (ok=%v), want urgency 5", priority, ok)
	}

	if err := state.Update(3, "i=?0, i=?1"); err != nil {
		t.Fatalf("duplicate i: %v", err)
	}
	priority, _ = state.Priority(3)
	if !priority.Incremental {
		t.Fatalf("duplicate i: got incremental=false, want true")
	}

	// A bad last occurrence invalidates the whole field value; the earlier
	// good occurrence must not survive.
	if err := state.Update(5, "u=1, u=8"); !errors.Is(err, ErrFieldValue) {
		t.Fatalf("last u out of range: want ErrFieldValue, got %v", err)
	}
	if _, ok := state.Priority(5); ok {
		t.Fatalf("rejected field value must not create an entry")
	}

	// A type error in an earlier duplicate does not matter when the last
	// value is valid.
	if err := state.Update(7, "u=\"x\", u=2"); err != nil {
		t.Fatalf("bad duplicate then good: %v", err)
	}
	priority, _ = state.Priority(7)
	if priority.Urgency != 2 {
		t.Fatalf("last-wins across types: got urgency %d, want 2", priority.Urgency)
	}
}

func TestDefaultsAndUnknownKeys(t *testing.T) {
	state := NewState()

	// Empty dictionary yields the RFC 9218 defaults.
	if err := state.Update(1, "   "); err != nil {
		t.Fatalf("empty value: %v", err)
	}
	priority, ok := state.Priority(1)
	if !ok || priority != DefaultPriority() {
		t.Fatalf("empty value: got %+v, want default %+v", priority, DefaultPriority())
	}

	// Unknown keys, bare flags and parameters are ignored regardless of type.
	if err := state.Update(3, `unknown=(1 2), flag, u=2; rel=1, i=?1; x=:aa==:`); err != nil {
		t.Fatalf("unknown keys: %v", err)
	}
	priority, _ = state.Priority(3)
	want := Priority{Urgency: 2, Incremental: true}
	if priority != want {
		t.Fatalf("unknown keys: got %+v, want %+v", priority, want)
	}

	// An unknown key with a malformed value still makes the whole dictionary
	// unparseable: unknown means unrecognized, not unvalidated syntax.
	if err := state.Update(5, `unknown=(1`); !errors.Is(err, ErrFieldValue) {
		t.Fatalf("malformed unknown value: want ErrFieldValue, got %v", err)
	}
}

func TestPendingEviction(t *testing.T) {
	state := NewState()

	// Fill exactly to the cap with odd identifiers 1, 3, ..., 127.
	for id := uint32(1); id <= 2*maxPending-1; id += 2 {
		if err := state.Update(id, "u=1"); err != nil {
			t.Fatalf("fill %d: %v", id, err)
		}
	}
	if state.PendingCount() != maxPending {
		t.Fatalf("PendingCount: want %d, got %d", maxPending, state.PendingCount())
	}

	// The next entry evicts the oldest (stream 1).
	if err := state.Update(129, "u=2"); err != nil {
		t.Fatalf("evicting update: %v", err)
	}
	if state.PendingCount() != maxPending {
		t.Fatalf("PendingCount after eviction: want %d, got %d", maxPending, state.PendingCount())
	}
	if _, ok := state.Priority(1); ok {
		t.Fatalf("oldest pending entry for stream 1 should have been evicted")
	}
	if priority, ok := state.Priority(129); !ok || priority.Urgency != 2 {
		t.Fatalf("new pending entry missing: %+v ok=%v", priority, ok)
	}

	// Refreshing an existing entry neither changes its age nor grows the map.
	if err := state.Update(3, "u=6"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if state.PendingCount() != maxPending {
		t.Fatalf("refresh changed size: got %d", state.PendingCount())
	}
	if err := state.Update(131, "u=0"); err != nil {
		t.Fatalf("second evicting update: %v", err)
	}
	// Stream 3 kept its position at the front, so it (not stream 5) is
	// evicted next. Had the refresh moved it to the back, stream 5 would
	// have been evicted instead.
	if _, ok := state.Priority(3); ok {
		t.Fatalf("refreshed entry kept its age and should have been evicted first")
	}
	if _, ok := state.Priority(5); !ok {
		t.Fatalf("stream 5 should survive as the new oldest pending entry")
	}
}

func TestStreamLifecycleAndClosedUpdates(t *testing.T) {
	state := NewState()

	// Priority arrives before the stream is opened.
	if err := state.Update(1, "u=0, i=?1"); err != nil {
		t.Fatalf("pending update: %v", err)
	}
	state.StreamOpened(1)
	if state.PendingCount() != 0 {
		t.Fatalf("opening a stream must adopt and remove its pending entry")
	}
	priority, ok := state.Priority(1)
	want := Priority{Urgency: 0, Incremental: true}
	if !ok || priority != want {
		t.Fatalf("adopted priority: got %+v ok=%v, want %+v", priority, ok, want)
	}

	// Updates after opening keep applying.
	if err := state.Update(1, "u=7"); err != nil {
		t.Fatalf("open update: %v", err)
	}
	if priority, _ := state.Priority(1); priority.Urgency != 7 || priority.Incremental {
		t.Fatalf("open update replaces the whole field value: got %+v", priority)
	}

	// A stream opened without prior state gets defaults.
	state.StreamOpened(3)
	if priority, ok := state.Priority(3); !ok || priority != DefaultPriority() {
		t.Fatalf("fresh stream: got %+v ok=%v, want defaults", priority, ok)
	}

	// Close stream 1: its entry is dropped and later updates are ignored.
	state.StreamClosed(1)
	if _, ok := state.Priority(1); ok {
		t.Fatalf("closing a stream must drop its entry")
	}
	if err := state.Update(1, "u=0"); err != nil {
		t.Fatalf("updates for closed streams are ignored, not errors: %v", err)
	}
	if _, ok := state.Priority(1); ok {
		t.Fatalf("a closed stream must never re-enter the state maps")
	}
	if state.PendingCount() != 0 {
		t.Fatalf("ignored closed-stream update must not become pending")
	}

	// A malformed update for a closed stream is still ignored entirely.
	if err := state.Update(1, "u=8"); err != nil {
		t.Fatalf("closed stream malformed update should be ignored: %v", err)
	}

	// The watermark compaction must not forget closed low-numbered streams.
	state.StreamClosed(3)
	state.StreamOpened(5)
	state.StreamClosed(5)
	for _, id := range []uint32{1, 3, 5} {
		if err := state.Update(id, "u=1"); err != nil {
			t.Fatalf("closed %d update: %v", id, err)
		}
		if _, ok := state.Priority(id); ok {
			t.Fatalf("closed stream %d re-entered state after watermark compaction", id)
		}
	}

	// An unrelated odd stream remains fully functional.
	if err := state.Update(7, "u=4"); err != nil {
		t.Fatalf("live stream update: %v", err)
	}
	if priority, ok := state.Priority(7); !ok || priority.Urgency != 4 {
		t.Fatalf("live stream state: %+v ok=%v", priority, ok)
	}
}

func TestUpdateFrame(t *testing.T) {
	state := NewState()
	payload := frame(1, "u=2, i=?1")
	if err := state.UpdateFrame(payload); err != nil {
		t.Fatalf("UpdateFrame: %v", err)
	}
	priority, ok := state.Priority(1)
	if !ok || priority.Urgency != 2 || !priority.Incremental {
		t.Fatalf("UpdateFrame state: got %+v ok=%v", priority, ok)
	}

	// A payload without a field value is a valid empty dictionary.
	if err := state.UpdateFrame(frame(3, "")); err != nil {
		t.Fatalf("empty field frame: %v", err)
	}
	if priority, ok := state.Priority(3); !ok || priority != DefaultPriority() {
		t.Fatalf("empty field frame: got %+v ok=%v", priority, ok)
	}
}

func TestEffectivePriority(t *testing.T) {
	state := NewState()
	if got := state.EffectivePriority(99); got != DefaultPriority() {
		t.Fatalf("unknown stream: got %+v, want defaults", got)
	}
	if err := state.Update(99, "u=6"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := state.EffectivePriority(99); got.Urgency != 6 || got.Incremental {
		t.Fatalf("known stream: got %+v", got)
	}
}

func TestBoundaryValues(t *testing.T) {
	state := NewState()

	// u=7 is the maximum valid urgency; parameters and OWS are tolerated.
	if err := state.Update(1, "  u=7;q=?0 , i =?1 "); err != nil {
		t.Fatalf("boundary u=7: %v", err)
	}
	if priority, ok := state.Priority(1); !ok || priority.Urgency != 7 || !priority.Incremental {
		t.Fatalf("boundary u=7: got %+v ok=%v", priority, ok)
	}

	// Bare "u" means u=?1, a boolean where an integer is required.
	if err := state.Update(3, "u"); !errors.Is(err, ErrFieldValue) {
		t.Fatalf("bare u flag: want ErrFieldValue, got %v", err)
	}

	// Reserved R bit set (identifier >= 2^31) is a connection error.
	if err := state.Update(1<<31+1, "u=1"); !errors.Is(err, ErrConnectionProtocol) {
		t.Fatalf("R bit set: want ErrConnectionProtocol, got %v", err)
	}

	// Maximum legal client stream identifier is accepted.
	if err := state.Update(1<<31-1, "u=0"); err != nil {
		t.Fatalf("max odd stream id: %v", err)
	}

	// Whitespace-only and empty payloads both decode to the defaults.
	if err := state.UpdateFrame(frame(5, "")); err != nil {
		t.Fatalf("empty frame payload field: %v", err)
	}
	if priority, ok := state.Priority(5); !ok || priority != DefaultPriority() {
		t.Fatalf("empty field: got %+v ok=%v", priority, ok)
	}
}

func TestConcurrentAccess(t *testing.T) {
	state := NewState()
	const goroutines = 16
	const iterations = 200

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(base uint32) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				streamID := 2*uint32(i%50) + 1
				_ = state.Update(streamID, "u=2, i=?1")
				_ = state.UpdateFrame(frame(streamID, "u=1"))
				_ = state.EffectivePriority(streamID)
				if i%7 == 0 {
					state.StreamOpened(streamID)
				}
				if i%11 == 0 {
					state.StreamClosed(streamID)
				}
			}
		}(uint32(g))
	}
	wg.Wait()

	if state.PendingCount() > maxPending {
		t.Fatalf("pending cap violated under concurrency: %d", state.PendingCount())
	}
}
