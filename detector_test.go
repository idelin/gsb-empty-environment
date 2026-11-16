package netsec

import (
	"fmt"
	"sync"
	"testing"
)

func requireFinding(t *testing.T, findings []Finding, kind FindingKind) Finding {
	t.Helper()
	for _, f := range findings {
		if f.Kind == kind {
			return f
		}
	}
	t.Fatalf("expected finding %s, got %v", kind, findings)
	return Finding{}
}

func requireNoFindings(t *testing.T, findings []Finding) {
	t.Helper()
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %v", findings)
	}
}

func TestJNICallInsideCriticalSection(t *testing.T) {
	d := NewDetector()
	requireNoFindings(t, d.Record(GetEvent("worker-1", "arr-1", 0x1000, 8)))

	findings := d.Record(JNICallEvent("worker-1", "NewStringUTF"))
	f := requireFinding(t, findings, FindingJNICallInCritical)
	if f.Thread != "worker-1" || f.Function != "NewStringUTF" {
		t.Fatalf("unexpected finding: %+v", f)
	}
	if f.Severity != SeverityViolation {
		t.Fatalf("expected violation severity, got %s", f.Severity)
	}
}

func TestAllowedCriticalCallsInsideCriticalSection(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "outer", 0x1000, 8))

	requireNoFindings(t, d.Record(JNICallEvent("t", "GetPrimitiveArrayCritical")))
	requireNoFindings(t, d.Record(JNICallEvent("t", "ReleasePrimitiveArrayCritical")))
	requireNoFindings(t, d.Record(JNICallEvent("t", "GetStringCritical")))
	requireNoFindings(t, d.Record(GetEvent("t", "inner", 0x2000, 4)))

	requireNoFindings(t, d.Record(ReleaseEvent("t", "inner", 0x2000, 4, ReleaseModeDefault)))
	requireNoFindings(t, d.Record(ReleaseEvent("t", "outer", 0x1000, 8, ReleaseModeDefault)))
}

func TestWriteOneBytePastEnd(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))

	findings := d.Record(WriteEvent("t", 0x1008, 1))
	f := requireFinding(t, findings, FindingOutOfBoundsWrite)
	if f.Offset != 8 || f.Length != 1 || f.ArrayLength != 8 || f.ArrayBytes != 8 {
		t.Fatalf("unexpected bounds details: %+v", f)
	}
	if f.Array != "arr" || f.Address != 0x1008 {
		t.Fatalf("unexpected array or address: %+v", f)
	}
}

func TestWriteFarPastEndWithOneActiveArray(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))

	f := requireFinding(t, d.Record(WriteEvent("t", 0x1100, 4)), FindingOutOfBoundsWrite)
	if f.Offset != 0x100 {
		t.Fatalf("expected offset 256, got %d", f.Offset)
	}
}

func TestElementSizeBounds(t *testing.T) {
	d := NewDetector()
	d.Record(GetEventSized("t", "ints", 0x2000, 4, 4))

	requireNoFindings(t, d.Record(WriteEvent("t", 0x200f, 1)))
	f := requireFinding(t, d.Record(WriteEvent("t", 0x2010, 1)), FindingOutOfBoundsWrite)
	if f.ArrayBytes != 16 {
		t.Fatalf("expected 16 array bytes, got %d", f.ArrayBytes)
	}
}

func TestUseAfterRelease(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))
	requireNoFindings(t, d.Record(ReleaseEvent("t", "arr", 0x1000, 8, ReleaseModeDefault)))

	findings := d.Record(WriteEvent("t", 0x1002, 4))
	f := requireFinding(t, findings, FindingUseAfterRelease)
	if f.Array != "arr" || f.Offset != 2 || f.Length != 4 {
		t.Fatalf("unexpected use-after-release finding: %+v", f)
	}
}

func TestReadAfterRelease(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))
	d.Record(ReleaseEvent("t", "arr", 0x1000, 8, ReleaseModeDefault))

	f := requireFinding(t, d.Record(ReadEvent("t", 0x1000, 1)), FindingUseAfterRelease)
	if f.Message == "" {
		t.Fatal("expected an explanatory message")
	}
}

