package netsec

import "testing"

const (
	tid  = "thread-1"
	base = uintptr(0x1000)
)

func getEvent() Event {
	return Event{ThreadID: tid, Kind: EventGetCritical, ArrayID: "arr-1", Addr: base, Length: 16}
}

func kindsOf(fs []Finding) []FindingKind {
	out := make([]FindingKind, len(fs))
	for i, f := range fs {
		out[i] = f.Kind
	}
	return out
}

func hasKind(fs []Finding, k FindingKind) bool {
	for _, f := range fs {
		if f.Kind == k {
			return true
		}
	}
	return false
}

// 临界区内调用 NewStringUTF：违规。
func TestJNICallInsideCritical(t *testing.T) {
	d := NewDetector()
	d.Handle(getEvent())
	d.Handle(Event{ThreadID: tid, Kind: EventJNICall, Name: "NewStringUTF"})
	d.Handle(Event{ThreadID: tid, Kind: EventReleaseCritical, ArrayID: "arr-1"})

	fs := d.Findings()
	if len(fs) != 1 || fs[0].Kind != FindingJNICallInCritical {
		t.Fatalf("want exactly one JNICallInCritical finding, got %v", kindsOf(fs))
	}
}

// 嵌套 Get...Critical 与 Release 在临界区内是允许的。
func TestNestedGetCriticalAllowed(t *testing.T) {
	d := NewDetector()
	d.Handle(getEvent())
	d.Handle(Event{ThreadID: tid, Kind: EventGetCritical, ArrayID: "arr-2", Addr: 0x2000, Length: 8})
	d.Handle(Event{ThreadID: tid, Kind: EventReleaseCritical, ArrayID: "arr-2"})
	d.Handle(Event{ThreadID: tid, Kind: EventReleaseCritical, ArrayID: "arr-1"})

	if fs := d.Findings(); len(fs) != 0 {
		t.Fatalf("want no findings, got %v", fs)
	}
}

// 临界区内阻塞调用：违规。
func TestBlockingCallInsideCritical(t *testing.T) {
	d := NewDetector()
	d.Handle(getEvent())
	d.Handle(Event{ThreadID: tid, Kind: EventBlockingCall, Name: "read"})
	d.Handle(Event{ThreadID: tid, Kind: EventReleaseCritical, ArrayID: "arr-1"})

	fs := d.Findings()
	if len(fs) != 1 || fs[0].Kind != FindingBlockingInCritical {
		t.Fatalf("want exactly one BlockingInCritical finding, got %v", kindsOf(fs))
	}
}

// 越界一字节写入：违规。
func TestWriteOneByteOutOfBounds(t *testing.T) {
	d := NewDetector()
	d.Handle(getEvent())
	d.Handle(Event{ThreadID: tid, Kind: EventPtrWrite, Addr: base + 16, Size: 1})
	d.Handle(Event{ThreadID: tid, Kind: EventReleaseCritical, ArrayID: "arr-1"})

	fs := d.Findings()
	if len(fs) != 1 || fs[0].Kind != FindingWriteOutOfBounds {
		t.Fatalf("want exactly one WriteOutOfBounds finding, got %v", kindsOf(fs))
	}
}

// 跨界写入（起点在界内、终点越界）同样被检出。
func TestWriteSpanningBoundary(t *testing.T) {
	d := NewDetector()
	d.Handle(getEvent())
	d.Handle(Event{ThreadID: tid, Kind: EventPtrWrite, Addr: base + 14, Size: 4})
	d.Handle(Event{ThreadID: tid, Kind: EventReleaseCritical, ArrayID: "arr-1"})

	if !hasKind(d.Findings(), FindingWriteOutOfBounds) {
		t.Fatalf("want WriteOutOfBounds finding, got %v", kindsOf(d.Findings()))
	}
}

// 释放后读写：违规。
func TestUseAfterRelease(t *testing.T) {
	d := NewDetector()
	d.Handle(getEvent())
	d.Handle(Event{ThreadID: tid, Kind: EventReleaseCritical, ArrayID: "arr-1"})
	d.Handle(Event{ThreadID: tid, Kind: EventPtrWrite, Addr: base, Size: 4})
	d.Handle(Event{ThreadID: tid, Kind: EventPtrRead, Addr: base + 8, Size: 4})

	fs := d.Findings()
	if len(fs) != 2 {
		t.Fatalf("want two findings, got %v", kindsOf(fs))
	}
	for _, f := range fs {
		if f.Kind != FindingUseAfterRelease {
			t.Fatalf("unexpected finding kind %v", f.Kind)
		}
	}
}

