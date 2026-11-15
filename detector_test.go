package netsec

import (
	"strings"
	"testing"
	"time"
)

func requireKind(t *testing.T, findings []Finding, kind FindingKind) Finding {
	t.Helper()
	for _, finding := range findings {
		if finding.Kind == kind {
			return finding
		}
	}
	t.Fatalf("finding %q not found in %+v", kind, findings)
	return Finding{}
}

func requireNoFindings(t *testing.T, findings []Finding) {
	t.Helper()
	if len(findings) != 0 {
		t.Fatalf("unexpected findings: %+v", findings)
	}
}

func TestJNICallInCriticalNewStringUTF(t *testing.T) {
	detector := NewDetector()
	requireNoFindings(t, detector.Get("java-main", "array:42", 0x1000, 16, 1))

	findings := detector.JNICall("java-main", "NewStringUTF")
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	finding := findings[0]
	if finding.Kind != FindingJNICallInCritical {
		t.Fatalf("kind = %q, want %q", finding.Kind, FindingJNICallInCritical)
	}
	if finding.Severity != SeverityViolation {
		t.Fatalf("severity = %q, want %q", finding.Severity, SeverityViolation)
	}
	if finding.Thread != "java-main" || finding.Array != "array:42" {
		t.Fatalf("unexpected event context: %+v", finding)
	}
	if finding.ArrayLength != 16 || finding.ElementSize != 1 || finding.Pointer != 0x1000 {
		t.Fatalf("unexpected array context: %+v", finding)
	}
	if finding.Function != "NewStringUTF" || !strings.Contains(finding.Message, "NewStringUTF") {
		t.Fatalf("unexpected function context: %+v", finding)
	}

	requireNoFindings(t, detector.Release("java-main", "array:42", ReleaseCopyBack))
	if got := len(detector.Findings()); got != 1 {
		t.Fatalf("accumulated findings = %d, want 1", got)
	}
}

func TestReleaseAndNestedCriticalGetAreAllowed(t *testing.T) {
	detector := NewDetector()
	requireNoFindings(t, detector.Get("worker", "array:a", 0x2000, 4, 4))
	requireNoFindings(t, detector.Get("worker", "array:b", 0x3000, 8, 1))

	requireNoFindings(t, detector.JNICall("worker", "GetPrimitiveArrayCritical"))
	requireNoFindings(t, detector.JNICall("worker", "ReleasePrimitiveArrayCritical"))

	if got := len(detector.Active("worker")); got != 2 {
		t.Fatalf("active regions = %d, want 2", got)
	}
	requireNoFindings(t, detector.Release("worker", "array:a", ReleaseCopyBack))
	requireNoFindings(t, detector.Release("worker", "array:b", ReleaseCopyBack))
	if got := len(detector.Active("worker")); got != 0 {
		t.Fatalf("active regions after release = %d, want 0", got)
	}
	requireNoFindings(t, detector.Findings())
}

func TestOutOfBoundsWriteOneByte(t *testing.T) {
	detector := NewDetector()
	requireNoFindings(t, detector.Get("worker", "ints", 0x4000, 4, 4))

	requireNoFindings(t, detector.Write("worker", 0x400c, 4))

	findings := detector.Write("worker", 0x4010, 1)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	finding := findings[0]
	if finding.Kind != FindingOutOfBoundsWrite {
		t.Fatalf("kind = %q, want %q", finding.Kind, FindingOutOfBoundsWrite)
	}
	if finding.Address != 0x4010 || finding.Length != 1 {
		t.Fatalf("unexpected access: %+v", finding)
	}
	if finding.Pointer != 0x4000 || finding.Offset != 16 {
		t.Fatalf("unexpected array context: %+v", finding)
	}
	if finding.ArrayLength != 4 || finding.ElementSize != 4 {
		t.Fatalf("unexpected array size: %+v", finding)
	}

	requireNoFindings(t, detector.Release("worker", "ints", ReleaseCopyBack))
}

