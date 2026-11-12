package netsec

import (
	"errors"
	"sort"
	"sync"
)

// 与 Linux prctl(PR_SET_PTRACER, ...) 对应的特殊取值。
const (
	// PtracerAny 等价于 PR_SET_PTRACER_ANY，表示允许任意进程 ptrace 本进程。
	PtracerAny = 0
)

// ErrNoSuchProcess 表示访问了进程树中不存在的进程。
var ErrNoSuchProcess = errors.New("netsec: no such process")

// ErrProcessExists 表示尝试创建的进程 ID 已被占用。
var ErrProcessExists = errors.New("netsec: process already exists")

// ErrInitExited 表示尝试让 init 退出；init 是孤儿进程的最终重挂点，不能退出。
var ErrInitExited = errors.New("netsec: init process cannot exit")

// Process 描述进程树中的一个模拟进程。
//
// 字段值是进程树某一时刻的快照，调用者不得依赖修改返回值来改变进程树。
type Process struct {
	// PID 是进程标识。
	PID int
	// PPID 是当前父进程标识；孤儿重挂后会发生变化。
	PPID int
	// UID 是进程所属用户的有效用户 ID，用于同 uid 基本检查。
	UID int
	// Caps 是进程持有的能力集合。
	Caps CapSet
	// Ptracer 是经 PR_SET_PTRACER 指定的允许 ptrace 本进程的调用者。
	// 取值为具体 PID 或 PtracerAny；零值 0 在未指定授权时表示“无授权”，
	// 指定任意进程时请用 PtracerAny 并结合 HasPtracer 区分两种语义。
	Ptracer int
}

// ProcessTree 模拟随 fork 与 exit 变化的 Linux 进程树。
//
// 树以 init（默认 PID 1）为根；进程退出后，其子进程被重新挂载到 init，
// 因此孤儿不再属于原祖先的后代集合。多个 goroutine 可并发访问。
type ProcessTree struct {
	mu     sync.RWMutex
	init   int
	procs  map[int]*Process
	parent map[int]int
	tracer map[int]int
}

// NewProcessTree 创建以 PID 1（init）为根的进程树。
func NewProcessTree() *ProcessTree {
	return NewProcessTreeWithInit(1, 0)
}

// NewProcessTreeWithInit 创建以指定 init PID 为根的进程树，便于在不使用
// PID 1 的模拟场景下复用同一套判定逻辑。initUID 为根进程的 uid。
func NewProcessTreeWithInit(initPID, initUID int) *ProcessTree {
	tree := &ProcessTree{
		init:   initPID,
		procs:  make(map[int]*Process),
		parent: make(map[int]int),
		tracer: make(map[int]int),
	}
	tree.procs[initPID] = &Process{PID: initPID, PPID: initPID, UID: initUID}
	tree.parent[initPID] = initPID
	return tree
}

// InitPID 返回根进程 init 的 PID。
func (tree *ProcessTree) InitPID() int {
	return tree.init
}

// Add 在 parentPID 下插入一个进程，用于手工搭建进程树。
// 新进程的 uid 与能力集合由参数显式给出。
func (tree *ProcessTree) Add(pid, parentPID, uid int, caps CapSet) error {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	if _, ok := tree.procs[pid]; ok {
		return ErrProcessExists
	}
	if _, ok := tree.procs[parentPID]; !ok {
		return ErrNoSuchProcess
	}
	tree.putLocked(pid, parentPID, uid, cloneCaps(caps))
	return nil
}

// Fork 创建 parentPID 的子进程，子进程自动分配 PID 并继承父进程的
// uid 与能力集合。PR_SET_PTRACER 授权不跨 fork 继承。
func (tree *ProcessTree) Fork(parentPID int) (Process, error) {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	parent, ok := tree.procs[parentPID]
	if !ok {
		return Process{}, ErrNoSuchProcess
	}
	childPID := tree.nextPIDLocked()
	child := tree.putLocked(childPID, parentPID, parent.UID, cloneCaps(parent.Caps))
	return snapshotLocked(tree, child), nil
}

// ForkAs 以显式 PID、uid 与能力集合创建子进程，用于构造非继承场景。
func (tree *ProcessTree) ForkAs(parentPID, childPID, uid int, caps CapSet) error {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	if _, ok := tree.procs[childPID]; ok {
		return ErrProcessExists
	}
	if _, ok := tree.procs[parentPID]; !ok {
		return ErrNoSuchProcess
	}
	tree.putLocked(childPID, parentPID, uid, cloneCaps(caps))
	return nil
}