// 同一数组重复释放：违规。
func TestDoubleRelease(t *testing.T) {
	d := NewDetector()
	d.Handle(getEvent())
	d.Handle(Event{ThreadID: tid, Kind: EventReleaseCritical, ArrayID: "arr-1"})
	d.Handle(Event{ThreadID: tid, Kind: EventReleaseCritical, ArrayID: "arr-1"})

	fs := d.Findings()
	if len(fs) != 1 || fs[0].Kind != FindingDoubleRelease {
		t.Fatalf("want exactly one DoubleRelease finding, got %v", kindsOf(fs))
	}
}

// 线程结束时未释放：违规。
func TestUnreleasedAtThreadEnd(t *testing.T) {
	d := NewDetector()
	d.Handle(getEvent())
	d.Handle(Event{ThreadID: tid, Kind: EventThreadEnd})

	fs := d.Findings()
	if len(fs) != 1 || fs[0].Kind != FindingUnreleasedAtThreadEnd {
		t.Fatalf("want exactly one UnreleasedAtThreadEnd finding, got %v", kindsOf(fs))
	}
}

// JNI_ABORT 释放：临界区内对拷贝的写入被丢弃，给出提示。
func TestAbortDiscardsWrites(t *testing.T) {
	d := NewDetector()
	d.Handle(getEvent())
	d.Handle(Event{ThreadID: tid, Kind: EventPtrWrite, Addr: base, Size: 4})
	d.Handle(Event{ThreadID: tid, Kind: EventReleaseCritical, ArrayID: "arr-1", Mode: ReleaseAbort})

	fs := d.Findings()
	if len(fs) != 1 || fs[0].Kind != FindingAbortDiscardsWrites {
		t.Fatalf("want exactly one AbortDiscardsWrites finding, got %v", kindsOf(fs))
	}
}

// JNI_ABORT 但临界区内无写入：不提示。
func TestAbortWithoutWritesIsSilent(t *testing.T) {
	d := NewDetector()
	d.Handle(getEvent())
	d.Handle(Event{ThreadID: tid, Kind: EventPtrRead, Addr: base, Size: 4})
	d.Handle(Event{ThreadID: tid, Kind: EventReleaseCritical, ArrayID: "arr-1", Mode: ReleaseAbort})

	if fs := d.Findings(); len(fs) != 0 {
		t.Fatalf("want no findings, got %v", fs)
	}
}

// 合规序列：临界区内边界内读写、正常释放，无任何报告。
func TestCompliantUsage(t *testing.T) {
	d := NewDetector()
	d.Handle(getEvent())
	d.Handle(Event{ThreadID: tid, Kind: EventPtrRead, Addr: base, Size: 16})
	d.Handle(Event{ThreadID: tid, Kind: EventPtrWrite, Addr: base, Size: 16})
	d.Handle(Event{ThreadID: tid, Kind: EventPtrWrite, Addr: base + 15, Size: 1})
	d.Handle(Event{ThreadID: tid, Kind: EventReleaseCritical, ArrayID: "arr-1", Mode: ReleaseCopyBack})
	d.Handle(Event{ThreadID: tid, Kind: EventJNICall, Name: "NewStringUTF"}) // 已出临界区，合法
	d.Handle(Event{ThreadID: tid, Kind: EventThreadEnd})

	if fs := d.Findings(); len(fs) != 0 {
		t.Fatalf("want no findings, got %v", fs)
	}
}

// 临界区外的 JNI 调用与阻塞调用不报告。
func TestCallsOutsideCriticalAreFine(t *testing.T) {
	d := NewDetector()
	d.Handle(Event{ThreadID: tid, Kind: EventJNICall, Name: "NewStringUTF"})
	d.Handle(Event{ThreadID: tid, Kind: EventBlockingCall, Name: "sleep"})
	d.Handle(Event{ThreadID: tid, Kind: EventThreadEnd})

	if fs := d.Findings(); len(fs) != 0 {
		t.Fatalf("want no findings, got %v", fs)
	}
}

// 线程互不干扰：线程 A 持临界区不影响线程 B 的 JNI 调用。
func TestThreadsAreIndependent(t *testing.T) {
	d := NewDetector()
	d.Handle(getEvent())
	d.Handle(Event{ThreadID: "thread-2", Kind: EventJNICall, Name: "NewStringUTF"})
	d.Handle(Event{ThreadID: tid, Kind: EventReleaseCritical, ArrayID: "arr-1"})

	if fs := d.Findings(); len(fs) != 0 {
		t.Fatalf("want no findings, got %v", fs)
	}
}
