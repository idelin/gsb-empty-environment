package netsec

import "fmt"

// ReleaseMode 对应 JNI ReleasePrimitiveArrayCritical 的 mode 参数。
type ReleaseMode int

const (
	// ReleaseCopyBack 对应 mode 0：拷贝回 Java 堆并释放原生缓冲。
	ReleaseCopyBack ReleaseMode = iota
	// ReleaseCommit 对应 JNI_COMMIT：拷贝回但不释放原生缓冲。
	ReleaseCommit
	// ReleaseAbort 对应 JNI_ABORT：释放缓冲但不拷贝回，临界区内对拷贝的写入被丢弃。
	ReleaseAbort
)

// EventKind 描述跟踪订阅上报的事件类型。
type EventKind int

const (
	EventGetCritical EventKind = iota
	EventReleaseCritical
	EventJNICall
	EventBlockingCall
	EventPtrRead
	EventPtrWrite
	EventThreadEnd
)

// Event 是 JNI 调用跟踪流中的一条事件。
type Event struct {
	ThreadID string
	Kind     EventKind

	// Get/Release 事件：数组标识；Get 还需给出返回指针地址与数组长度（字节）。
	ArrayID string
	Addr    uintptr
	Length  int

	// Release 事件的释放模式。
	Mode ReleaseMode

	// JNI 调用或阻塞调用的函数名，如 "NewStringUTF"、"read"。
	Name string

	// 指针读写事件：访问长度（字节），Addr 为访问起始地址。
	Size int
}

// FindingKind 是检测报告的规则类别。
type FindingKind int

const (
	// FindingJNICallInCritical 临界区内调用了除 Release...Critical 与
	// 嵌套 Get...Critical 之外的 JNI 函数。
	FindingJNICallInCritical FindingKind = iota
	// FindingBlockingInCritical 临界区内发生阻塞调用，可能挂起垃圾回收。
	FindingBlockingInCritical
	// FindingWriteOutOfBounds 写入超出 Get 时声明的数组长度。
	FindingWriteOutOfBounds
	// FindingUseAfterRelease 指针释放后仍被读写。
	FindingUseAfterRelease
	// FindingDoubleRelease 同一数组被重复释放。
	FindingDoubleRelease
	// FindingUnreleasedAtThreadEnd 线程结束时仍持有未释放的临界区指针。
	FindingUnreleasedAtThreadEnd
	// FindingAbortDiscardsWrites Release 模式为 JNI_ABORT，临界区内对拷贝的写入被丢弃。
	FindingAbortDiscardsWrites
)

// Finding 是一条检测报告。
type Finding struct {
	Kind     FindingKind
	ThreadID string
	ArrayID  string
	Detail   string
}

func (f Finding) String() string {
	return fmt.Sprintf("thread=%s array=%s kind=%d: %s", f.ThreadID, f.ArrayID, f.Kind, f.Detail)
}

type arrayState struct {
	id       string
	base     uintptr
	length   int
	released bool
	writes   int // 临界区内发生的写入次数
}

type threadState struct {
	active map[string]*arrayState // 按数组标识索引的未释放临界区
}

// Detector 订阅 JNI 调用跟踪事件流，检测临界区违规。
type Detector struct {
	threads  map[string]*threadState
	byAddr   map[uintptr]*arrayState // 按基地址索引全部数组（含已释放）
	findings []Finding
}

// NewDetector 返回一个空的检测器。
func NewDetector() *Detector {
	return &Detector{
		threads: make(map[string]*threadState),
		byAddr:  make(map[uintptr]*arrayState),
	}
}

// Handle 处理一条跟踪事件。
func (d *Detector) Handle(ev Event) {
	switch ev.Kind {
	case EventGetCritical:
		d.onGet(ev)
	case EventReleaseCritical:
		d.onRelease(ev)
	case EventJNICall:
		d.onJNICall(ev)
	case EventBlockingCall:
		d.onBlocking(ev)
	case EventPtrRead, EventPtrWrite:
		d.onPtrAccess(ev)
	case EventThreadEnd:
		d.onThreadEnd(ev)
	}
}

// Findings 返回目前累计的全部报告。
func (d *Detector) Findings() []Finding {
	out := make([]Finding, len(d.findings))
	copy(out, d.findings)
	return out
}

func (d *Detector) report(kind FindingKind, ev Event, arrayID, detail string) {
	d.findings = append(d.findings, Finding{
		Kind:     kind,
		ThreadID: ev.ThreadID,
		ArrayID:  arrayID,
		Detail:   detail,
	})
}

