package netsec

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Barrier 是一次性闸门：到达的 goroutine 阻塞，直到另一个 goroutine
// 放行。用于把竞态窗口钉在确定性的时刻（例如视图读取中途）。
type Barrier struct {
	enterOnce sync.Once
	closeOnce sync.Once
	ch        chan struct{}
	fired     chan struct{}
}

func NewBarrier() *Barrier {
	return &Barrier{ch: make(chan struct{}), fired: make(chan struct{})}
}

// Wait 阻塞直到 Release 被调用，或 ctx 被取消。
func (b *Barrier) Wait(ctx context.Context) error {
	b.enterOnce.Do(func() { close(b.fired) })
	select {
	case <-b.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release 放行所有等待者，可重复调用。
func (b *Barrier) Release() {
	b.closeOnce.Do(func() { close(b.ch) })
}

// Fired 在有等待者进入 Wait 后关闭。
func (b *Barrier) Fired() <-chan struct{} { return b.fired }

// Store 模拟一个按路径访问的对象存储。
//
// 正常宿主只能通过 OpenPath 拿到句柄；测量执行组件全程只接收句柄，
// pathOpens 记录所有按路径打开的次数，测试据此断言执行阶段没有重新
// 按路径打开。
type Store struct {
	mu        sync.Mutex
	pathIndex map[string]*MockObject
	pathOpens int64
	nonceSeq  uint64
	objectSeq uint64
}

func NewStore() *Store {
	return &Store{pathIndex: make(map[string]*MockObject)}
}

// Create 在指定路径创建对象。
func (s *Store) Create(path string, data []byte) *MockObject {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.freshObjectIDLocked()
	obj := &MockObject{
		store: s,
		id:    id,
		path:  path,
		seq:   1,
		nonce: s.freshNonceLocked(id),
		data:  append([]byte(nil), data...),
	}
	s.pathIndex[path] = obj
	return obj
}

// OpenPath 模拟按路径打开，宿主初始化句柄时调用。
func (s *Store) OpenPath(path string) (*MockObject, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.pathIndex[path]
	if ok {
		atomic.AddInt64(&s.pathOpens, 1)
	}
	return obj, ok
}

// PathOpenCount 返回按路径打开的累计次数。
func (s *Store) PathOpenCount() int64 { return atomic.LoadInt64(&s.pathOpens) }

func (s *Store) freshNonceLocked(id string) string {
	s.nonceSeq++
	return fmt.Sprintf("nonce-%s-%d", id, s.nonceSeq)
}

func (s *Store) freshObjectIDLocked() string {
	s.objectSeq++
	return fmt.Sprintf("obj-%d", s.objectSeq)
}

// MockObject 是可阻塞的模拟对象句柄，实现 Object 接口。
type MockObject struct {
	store *Store

	mu    sync.Mutex
	id    string
	path  string
	seq   uint64
	nonce string
	data  []byte

	// 读屏障：视图每次 Read 前在其上等待，nil 表示不阻塞。
	readGate *Barrier
	// 打开视图钩子，nil 表示不拦截。
	openHook func()
}

var _ Object = (*MockObject)(nil)

func (o *MockObject) ID() string { return o.id }

func (o *MockObject) Path() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.path
}

func (o *MockObject) Proof(context.Context) (VersionProof, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return VersionProof{ObjectID: o.id, Seq: o.seq, Nonce: o.nonce}, nil
}

// SetReadGate 安装读屏障，之后每次视图 Read 都会先阻塞在该屏障上。
func (o *MockObject) SetReadGate(g *Barrier) {
	o.mu.Lock()
	o.readGate = g
	o.mu.Unlock()
}

// SetOpenHook 在每次 OpenView 进入时回调（持锁前调用），用于精确制造
// “取证明之后、打开视图之时”的竞态。
func (o *MockObject) SetOpenHook(hook func()) {
	o.mu.Lock()
	o.openHook = hook
	o.mu.Unlock()
}

// OpenView 基于句柄生成当前版本的不可变快照视图。
func (o *MockObject) OpenView(ctx context.Context) (View, error) {
	o.mu.Lock()
	hook := o.openHook
	gate := o.readGate
	snapshot := append([]byte(nil), o.data...)
	proof := VersionProof{ObjectID: o.id, Seq: o.seq, Nonce: o.nonce}
	o.mu.Unlock()

	if hook != nil {
		hook()
	}
	if gate != nil {
		if err := gate.Wait(ctx); err != nil {
			return nil, err
		}
	}
	return &mockView{data: snapshot, proof: proof, gate: gate, ctx: ctx}, nil
}

// ModifyInPlace 原地改写内容：同一对象、同一路径，版本号递增并换新 nonce。
func (o *MockObject) ModifyInPlace(data []byte) VersionProof {
	o.store.mu.Lock()
	o.mu.Lock()
	o.data = append([]byte(nil), data...)
	o.seq++
	o.nonce = o.store.freshNonceLocked(o.id)
	proof := VersionProof{ObjectID: o.id, Seq: o.seq, Nonce: o.nonce}
	o.mu.Unlock()
	o.store.mu.Unlock()
	return proof
}

// Rename 只改路径：对象标识、内容与版本证明完全不变。
func (o *MockObject) Rename(newPath string) {
	o.store.mu.Lock()
	o.mu.Lock()
	delete(o.store.pathIndex, o.path)
	o.path = newPath
	o.store.pathIndex[newPath] = o
	o.mu.Unlock()
	o.store.mu.Unlock()
}

// ReplaceWithReusedSeq 用一个新对象替换当前路径，并显式复用旧版本号：
// Seq 相同但 ObjectID 与 Nonce 均不同，模拟存储层版本号回绕/复用。
func (o *MockObject) ReplaceWithReusedSeq(data []byte) *MockObject {
	o.store.mu.Lock()
	defer o.store.mu.Unlock()

	o.mu.Lock()
	path := o.path
	reusedSeq := o.seq
	o.mu.Unlock()

	next := &MockObject{
		store: o.store,
		id:    o.store.freshObjectIDLocked(),
		path:  path,
		seq:   reusedSeq,
		nonce: o.store.freshNonceLocked("replacement"),
		data:  append([]byte(nil), data...),
	}
	o.store.pathIndex[path] = next
	return next
}

// SnapshotData 返回对象当前内容的拷贝。
func (o *MockObject) SnapshotData() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.data...)
}