// Exit 令 pid 退出：删除该进程，清除其 PR_SET_PTRACER 授权，并把它的
// 直接子进程重新挂载到 init。更深层后代通过新的父链间接挂到 init，
// 从此不再是已退出进程或其祖先的后代。
func (tree *ProcessTree) Exit(pid int) error {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	if _, ok := tree.procs[pid]; !ok {
		return ErrNoSuchProcess
	}
	if pid == tree.init {
		return ErrInitExited
	}

	delete(tree.procs, pid)
	delete(tree.parent, pid)
	delete(tree.tracer, pid)

	// 孤儿重挂到 init，模拟内核 reparent-to-subreaper/init 的行为。
	for child, parent := range tree.parent {
		if parent == pid {
			tree.parent[child] = tree.init
			tree.procs[child].PPID = tree.init
		}
	}
	return nil
}

// SetPtracer 模拟 prctl(PR_SET_PTRACER, tracerPID)。
// 传入 PtracerAny 表示 PR_SET_PTRACER_ANY；tracerPID 为具体 PID 时，
// 该进程必须仍存在，否则返回 ErrNoSuchProcess。
func (tree *ProcessTree) SetPtracer(pid, tracerPID int) error {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	if _, ok := tree.procs[pid]; !ok {
		return ErrNoSuchProcess
	}
	if tracerPID != PtracerAny {
		if _, ok := tree.procs[tracerPID]; !ok {
			return ErrNoSuchProcess
		}
	}
	tree.tracer[pid] = tracerPID
	return nil
}

// ClearPtracer 模拟 prctl(PR_SET_PTRACER, 0)，撤销之前的 ptrace 授权。
func (tree *ProcessTree) ClearPtracer(pid int) error {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	if _, ok := tree.procs[pid]; !ok {
		return ErrNoSuchProcess
	}
	delete(tree.tracer, pid)
	return nil
}

// HasPtracer 报告 pid 是否存在 PR_SET_PTRACER 授权，并返回授权值：
// ok 为 false 表示未设置；tracer 为 PtracerAny 表示任意进程，否则为
// 被指定调用者的 PID。
func (tree *ProcessTree) HasPtracer(pid int) (tracer int, ok bool, err error) {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	if _, exists := tree.procs[pid]; !exists {
		return 0, false, ErrNoSuchProcess
	}
	tracer, ok = tree.tracer[pid]
	return tracer, ok, nil
}

// Lookup 返回 pid 的进程快照；不存在时返回 ErrNoSuchProcess。
func (tree *ProcessTree) Lookup(pid int) (Process, error) {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	proc, ok := tree.procs[pid]
	if !ok {
		return Process{}, ErrNoSuchProcess
	}
	return snapshotLocked(tree, proc), nil
}

// Parent 返回 pid 当前父进程的快照。
func (tree *ProcessTree) Parent(pid int) (Process, error) {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	proc, ok := tree.procs[pid]
	if !ok {
		return Process{}, ErrNoSuchProcess
	}
	parent := tree.procs[tree.parent[proc.PID]]
	return snapshotLocked(tree, parent), nil
}

// IsDescendant 判定 ancestor 是否为 pid 的严格祖先（父、祖父……）。
// 两个参数指向同一进程时返回 false；任一进程不存在时返回 false。
func (tree *ProcessTree) IsDescendant(ancestor, pid int) bool {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	if ancestor == pid {
		return false
	}
	if _, ok := tree.procs[ancestor]; !ok {
		return false
	}
	if _, ok := tree.procs[pid]; !ok {
		return false
	}
	cur := tree.parent[pid]
	for {
		if cur == ancestor {
			return true
		}
		if cur == tree.init {
			return false
		}
		next := tree.parent[cur]
		if next == cur {
			return false
		}
		cur = next
	}
}

// PIDs 返回进程树中当前所有进程 PID 的升序快照。
func (tree *ProcessTree) PIDs() []int {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	pids := make([]int, 0, len(tree.procs))
	for pid := range tree.procs {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids
}

func (tree *ProcessTree) putLocked(pid, parentPID, uid int, caps CapSet) *Process {
	proc := &Process{
		PID:  pid,
		PPID: parentPID,
		UID:  uid,
		Caps: caps,
	}
	tree.procs[pid] = proc
	tree.parent[pid] = parentPID
	return proc
}

func (tree *ProcessTree) nextPIDLocked() int {
	pid := tree.init + 1
	for {
		if _, ok := tree.procs[pid]; !ok {
			return pid
		}
		pid++
	}
}

func snapshotLocked(tree *ProcessTree, proc *Process) Process {
	copyProc := *proc
	copyProc.Caps = cloneCaps(proc.Caps)
	if tracer, ok := tree.tracer[proc.PID]; ok {
		copyProc.Ptracer = tracer
	}
	return copyProc
}

func cloneCaps(caps CapSet) CapSet {
	if caps == nil {
		return nil
	}
	clone := make(CapSet, len(caps))
	for capability := range caps {
		clone[capability] = true
	}
	return clone
}