func (d *Detector) thread(id string) *threadState {
	ts, ok := d.threads[id]
	if !ok {
		ts = &threadState{active: make(map[string]*arrayState)}
		d.threads[id] = ts
	}
	return ts
}

func (d *Detector) onGet(ev Event) {
	ts := d.thread(ev.ThreadID)
	st := &arrayState{id: ev.ArrayID, base: ev.Addr, length: ev.Length}
	ts.active[ev.ArrayID] = st
	d.byAddr[ev.Addr] = st
}

func (d *Detector) onRelease(ev Event) {
	ts := d.thread(ev.ThreadID)
	st, ok := ts.active[ev.ArrayID]
	if !ok {
		d.report(FindingDoubleRelease, ev, ev.ArrayID,
			fmt.Sprintf("ReleasePrimitiveArrayCritical on array %q which is not held (already released or never acquired)", ev.ArrayID))
		return
	}
	delete(ts.active, ev.ArrayID)
	st.released = true
	if ev.Mode == ReleaseAbort && st.writes > 0 {
		d.report(FindingAbortDiscardsWrites, ev, ev.ArrayID,
			fmt.Sprintf("JNI_ABORT release of array %q discards %d write(s) made to the copy inside the critical section", ev.ArrayID, st.writes))
	}
}

func (d *Detector) inCritical(id string) bool {
	ts, ok := d.threads[id]
	return ok && len(ts.active) > 0
}

func (d *Detector) onJNICall(ev Event) {
	if !d.inCritical(ev.ThreadID) {
		return
	}
	d.report(FindingJNICallInCritical, ev, "",
		fmt.Sprintf("JNI function %s called while thread holds a critical section; only Release...Critical and nested Get...Critical are allowed", ev.Name))
}

func (d *Detector) onBlocking(ev Event) {
	if !d.inCritical(ev.ThreadID) {
		return
	}
	d.report(FindingBlockingInCritical, ev, "",
		fmt.Sprintf("blocking call %s inside critical section may stall garbage collection", ev.Name))
}

func (d *Detector) onPtrAccess(ev Event) {
	// 命中已释放数组：释放后读写。
	for _, st := range d.byAddr {
		if st.released && inRange(ev.Addr, ev.Size, st) {
			verb := "read"
			if ev.Kind == EventPtrWrite {
				verb = "write"
			}
			d.report(FindingUseAfterRelease, ev, st.id,
				fmt.Sprintf("%s of %d byte(s) at 0x%x touches released array %q [0x%x, 0x%x)", verb, ev.Size, ev.Addr, st.id, st.base, st.base+uintptr(st.length)))
			return
		}
	}
	// 命中活动数组：越界写检测。
	for _, st := range d.byAddr {
		if st.released || !belongsTo(ev, st) {
			continue
		}
		if ev.Kind == EventPtrWrite {
			st.writes++
			if ev.Size > 0 && ev.Addr+uintptr(ev.Size) > st.base+uintptr(st.length) {
				over := ev.Addr + uintptr(ev.Size) - (st.base + uintptr(st.length))
				d.report(FindingWriteOutOfBounds, ev, st.id,
					fmt.Sprintf("write of %d byte(s) at 0x%x exceeds array %q length %d by %d byte(s)", ev.Size, ev.Addr, st.id, st.length, over))
			}
		}
		return
	}
}

// belongsTo 判断一次访问是否归属于该活动数组。读要求区间相交；
// 写放宽到起点落在 [base, base+len] 闭区间，使恰好从末端开始的越界写也能归因。
func belongsTo(ev Event, st *arrayState) bool {
	if ev.Kind == EventPtrWrite {
		return ev.Addr >= st.base && ev.Addr <= st.base+uintptr(st.length)
	}
	return inRange(ev.Addr, ev.Size, st)
}

// inRange 判断 [addr, addr+size) 是否与数组区间相交。
func inRange(addr uintptr, size int, st *arrayState) bool {
	end := addr + uintptr(size)
	arrEnd := st.base + uintptr(st.length)
	return addr < arrEnd && end > st.base
}

func (d *Detector) onThreadEnd(ev Event) {
	ts, ok := d.threads[ev.ThreadID]
	if !ok {
		return
	}
	for _, st := range ts.active {
		d.report(FindingUnreleasedAtThreadEnd, ev, st.id,
			fmt.Sprintf("thread %s ended while still holding critical pointer for array %q (base 0x%x, length %d)", ev.ThreadID, st.id, st.base, st.length))
	}
	delete(d.threads, ev.ThreadID)
}
