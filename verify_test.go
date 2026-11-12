package netsec_test

import (
	"errors"
	"strings"
	"testing"

	"example.org/netsec/q0032"
)

func TestVerifyKnownImplementations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		compare netsec.CompareFunc
		wantErr bool
	}{
		{
			name:    "xor accumulation",
			compare: compareXORAccumulate,
		},
		{
			name:    "length first",
			compare: compareLengthFirst,
		},
		{
			name:    "early return",
			compare: compareEarlyReturn,
			wantErr: true,
		},
		{
			name:    "deliberate position leak",
			compare: compareLeakPosition,
			wantErr: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := netsec.Verify(test.compare)
			if test.wantErr && err == nil {
				t.Fatal("Verify accepted a leaking comparison")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("Verify rejected a constant-time comparison: %v", err)
			}
		})
	}
}

func TestVerifyReportsAccessMismatch(t *testing.T) {
	t.Parallel()

	report := netsec.Analyze(compareEarlyReturn)
	if report.Passed() {
		t.Fatal("Analyze accepted an early-return comparison")
	}

	var found bool
	for _, failure := range report.Failures {
		if failure.Kind != netsec.FailureAccessMismatch {
			continue
		}
		found = true
		if failure.Baseline == "" {
			t.Fatal("access mismatch did not identify its baseline case")
		}
		if !strings.Contains(failure.Detail, "access") {
			t.Fatalf("access mismatch has unhelpful detail: %q", failure.Detail)
		}
		break
	}
	if !found {
		t.Fatalf("no access mismatch reported; failures: %+v", report.Failures)
	}

	err := report.Err()
	var verificationError *netsec.VerificationError
	if !errors.As(err, &verificationError) {
		t.Fatalf("report error has type %T, want *netsec.VerificationError", err)
	}
	if !strings.Contains(err.Error(), "access mismatch") {
		t.Fatalf("error text does not explain the mismatch: %v", err)
	}
}

func TestVerifyReportsValueDependentIndex(t *testing.T) {
	t.Parallel()

	err := netsec.Verify(compareLeakValueIndex)
	if err == nil {
		t.Fatal("Verify accepted a comparison whose read index depends on a byte value")
	}
}

func TestVerifyReportsIncorrectResult(t *testing.T) {
	t.Parallel()

	err := netsec.Verify(func(netsec.ByteView, netsec.ByteView) bool {
		return false
	})
	if err == nil {
		t.Fatal("Verify accepted a comparison that always returns false")
	}

	var verificationError *netsec.VerificationError
	if !errors.As(err, &verificationError) {
		t.Fatalf("report error has type %T, want *netsec.VerificationError", err)
	}

	var found bool
	for _, failure := range verificationError.Report.Failures {
		if failure.Kind == netsec.FailureWrongResult {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no wrong-result failure reported: %+v", verificationError.Report.Failures)
	}
}

func TestTraceRecordsExactOrder(t *testing.T) {
	t.Parallel()

	compare := func(a, b netsec.ByteView) bool {
		_ = a.Len()
		first := a.ByteAt(1)
		second := b.ByteAt(0)
		_ = a.ByteAt(0)
		return first == second
	}

	got, accesses, err := netsec.Trace(compare, []byte{0x10, 0x20}, []byte{0x20, 0x30})
	if err != nil {
		t.Fatalf("Trace returned an error: %v", err)
	}
	if !got {
		t.Fatal("Trace returned the wrong comparison result")
	}

	want := []netsec.Access{
		{Source: netsec.SourceA, Index: 1},
		{Source: netsec.SourceB, Index: 0},
		{Source: netsec.SourceA, Index: 0},
	}
	if len(accesses) != len(want) {
		t.Fatalf("got %d accesses, want %d: %v", len(accesses), len(want), accesses)
	}
	for i := range want {
		if accesses[i] != want[i] {
			t.Fatalf("access %d = %v, want %v", i, accesses[i], want[i])
		}
	}
}

func TestTraceConvertsOutOfRangePanicToError(t *testing.T) {
	t.Parallel()

	compare := func(a, _ netsec.ByteView) bool {
		_ = a.ByteAt(4)
		return true
	}

	_, accesses, err := netsec.Trace(compare, []byte{1}, []byte{1})
	if err == nil {
		t.Fatal("Trace did not report the out-of-range panic")
	}
	if len(accesses) != 1 || accesses[0].Source != netsec.SourceA || accesses[0].Index != 4 {
		t.Fatalf("partial trace = %v, want one attempted read of a[4]", accesses)
	}
}

func compareXORAccumulate(a, b netsec.ByteView) bool {
	aLength, bLength := a.Len(), b.Len()

	length := aLength
	if bLength < length {
		length = bLength
	}

	var difference byte
	for i := 0; i < length; i++ {
		difference |= a.ByteAt(i) ^ b.ByteAt(i)
	}
	return aLength == bLength && difference == 0
}

func compareLengthFirst(a, b netsec.ByteView) bool {
	if a.Len() != b.Len() {
		return false
	}

	var difference byte
	for i := 0; i < a.Len(); i++ {
		difference |= a.ByteAt(i) ^ b.ByteAt(i)
	}
	return difference == 0
}

func compareEarlyReturn(a, b netsec.ByteView) bool {
	if a.Len() != b.Len() {
		return false
	}

	for i := 0; i < a.Len(); i++ {
		if a.ByteAt(i) != b.ByteAt(i) {
			return false
		}
	}
	return true
}

func compareLeakPosition(a, b netsec.ByteView) bool {
	if a.Len() != b.Len() {
		return false
	}

	var difference byte
	for i := 0; i < a.Len(); i++ {
		aByte := a.ByteAt(i)
		bByte := b.ByteAt(i)
		difference |= aByte ^ bByte
		if aByte != bByte {
			_ = a.ByteAt(0)
		}
	}
	return difference == 0
}

func compareLeakValueIndex(a, b netsec.ByteView) bool {
	if a.Len() != b.Len() {
		return false
	}

	var difference byte
	for i := 0; i < a.Len(); i++ {
		difference |= a.ByteAt(i) ^ b.ByteAt(i)
	}
	if difference != 0 {
		index := int(a.ByteAt(0)) % a.Len()
		_ = a.ByteAt(index)
	}
	return difference == 0
}
