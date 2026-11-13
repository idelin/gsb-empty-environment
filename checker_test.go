package netsec

import (
	"errors"
	"sync"
	"testing"
)

const testBufferBytes = 4096

func TestCallerQuotaDoesNotAffectOtherCallers(t *testing.T) {
	checker := New(testBufferBytes)
	const (
		attacker = CallerID(1)
		victim   = CallerID(2)
	)

	dataAtQuota := make([]byte, checker.Limits().PerCallerBytes)
	first, err := checker.Admit(attacker, 1, dataAtQuota, nil)
	if err != nil {
		t.Fatalf("admit caller at quota: %v", err)
	}
	if first.Size() != checker.Limits().PerCallerBytes {
		t.Fatalf("reservation size = %d, want %d", first.Size(), checker.Limits().PerCallerBytes)
	}

	_, err = checker.Admit(attacker, 2, make([]byte, 8), nil)
	if !errors.Is(err, ErrCallerQuota) {
		t.Fatalf("over-quota admission error = %v, want ErrCallerQuota", err)
	}
	if got := checker.PendingBytesFor(attacker); got != checker.Limits().PerCallerBytes {
		t.Fatalf("rejected transaction changed caller usage: got %d", got)
	}

	other, err := checker.Admit(victim, 1, dataAtQuota, nil)
	if err != nil {
		t.Fatalf("other caller admission failed: %v", err)
	}
	if got := checker.PendingBytesFor(victim); got != checker.Limits().PerCallerBytes {
		t.Fatalf("other caller usage = %d, want %d", got, checker.Limits().PerCallerBytes)
	}
	if got := checker.PendingBytes(); got != checker.Limits().OneWayBytes {
		t.Fatalf("total pending = %d, want %d", got, checker.Limits().OneWayBytes)
	}

	if !first.Release() {
		t.Fatal("first reservation could not be released")
	}
	if first.Release() {
		t.Fatal("second release of the same reservation succeeded")
	}
	if got := checker.PendingBytesFor(victim); got != checker.Limits().PerCallerBytes {
		t.Fatalf("releasing attacker affected victim: got %d", got)
	}

	replacement, err := checker.Admit(attacker, 3, dataAtQuota, nil)
	if err != nil {
		t.Fatalf("admission after release failed: %v", err)
	}
	if other.Done() {
		t.Fatal("victim reservation was released unexpectedly")
	}
	if !checker.Complete(attacker, 3) {
		t.Fatal("completion event did not release replacement")
	}
	if checker.Complete(attacker, 3) {
		t.Fatal("duplicate completion event released capacity twice")
	}
	if replacement.Done() != true {
		t.Fatal("completed reservation still reports pending")
	}
	if got := checker.PendingBytes(); got != checker.Limits().PerCallerBytes {
		t.Fatalf("total pending after events = %d, want victim's %d", got, checker.Limits().PerCallerBytes)
	}
}

func TestSharedOneWayQuotaRejectsOnlyExcessCaller(t *testing.T) {
	checker := New(testBufferBytes)
	const (
		callerA = CallerID(10)
		callerB = CallerID(11)
		callerC = CallerID(12)
	)

	dataAtQuarter := make([]byte, checker.Limits().PerCallerBytes)
	a, err := checker.Admit(callerA, 1, dataAtQuarter, nil)
	if err != nil {
		t.Fatalf("admit caller A: %v", err)
	}
	b, err := checker.Admit(callerB, 1, dataAtQuarter, nil)
	if err != nil {
		t.Fatalf("admit caller B: %v", err)
	}
	if got := checker.PendingBytes(); got != checker.Limits().OneWayBytes {
		t.Fatalf("shared quota not full: got %d, want %d", got, checker.Limits().OneWayBytes)
	}

	_, err = checker.Admit(callerC, 1, make([]byte, 8), nil)
	if !errors.Is(err, ErrOneWayQuota) {
		t.Fatalf("admission beyond shared quota error = %v, want ErrOneWayQuota", err)
	}
	if got := checker.PendingBytesFor(callerC); got != 0 {
		t.Fatalf("rejected caller was charged %d bytes", got)
	}
	if a.Done() || b.Done() {
		t.Fatal("shared-quota rejection should not release pending callers")
	}

	if !checker.Complete(callerB, 1) {
		t.Fatal("caller B completion failed")
	}
	if _, err := checker.Admit(callerC, 1, make([]byte, 8), nil); err != nil {
		t.Fatalf("caller C still blocked after capacity was released: %v", err)
	}
	if got, want := checker.PendingBytes(), checker.Limits().PerCallerBytes+8; got != want {
		t.Fatalf("total pending after refill = %d, want %d", got, want)
	}
}