// mockView 是对象某一版本的不可变快照。
type mockView struct {
	data  []byte
	pos   int64
	proof VersionProof
	gate  *Barrier
	ctx   context.Context
}

var _ View = (*mockView)(nil)

func (v *mockView) Read(p []byte) (int, error) {
	if v.gate != nil {
		if err := v.gate.Wait(v.ctx); err != nil {
			return 0, err
		}
	}
	if v.pos >= int64(len(v.data)) {
		return 0, io.EOF
	}
	n := copy(p, v.data[v.pos:])
	v.pos += int64(n)
	if v.pos >= int64(len(v.data)) {
		return n, io.EOF
	}
	return n, nil
}

func (v *mockView) Seek(offset int64, whence int) (int64, error) {
	var target int64
	switch whence {
	case 0:
		target = offset
	case 1:
		target = v.pos + offset
	case 2:
		target = int64(len(v.data)) + offset
	default:
		return 0, fmt.Errorf("netsec: invalid seek whence %d", whence)
	}
	if target < 0 || target > int64(len(v.data)) {
		return 0, fmt.Errorf("netsec: seek out of range")
	}
	v.pos = target
	return target, nil
}

func (v *mockView) Close() error        { return nil }
func (v *mockView) Proof() VersionProof { return v.proof }
func (v *mockView) Size() int64         { return int64(len(v.data)) }
