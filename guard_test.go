package netsec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestReportConcurrentFiresOnce(t *testing.T) {
	transport := &countingTransport{}
	logger := &recordingLogger{}
	callback := &countingCallback{}
	guard := NewGuard(transport, WithLogger(logger), WithFailureCallback(callback.callback))

	detailed := NewInternalError(string(internalStage), "detail", nil, sensitiveFields()...)

	const goroutines = 64
	var wg sync.WaitGroup
	successes := make(chan PublicError, goroutines)
	rejections := make(chan error, goroutines)
	start := make(chan struct{})

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			<-start
			pub, err := guard.Report(detailed)
			if err != nil {
				rejections <- err
			} else {
				successes <- pub
			}
		}()
	}
	close(start)
	wg.Wait()
	close(successes)
	close(rejections)

	if len(successes) != 1 {
		t.Fatalf("successful reports = %d, want 1", len(successes))
	}
	if len(rejections) != goroutines-1 {
		t.Fatalf("rejections = %d, want %d", len(rejections), goroutines-1)
	}
	for err := range rejections {
		if !errors.Is(err, ErrAlreadyReported) {
			t.Fatalf("unexpected rejection: %v", err)
		}
	}
	if logger.count() != 1 {
		t.Fatalf("log entries = %d, want 1", logger.count())
	}
	if callback.count() != 1 {
		t.Fatalf("callback invocations = %d, want 1", callback.count())
	}

	snapshot := transport.snapshot()
	if snapshot.writeCalls == 0 {
		t.Fatal("expected at least one transport write")
	}
	if snapshot.bytesWritten != wireLen || len(snapshot.payload) != wireLen {
		t.Fatalf("wire payload = %d bytes (%d written), want %d",
			len(snapshot.payload), snapshot.bytesWritten, wireLen)
	}
	assertNoSecretLeak(t, "concurrent wire payload", string(snapshot.payload))

	// The successful public error is exactly what the callback received.
	won := <-successes
	if won != callback.single() {
		t.Fatal("callback public error differs from Report result")
	}
}

func TestPartialWriteCompletesFixedFrame(t *testing.T) {
	transport := &countingTransport{partialAt: 3}
	logger := &recordingLogger{}
	callback := &countingCallback{}
	guard := NewGuard(transport, WithLogger(logger), WithFailureCallback(callback.callback))

	detailed := NewInternalError(string(internalStage), "detail", nil, sensitiveFields()...)
	pub, err := guard.Report(detailed)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	snapshot := transport.snapshot()
	if string(snapshot.payload) != pub.wireFrame() {
		t.Fatalf("reassembled frame = %q, want %q", snapshot.payload, pub.wireFrame())
	}
	if snapshot.bytesWritten != wireLen {
		t.Fatalf("bytes written = %d, want %d", snapshot.bytesWritten, wireLen)
	}
	wantCalls := (wireLen + transport.partialAt - 1) / transport.partialAt
	if snapshot.writeCalls != wantCalls {
		t.Fatalf("write calls = %d, want %d", snapshot.writeCalls, wantCalls)
	}
	if logger.count() != 1 || callback.count() != 1 {
		t.Fatalf("log=%d callback=%d, want both 1", logger.count(), callback.count())
	}
	assertNoSecretLeak(t, "partial-write payload", string(snapshot.payload))

	if _, err := guard.Report(detailed); !errors.Is(err, ErrAlreadyReported) {
		t.Fatalf("second report err = %v, want ErrAlreadyReported", err)
	}
	snapshot = transport.snapshot()
	if snapshot.writeCalls != wantCalls || snapshot.bytesWritten != wireLen {
		t.Fatalf("repeated report mutated transport: %+v", snapshot)
	}
	if logger.count() != 1 || callback.count() != 1 {
		t.Fatalf("repeated report triggered notification: log=%d callback=%d",
			logger.count(), callback.count())
	}
}