func TestUseAfterRelease(t *testing.T) {
	detector := NewDetector()
	requireNoFindings(t, detector.Get("worker", "bytes", 0x5000, 8, 1))
	requireNoFindings(t, detector.Release("worker", "bytes", ReleaseCopyBack))

	findings := detector.Write("worker", 0x5003, 2)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	finding := findings[0]
	if finding.Kind != FindingUseAfterRelease {
		t.Fatalf("kind = %q, want %q", finding.Kind, FindingUseAfterRelease)
	}
	if finding.Pointer != 0x5000 || finding.Offset != 3 || finding.Address != 0x5003 {
		t.Fatalf("unexpected pointer context: %+v", finding)
	}
	if finding.ReleaseSeq == 0 || finding.Array != "bytes" {
		t.Fatalf("missing release context: %+v", finding)
	}

	readFindings := detector.Read("worker", 0x5000, 1)
	requireKind(t, readFindings, FindingUseAfterRelease)
}

func TestUseAfterReleaseWhileAnotherRegionIsActive(t *testing.T) {
	detector := NewDetector()
	requireNoFindings(t, detector.Get("worker", "old", 0x5100, 8, 1))
	requireNoFindings(t, detector.Release("worker", "old", ReleaseCopyBack))
	requireNoFindings(t, detector.Get("worker", "new", 0x5200, 8, 1))

	findings := detector.Write("worker", 0x5102, 1)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Kind != FindingUseAfterRelease || findings[0].Array != "old" {
		t.Fatalf("stale pointer was not classified as use after release: %+v", findings)
	}
	requireNoFindings(t, detector.Write("worker", 0x5202, 1))
}

func TestDuplicateRelease(t *testing.T) {
	detector := NewDetector()
	requireNoFindings(t, detector.Get("worker", "bytes", 0x6000, 32, 1))
	requireNoFindings(t, detector.Release("worker", "bytes", ReleaseCopyBack))

	findings := detector.Release("worker", "bytes", ReleaseCopyBack)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	finding := findings[0]
	if finding.Kind != FindingDuplicateRelease {
		t.Fatalf("kind = %q, want %q", finding.Kind, FindingDuplicateRelease)
	}
	if finding.Thread != "worker" || finding.Array != "bytes" || finding.Pointer != 0x6000 {
		t.Fatalf("unexpected release context: %+v", finding)
	}
	if finding.ArrayLength != 32 || finding.GetSeq == 0 || finding.ReleaseSeq == 0 {
		t.Fatalf("unexpected release metadata: %+v", finding)
	}

	requireNoFindings(t, detector.Get("worker", "bytes", 0x7000, 8, 1))
	requireNoFindings(t, detector.Release("worker", "bytes", ReleaseCopyBack))
}

func TestCompliantWrites(t *testing.T) {
	detector := NewDetector()
	requireNoFindings(t, detector.Get("worker", "ints", 0x8000, 10, 4))

	requireNoFindings(t, detector.Write("worker", 0x8000, 40))
	requireNoFindings(t, detector.Write("worker", 0x8004, 12))
	requireNoFindings(t, detector.Read("worker", 0x8010, 8))
	requireNoFindings(t, detector.Release("worker", "ints", ReleaseCopyBack))

	requireNoFindings(t, detector.Findings())
	if got := len(detector.Active("worker")); got != 0 {
		t.Fatalf("active regions = %d, want 0", got)
	}
}

func TestAbortDiscardsWrites(t *testing.T) {
	detector := NewDetector()
	requireNoFindings(t, detector.Get("worker", "bytes", 0x9000, 16, 1))
	requireNoFindings(t, detector.Read("worker", 0x9000, 4))
	requireNoFindings(t, detector.Write("worker", 0x9002, 3))

	findings := detector.Release("worker", "bytes", ReleaseAbort)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	finding := findings[0]
	if finding.Kind != FindingAbortedWritesDiscarded {
		t.Fatalf("kind = %q, want %q", finding.Kind, FindingAbortedWritesDiscarded)
	}
	if finding.Severity != SeverityWarning {
		t.Fatalf("severity = %q, want %q", finding.Severity, SeverityWarning)
	}
	if finding.Mode != ReleaseAbort || finding.WriteCount != 1 || finding.BytesWritten != 3 {
		t.Fatalf("unexpected discard metadata: %+v", finding)
	}
	if !strings.Contains(finding.Message, "discarded") {
		t.Fatalf("message does not mention discarded writes: %q", finding.Message)
	}
}