func TestRejectedMalformedTransactionReservesNothing(t *testing.T) {
	checker := New(testBufferBytes)
	tests := []struct {
		name    string
		data    []byte
		offsets []uint64
		wantErr error
	}{
		{
			name:    "overlap",
			data:    make([]byte, 48),
			offsets: []uint64{0, 8},
			wantErr: ErrObjectOverlap,
		},
		{
			name:    "out of bounds by one byte",
			data:    make([]byte, 99),
			offsets: []uint64{76},
			wantErr: ErrObjectBounds,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := checker.Admit(1, 1, test.data, test.offsets)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("admission error = %v, want %v", err, test.wantErr)
			}
			if got := checker.PendingBytes(); got != 0 {
				t.Fatalf("malformed transaction reserved %d bytes", got)
			}
			if got := checker.PendingTransactions(); got != 0 {
				t.Fatalf("malformed transaction left %d pending entries", got)
			}
		})
	}
}

func TestAdmitUsesRoundedOccupancy(t *testing.T) {
	checker := New(64) // one-way = 32, per caller = 16
	first, err := checker.Admit(1, 1, make([]byte, 9), nil)
	if err != nil {
		t.Fatalf("admit first transaction: %v", err)
	}
	if first.Size() != 16 {
		t.Fatalf("rounded size = %d, want 16", first.Size())
	}
	if _, err := checker.Admit(1, 2, make([]byte, 8), nil); !errors.Is(err, ErrCallerQuota) {
		t.Fatalf("admission beyond rounded quota error = %v, want ErrCallerQuota", err)
	}
}

func TestDuplicatePendingTransactionIsRejected(t *testing.T) {
	checker := New(testBufferBytes)
	if _, err := checker.Admit(1, 7, make([]byte, 8), nil); err != nil {
		t.Fatalf("first admission failed: %v", err)
	}
	if _, err := checker.Admit(1, 7, make([]byte, 8), nil); !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("duplicate admission error = %v, want ErrDuplicateTransaction", err)
	}
	if !checker.Complete(1, 7) {
		t.Fatal("completion failed")
	}
	if _, err := checker.Admit(1, 7, make([]byte, 8), nil); err != nil {
		t.Fatalf("transaction ID could not be reused after completion: %v", err)
	}
}

func TestConcurrentAdmissionRespectsCallerQuota(t *testing.T) {
	checker := New(testBufferBytes)
	dataAtQuarter := make([]byte, checker.Limits().PerCallerBytes)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		admitted []*Reservation
		rejected int
	)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id TransactionID) {
			defer wg.Done()
			reservation, err := checker.Admit(42, id, dataAtQuarter, nil)
			if err != nil {
				if !errors.Is(err, ErrCallerQuota) {
					t.Errorf("unexpected admission error: %v", err)
				}
				mu.Lock()
				rejected++
				mu.Unlock()
				return
			}
			mu.Lock()
			admitted = append(admitted, reservation)
			mu.Unlock()
		}(TransactionID(i))
	}
	wg.Wait()

	if len(admitted) != 1 || rejected != 15 {
		t.Fatalf("admitted = %d, rejected = %d; want 1 and 15", len(admitted), rejected)
	}
	if got := checker.PendingBytesFor(42); got != checker.Limits().PerCallerBytes {
		t.Fatalf("caller usage = %d, want %d", got, checker.Limits().PerCallerBytes)
	}
	if !admitted[0].Release() {
		t.Fatal("release failed")
	}
	if got := checker.PendingBytes(); got != 0 {
		t.Fatalf("pending bytes after release = %d, want 0", got)
	}
}