func TestWriteErrorStillNotifiesOnce(t *testing.T) {
	writeErr := errors.New("transport reset")
	transport := &countingTransport{failAfter: 5, writeErr: writeErr}
	logger := &recordingLogger{}
	callback := &countingCallback{}
	guard := NewGuard(transport, WithLogger(logger), WithFailureCallback(callback.callback))

	detailed := NewInternalError(string(internalStage), "detail", nil, sensitiveFields()...)
	pub, err := guard.Report(detailed)
	if err != nil {
		t.Fatalf("Report should swallow write error, got %v", err)
	}
	if logger.count() != 1 || callback.count() != 1 {
		t.Fatalf("log=%d callback=%d, want both 1 despite write failure",
			logger.count(), callback.count())
	}
	if callback.single().CorrelationID != pub.CorrelationID {
		t.Fatal("callback received a different public error")
	}

	snapshot := transport.snapshot()
	if snapshot.bytesWritten >= wireLen {
		t.Fatalf("expected truncated write, got %d/%d bytes", snapshot.bytesWritten, wireLen)
	}
	assertNoSecretLeak(t, "truncated payload", string(snapshot.payload))
}

func TestCloseRunsOnceUnderConcurrency(t *testing.T) {
	closeErr := errors.New("close failed")
	transport := &countingTransport{closeErr: closeErr}
	guard := NewGuard(transport)

	const goroutines = 32
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	start := make(chan struct{})

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			<-start
			errs <- guard.Close()
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if !errors.Is(err, closeErr) {
			t.Fatalf("Close returned %v, want propagated close error", err)
		}
	}
	if got := transport.snapshot().closeCalls; got != 1 {
		t.Fatalf("underlying Close calls = %d, want 1", got)
	}

	if err := guard.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("repeated Close err = %v, want %v", err, closeErr)
	}
	if got := transport.snapshot().closeCalls; got != 1 {
		t.Fatalf("Close calls after repeat = %d, want 1", got)
	}
}

func TestRunHandshakeSuccessAndFailure(t *testing.T) {
	transport := &countingTransport{}
	logger := &recordingLogger{}
	callback := &countingCallback{}
	guard := NewGuard(transport, WithLogger(logger), WithFailureCallback(callback.callback))

	if pub, err := guard.Run(func() error { return nil }); err != nil || pub != (PublicError{}) {
		t.Fatalf("successful handshake = (%+v, %v), want zero value, nil", pub, err)
	}
	if logger.count() != 0 || callback.count() != 0 {
		t.Fatalf("success path notified: log=%d callback=%d", logger.count(), callback.count())
	}

	detailed := NewInternalError(string(internalStage), "detail", nil, sensitiveFields()...)
	pub, err := guard.Run(func() error { return detailed })
	if err != nil {
		t.Fatalf("Run failure: %v", err)
	}
	if pub.Category != PublicAuthFailed {
		t.Fatalf("Run category = %s, want %s", pub.Category, PublicAuthFailed)
	}
	if logger.count() != 1 || callback.count() != 1 {
		t.Fatalf("failure path log=%d callback=%d, want both 1", logger.count(), callback.count())
	}

	// A second failure on the same guard is suppressed.
	if _, err := guard.Run(func() error { return detailed }); !errors.Is(err, ErrAlreadyReported) {
		t.Fatalf("second Run err = %v, want ErrAlreadyReported", err)
	}
	if logger.count() != 1 || callback.count() != 1 {
		t.Fatalf("suppressed Run notified again: log=%d callback=%d", logger.count(), callback.count())
	}
}

func TestReportRejectsNilAndAdHocErrors(t *testing.T) {
	guard := NewGuard(&countingTransport{})
	if _, err := guard.Report(nil); err == nil {
		t.Fatal("Report(nil) must fail")
	}

	pub, err := guard.Report(fmt.Errorf("ad hoc: %s", secretPSK))
	if err != nil {
		t.Fatalf("Report ad hoc: %v", err)
	}
	if pub.Category != PublicInternal {
		t.Fatalf("ad hoc category = %s, want %s", pub.Category, PublicInternal)
	}
	// The ad hoc message, including the embedded secret, never reaches the
	// public rendering or the wire.
	assertNoSecretLeak(t, "ad hoc report",
		pub.Error(),
		fmt.Sprintf("%+v", pub),
		string(guard.transport.(*countingTransport).snapshot().payload),
	)
}