func TestBlockingCallInCritical(t *testing.T) {
	detector := NewDetector()
	requireNoFindings(t, detector.Get("worker", "bytes", 0xa000, 8, 1))

	findings := detector.BlockingCallDuration("worker", "monitor-enter", 25*time.Millisecond)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	finding := findings[0]
	if finding.Kind != FindingBlockingCallInCritical || finding.Array != "bytes" {
		t.Fatalf("unexpected finding: %+v", finding)
	}
	requireNoFindings(t, detector.Release("worker", "bytes", ReleaseCopyBack))
	requireNoFindings(t, detector.BlockingCall("worker", "monitor-enter"))
}

func TestThreadExitWithUnreleasedRegions(t *testing.T) {
	detector := NewDetector()
	requireNoFindings(t, detector.Get("worker", "a", 0xb000, 4, 1))
	requireNoFindings(t, detector.Get("worker", "b", 0xc000, 2, 4))

	findings := detector.ThreadExit("worker")
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}
	for _, finding := range findings {
		if finding.Kind != FindingUnreleasedAtThreadExit {
			t.Fatalf("unexpected kind: %+v", finding)
		}
	}
	if findings[0].Array != "a" || findings[1].Array != "b" {
		t.Fatalf("unexpected arrays: %+v", findings)
	}
	if got := len(detector.Active("worker")); got != 0 {
		t.Fatalf("active regions after exit = %d, want 0", got)
	}
	requireNoFindings(t, detector.ThreadExit("worker"))
}

func TestSubscriptionsAndEventMetadata(t *testing.T) {
	detector := NewDetector()
	var events []Event
	var findings []Finding
	cancelEvents := detector.Subscribe(func(event Event) {
		events = append(events, event)
	})
	cancelFindings := detector.SubscribeFindings(func(finding Finding) {
		findings = append(findings, finding)
	})
	defer cancelEvents()
	defer cancelFindings()

	detector.Get("worker", "bytes", 0xd000, 8, 2)
	detector.JNICall("worker", "NewStringUTF")
	detector.Write("worker", 0xd002, 2)
	detector.Release("worker", "bytes", ReleaseCopyBack)

	if len(events) != 4 {
		t.Fatalf("events = %d, want 4: %+v", len(events), events)
	}
	if events[0].Kind != EventGetCritical || events[0].Thread != "worker" ||
		events[0].Array != "bytes" || events[0].ArrayLength != 8 ||
		events[0].Address != 0xd000 || events[0].CriticalDepth != 1 {
		t.Fatalf("unexpected Get event: %+v", events[0])
	}
	if events[3].Kind != EventReleaseCritical || events[3].ArrayLength != 8 ||
		events[3].ElementSize != 2 || events[3].Address != 0xd000 ||
		events[3].CriticalDepth != 0 {
		t.Fatalf("unexpected Release event: %+v", events[3])
	}
	if len(findings) != 1 || findings[0].Kind != FindingJNICallInCritical {
		t.Fatalf("unexpected finding subscription: %+v", findings)
	}

	cancelEvents()
	cancelFindings()
	before := len(events)
	detector.Get("worker", "other", 0xe000, 1, 1)
	if len(events) != before {
		t.Fatalf("event delivered after unsubscribe: %+v", events)
	}
}

func TestReleaseCommitKeepsRegionPinned(t *testing.T) {
	detector := NewDetector()
	requireNoFindings(t, detector.Get("worker", "bytes", 0xf000, 4, 1))
	requireNoFindings(t, detector.Write("worker", 0xf001, 1))
	requireNoFindings(t, detector.Release("worker", "bytes", ReleaseCommit))

	if got := len(detector.Active("worker")); got != 1 {
		t.Fatalf("active regions after JNI_COMMIT = %d, want 1", got)
	}
	requireNoFindings(t, detector.Release("worker", "bytes", ReleaseAbort))
}

func TestAnalyze(t *testing.T) {
	findings := Analyze([]Event{
		{Kind: EventGetCritical, Thread: "worker", Array: "bytes", Address: 0x10000, ArrayLength: 2, ElementSize: 1},
		{Kind: EventPointerWrite, Thread: "worker", Address: 0x10002, Length: 1},
		{Kind: EventReleaseCritical, Thread: "worker", Array: "bytes", Mode: ReleaseCopyBack},
	})
	requireKind(t, findings, FindingOutOfBoundsWrite)
}