func TestDuplicateRelease(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))
	requireNoFindings(t, d.Record(ReleaseEvent("t", "arr", 0x1000, 8, ReleaseModeDefault)))

	f := requireFinding(t, d.Record(ReleaseEvent("t", "arr", 0x1000, 8, ReleaseModeDefault)), FindingDuplicateRelease)
	if f.Array != "arr" || f.ArrayLength != 8 {
		t.Fatalf("unexpected duplicate-release finding: %+v", f)
	}
}

func TestReleaseByAddressWithoutArrayID(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))
	d.Record(ReleaseEvent("t", "", 0x1000, 8, ReleaseModeDefault))

	f := requireFinding(t, d.Record(ReleaseEvent("t", "", 0x1000, 8, ReleaseModeDefault)), FindingDuplicateRelease)
	if f.Array != "arr" {
		t.Fatalf("expected duplicate release to identify the array, got %+v", f)
	}
}

func TestCompliantReadsAndWrites(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))

	requireNoFindings(t, d.Record(WriteEvent("t", 0x1002, 4)))
	requireNoFindings(t, d.Record(ReadEvent("t", 0x1000, 3)))
	requireNoFindings(t, d.Record(WriteEvent("t", 0x1007, 1)))
	requireNoFindings(t, d.Record(ReleaseEvent("t", "arr", 0x1000, 8, ReleaseModeDefault)))
}

func TestReleaseAbortDiscardsWrites(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))
	d.Record(WriteEvent("t", 0x1000, 4))
	d.Record(WriteEvent("t", 0x1004, 2))

	findings := d.Record(ReleaseEvent("t", "arr", 0x1000, 8, JNI_ABORT))
	f := requireFinding(t, findings, FindingWritesDiscarded)
	if f.Writes != 2 || f.WriteBytes != 6 || f.Mode != JNI_ABORT {
		t.Fatalf("unexpected discarded-write details: %+v", f)
	}
	if f.Severity != SeverityWarning {
		t.Fatalf("expected warning severity, got %s", f.Severity)
	}
}

func TestReleaseAbortWithoutWritesIsQuiet(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))
	requireNoFindings(t, d.Record(ReleaseEvent("t", "arr", 0x1000, 8, JNI_ABORT)))
}

func TestBlockingCallInsideCriticalSection(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))

	f := requireFinding(t, d.Record(BlockingCallEvent("t", "sync.Mutex.Lock")), FindingBlockingInCritical)
	if f.Function != "sync.Mutex.Lock" {
		t.Fatalf("unexpected finding: %+v", f)
	}
}

func TestBlockingCallOutsideCriticalSectionIsAllowed(t *testing.T) {
	d := NewDetector()
	requireNoFindings(t, d.Record(BlockingCallEvent("t", "sync.Mutex.Lock")))
}

func TestThreadEndWithUnreleasedArray(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))

	f := requireFinding(t, d.ThreadEnded("t"), FindingUnreleasedAtThreadEnd)
	if f.Array != "arr" || f.ArrayLength != 8 || f.Address != 0x1000 {
		t.Fatalf("unexpected thread-end finding: %+v", f)
	}
}

func TestThreadEndAfterReleaseIsQuiet(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))
	d.Record(ReleaseEvent("t", "arr", 0x1000, 8, ReleaseModeDefault))
	requireNoFindings(t, d.ThreadEnded("t"))
}

func TestNestedCriticalGets(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "outer", 0x1000, 8))
	requireNoFindings(t, d.Record(GetEvent("t", "inner", 0x2000, 4)))
	requireNoFindings(t, d.Record(WriteEvent("t", 0x2001, 2)))

	// Release the inner array first, then the outer one.
	requireNoFindings(t, d.Record(ReleaseEvent("t", "inner", 0x2000, 4, ReleaseModeDefault)))
	requireNoFindings(t, d.Record(WriteEvent("t", 0x1001, 2)))
	requireNoFindings(t, d.Record(ReleaseEvent("t", "outer", 0x1000, 8, ReleaseModeDefault)))
	requireNoFindings(t, d.ThreadEnded("t"))
}

