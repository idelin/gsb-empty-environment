package netsec

import (
	"errors"
	"testing"
)

func TestShrinkDuringHeaderBlock(t *testing.T) {
	conn := NewConnection(100)

	// Start a header block and send half of it (50 of the intended 100 bytes).
	if err := conn.HeadersFrame(1, 50, false); err != nil {
		t.Fatalf("first half rejected: %v", err)
	}
	if got := conn.HeaderBlockSize(1); got != 50 {
		t.Fatalf("block size = %d, want 50", got)
	}

	// Shrink the limit below the bytes already accepted. The in-flight block
	// keeps the limit snapshotted at its start and must remain accepted.
	conn.SetMaxHeaderListSize(20)
	if !conn.HeaderBlockInProgress(1) {
		t.Fatal("header block should still be in progress")
	}

	// The second half brings the block to exactly its original 100-byte limit.
	if err := conn.ContinuationFrame(1, 50, true); err != nil {
		t.Fatalf("in-flight block rejected after shrink: %v", err)
	}
	if conn.HeaderBlockInProgress(1) {
		t.Fatal("header block should have terminated")
	}
	if conn.StreamFailed(1) {
		t.Fatal("stream 1 must not fail: the shrink does not affect it")
	}

	// A new block is governed by the new limit. One within 20 bytes succeeds.
	if err := conn.HeadersFrame(3, 20, true); err != nil {
		t.Fatalf("block within new limit rejected: %v", err)
	}

	// A single new header block over the current 20-byte limit fails the
	// stream.
	err := conn.HeadersFrame(5, 21, true)
	var se *StreamError
	if !errors.As(err, &se) {
		t.Fatalf("want *StreamError, got %v", err)
	}
	if se.Code != ErrCodeEnhanceYourCalm || !errors.Is(err, ErrHeaderListTooLarge) {
		t.Fatalf("want ENHANCE_YOUR_CALM / ErrHeaderListTooLarge, got code %d err %v", se.Code, err)
	}
	if se.StreamID != 5 || !conn.StreamFailed(5) {
		t.Fatal("stream 5 should be marked failed")
	}

	// A block that starts small but grows past the limit on a CONTINUATION
	// also fails, using the limit current at its start.
	if err := conn.HeadersFrame(7, 10, false); err != nil {
		t.Fatalf("partial block rejected: %v", err)
	}
	err = conn.ContinuationFrame(7, 15, true) // 25 > 20
	if !errors.Is(err, ErrHeaderListTooLarge) {
		t.Fatalf("want ErrHeaderListTooLarge, got %v", err)
	}
	if !conn.StreamFailed(7) {
		t.Fatal("stream 7 should be marked failed")
	}
}

func TestDefaultUnlimited(t *testing.T) {
	conn := NewConnection(0)
	if err := conn.HeadersFrame(1, 1_000_000, false); err != nil {
		t.Fatalf("large block rejected with no limit: %v", err)
	}
	if err := conn.ContinuationFrame(1, 1_000_000, true); err != nil {
		t.Fatalf("large continuation rejected with no limit: %v", err)
	}
}

func TestShrinkThenRaiseAffectsOnlyNewBlocks(t *testing.T) {
	conn := NewConnection(100)

	// In-flight block with a 100-byte snapshot.
	if err := conn.HeadersFrame(1, 60, false); err != nil {
		t.Fatal(err)
	}
	conn.SetMaxHeaderListSize(10) // shrink: must not affect stream 1
	conn.SetMaxHeaderListSize(1000)

	// Completing stream 1 at 90 bytes is fine under its snapshot of 100.
	if err := conn.ContinuationFrame(1, 30, true); err != nil {
		t.Fatalf("in-flight block failed after setting changes: %v", err)
	}

	// The new limit of 1000 applies to the next block.
	if err := conn.HeadersFrame(3, 900, true); err != nil {
		t.Fatalf("block under new limit rejected: %v", err)
	}
}

func TestContinuationWithoutHeaders(t *testing.T) {
	conn := NewConnection(100)
	err := conn.ContinuationFrame(1, 10, true)
	var se *StreamError
	if !errors.As(err, &se) || se.Code != ErrCodeProtocol || !errors.Is(err, ErrProtocol) {
		t.Fatalf("want PROTOCOL_ERROR, got %v", err)
	}
}

func TestHeadersWhileBlockActive(t *testing.T) {
	conn := NewConnection(100)
	if err := conn.HeadersFrame(1, 10, false); err != nil {
		t.Fatal(err)
	}
	err := conn.HeadersFrame(1, 5, true)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("want ErrProtocol, got %v", err)
	}
}

func TestFramesOnFailedStreamRejected(t *testing.T) {
	conn := NewConnection(10)
	err := conn.HeadersFrame(1, 50, true)
	if !errors.Is(err, ErrHeaderListTooLarge) {
		t.Fatalf("want ErrHeaderListTooLarge, got %v", err)
	}
	if err := conn.HeadersFrame(1, 1, true); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("want ErrStreamClosed, got %v", err)
	}
	if err := conn.ContinuationFrame(1, 1, true); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("want ErrStreamClosed, got %v", err)
	}
}

func TestHandleEventGateway(t *testing.T) {
	conn := NewConnection(100)

	if err := conn.HandleEvent(HeadersEvent{StreamID: 1, PayloadLen: 40, EndHeaders: false}); err != nil {
		t.Fatal(err)
	}
	if err := conn.HandleEvent(SettingUpdateEvent{MaxHeaderListSize: 10}); err != nil {
		t.Fatal(err)
	}
	// In-flight block still governed by its 100-byte snapshot.
	if err := conn.HandleEvent(ContinuationEvent{StreamID: 1, PayloadLen: 40, EndHeaders: true}); err != nil {
		t.Fatalf("in-flight block failed via event path: %v", err)
	}

	// New block limited to 10 bytes.
	err := conn.HandleEvent(HeadersEvent{StreamID: 3, PayloadLen: 11, EndHeaders: true})
	if !errors.Is(err, ErrHeaderListTooLarge) {
		t.Fatalf("want ErrHeaderListTooLarge, got %v", err)
	}

	// RST_STREAM clears state so the stream can be observed fresh.
	if err := conn.HandleEvent(StreamResetEvent{StreamID: 3}); err != nil {
		t.Fatal(err)
	}
	if conn.StreamFailed(3) {
		t.Fatal("reset should clear failed state")
	}

	if err := conn.HandleEvent(nil); err == nil {
		t.Fatal("unknown event should return an error")
	}
}

func TestResetStream(t *testing.T) {
	conn := NewConnection(100)
	if err := conn.HeadersFrame(1, 10, false); err != nil {
		t.Fatal(err)
	}
	conn.ResetStream(1)
	if conn.HeaderBlockInProgress(1) {
		t.Fatal("reset should clear in-flight block")
	}
	// A fresh block on the same id starts clean.
	if err := conn.HeadersFrame(1, 10, true); err != nil {
		t.Fatalf("block after reset rejected: %v", err)
	}
}