func TestReacquireSamePointerAvoidsFalseUseAfterRelease(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))
	d.Record(ReleaseEvent("t", "arr", 0x1000, 8, ReleaseModeDefault))
	d.Record(GetEvent("t", "arr", 0x1000, 8))

	requireNoFindings(t, d.Record(WriteEvent("t", 0x1000, 1)))
}

func TestUseAfterReleaseWhileAnotherArrayIsActive(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "old", 0x1000, 8))
	d.Record(ReleaseEvent("t", "old", 0x1000, 8, ReleaseModeDefault))
	d.Record(GetEvent("t", "new", 0x2000, 8))

	f := requireFinding(t, d.Record(WriteEvent("t", 0x1004, 1)), FindingUseAfterRelease)
	if f.Array != "old" {
		t.Fatalf("expected use after release of old array, got %+v", f)
	}
}

func TestJNICommitKeepsCriticalSectionActive(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))
	d.Record(WriteEvent("t", 0x1000, 4))
	requireNoFindings(t, d.Record(ReleaseEvent("t", "arr", 0x1000, 8, JNI_COMMIT)))

	// The section remains active, so a later JNI call is still a violation.
	requireFinding(t, d.Record(JNICallEvent("t", "NewStringUTF")), FindingJNICallInCritical)

	// Only writes after the commit are discarded by the final abort.
	d.Record(WriteEvent("t", 0x1004, 2))
	f := requireFinding(t, d.Record(ReleaseEvent("t", "arr", 0x1000, 8, JNI_ABORT)), FindingWritesDiscarded)
	if f.Writes != 1 || f.WriteBytes != 2 {
		t.Fatalf("expected only post-commit writes to be discarded, got %+v", f)
	}
}

func TestInvalidRelease(t *testing.T) {
	d := NewDetector()
	f := requireFinding(
		t,
		d.Record(ReleaseEvent("t", "never-acquired", 0xdead, 8, ReleaseModeDefault)),
		FindingInvalidRelease,
	)
	if f.Severity != SeverityWarning {
		t.Fatalf("expected warning severity, got %s", f.Severity)
	}
}

func TestSubscribeAndUnsubscribe(t *testing.T) {
	d := NewDetector()
	var got []Finding
	unsubscribe := d.Subscribe(func(f Finding) {
		got = append(got, f)
	})

	d.Record(GetEvent("t", "arr", 0x1000, 8))
	d.Record(JNICallEvent("t", "NewStringUTF"))
	if len(got) != 1 || got[0].Kind != FindingJNICallInCritical {
		t.Fatalf("unexpected subscriber findings: %v", got)
	}

	unsubscribe()
	unsubscribe()
	d.Record(BlockingCallEvent("t", "sleep"))
	if len(got) != 1 {
		t.Fatalf("subscriber received findings after unsubscribe: %v", got)
	}
}

func TestFindingsAndReset(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", 0x1000, 8))
	d.Record(JNICallEvent("t", "NewStringUTF"))
	if len(d.Findings()) != 1 {
		t.Fatalf("expected one retained finding, got %v", d.Findings())
	}

	d.Reset()
	requireNoFindings(t, d.Findings())
	requireNoFindings(t, d.Record(JNICallEvent("t", "NewStringUTF")))
}

func TestConcurrentThreadsAreIsolated(t *testing.T) {
	d := NewDetector()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			thread := ThreadID(fmt.Sprintf("t%d", i))
			array := ArrayID("arr")
			d.Record(GetEvent(thread, array, uintptr(0x1000+i*0x100), 8))
			d.Record(WriteEvent(thread, uintptr(0x1000+i*0x100), 8))
			d.Record(ReleaseEvent(thread, array, uintptr(0x1000+i*0x100), 8, ReleaseModeDefault))
		}()
	}
	wg.Wait()
	requireNoFindings(t, d.Findings())
}

func TestAddressOverflowIsHandled(t *testing.T) {
	d := NewDetector()
	d.Record(GetEvent("t", "arr", ^uintptr(0)-3, 8))
	f := requireFinding(t, d.Record(WriteEvent("t", ^uintptr(0), 2)), FindingOutOfBoundsWrite)
	if f.Offset != 3 {
		t.Fatalf("unexpected offset: %d", f.Offset)
	}
}
