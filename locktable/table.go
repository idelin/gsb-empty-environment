// Package locktable 实现面向存储服务端的 POSIX 区间锁表，用于抵御
// 伪造 lk-owner 的锁窃取攻击。
//
// 安全模型要点：
//   - 锁归属键是 (连接标识, lk-owner) 二元组；连接标识由传输层在认证
//     完成后通过 Connect 登记，绝不取自锁请求体，因此请求里声明的
//     lk-owner 无法越权到其他连接。
//   - 同一 lk-owner 出现在不同连接上属于不同属主，彼此互不可见。
//   - UNLOCK 只按当前连接解析属主键，只能释放本连接持有的区间，并允许
//     把一个持有区间拆成多段（POSIX 风格的整段/子区间释放）。
//   - 连接断开后其锁进入固定 10 秒宽限期；期内只有凭断开前由本表
//     发放的 reclaim 令牌发起的 Reclaim 才能原样取回，过期即清除。
//   - clear-locks 类管理操作（ClearLocks）要求连接具备管理身份。
//   - 阻塞队列中的请求一旦所属连接断开，立即从队列移除并失败返回。
package locktable

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"
	"time"
)

// GracePeriod 是连接断开后其锁保留的宽限期，固定为 10 秒。
const GracePeriod = 10 * time.Second

var (
	// ErrClosed 表示锁表已关闭，不再接受新连接。
	ErrClosed = errors.New("locktable: table is closed")
	// ErrUnknownConn 表示连接标识未登记或已经断开。
	ErrUnknownConn = errors.New("locktable: unknown connection")
	// ErrInvalidRange 表示锁区间非法（Start > End）。
	ErrInvalidRange = errors.New("locktable: invalid byte range")
	// ErrPermissionDenied 表示非管理身份发起了管理操作。
	ErrPermissionDenied = errors.New("locktable: administrative identity required")
	// ErrConflict 表示 TryLock 与其他属主（含宽限期内的锁）冲突。
	ErrConflict = errors.New("locktable: range is locked by another owner")
	// ErrLockNotHeld 表示待解锁区间没有任何由本连接该属主持有的锁。
	ErrLockNotHeld = errors.New("locktable: no matching lock held by this owner")
	// ErrBlockerGone 表示阻塞等待期间，所属连接已断开，请求被移出队列。
	ErrBlockerGone = errors.New("locktable: blocker removed because its connection disconnected")
	// ErrLocksCleared 表示阻塞等待期间，管理操作清空了全部锁与队列。
	ErrLocksCleared = errors.New("locktable: waiting request removed by clear-locks operation")
	// ErrNoReclaimToken 表示断开前未申请过 reclaim 令牌，无法重取。
	ErrNoReclaimToken = errors.New("locktable: no reclaim token was issued before disconnect")
	// ErrTokenMismatch 表示 reclaim 令牌与断开前发放的令牌不一致。
	ErrTokenMismatch = errors.New("locktable: reclaim token does not match")
	// ErrGraceExpired 表示宽限期已过，锁已被清除。
	ErrGraceExpired = errors.New("locktable: grace period expired and locks were cleared")
)

// ConnID 是传输层认证后赋予连接的不透明标识，由锁表发放而非请求提供。
type ConnID uint64

// LkOwner 是请求中携带的锁属主标识。单独的 LkOwner 不构成属主键，
// 必须与服务端持有的 ConnID 组合后才能定位锁。
type LkOwner string

// ReclaimToken 是断开前由锁表发放的不透明重取令牌。
type ReclaimToken string

// LockType 区分读锁与写锁：写锁互斥，读锁之间共享。
type LockType int

const (
	// ReadLock 共享读锁。
	ReadLock LockType = iota + 1
	// WriteLock 独占写锁。
	WriteLock
)

// ByteRange 是闭区间 [Start, End]（两端均含），单位为字节。
type ByteRange struct {
	Start uint64
	End   uint64
}

// HeldLock 描述一个属主持有的单个锁区间。
type HeldLock struct {
	Range ByteRange
	Type  LockType
}

type lockEntry struct {
	rng ByteRange
	typ LockType
}

type ownerKey struct {
	conn  ConnID
	owner LkOwner
}

type connState struct {
	admin        bool
	reclaimToken ReclaimToken
}

type graceEntry struct {
	conn   ConnID
	owner  LkOwner
	locks  []lockEntry
	token  ReclaimToken
	expire time.Time
}

type waiter struct {
	conn  ConnID
	owner LkOwner
	rng   ByteRange
	typ   LockType
	ch    chan struct{}
	err   error
}

// Table 是并发安全的 POSIX 区间锁表。零值不可用，须用 New 创建。
type Table struct {
	mu sync.Mutex

	clock  Clock
	closed bool
	nextID ConnID
	conns  map[ConnID]*connState
	locks  map[ownerKey][]lockEntry
	grace  map[ownerKey]*graceEntry

	waiters  []*waiter
	expiredT map[ReclaimToken]struct{}

	kick chan struct{}
	done chan struct{}
	wg   sync.WaitGroup
}

// New 创建锁表并启动宽限期回收协程，使用真实时钟与 10 秒宽限期。
func New() *Table {
	return newWithClock(NewRealClock())
}

// NewForTest 使用调用方提供的时钟创建锁表，便于确定性地测试宽限期逻辑。
func NewForTest(clock Clock) *Table {
	return newWithClock(clock)
}

// newWithClock 供测试注入可控时钟。
func newWithClock(clock Clock) *Table {
	table := &Table{
		clock:    clock,
		nextID:   1,
		conns:    make(map[ConnID]*connState),
		locks:    make(map[ownerKey][]lockEntry),
		grace:    make(map[ownerKey]*graceEntry),
		expiredT: make(map[ReclaimToken]struct{}),
		kick:     make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	table.wg.Add(1)
	go table.janitor()
	return table
}

// Close 停止后台回收协程并清空全部锁、宽限期记录与阻塞队列。
// 阻塞中的请求以 ErrLocksCleared 失败返回。
func (t *Table) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	pending := t.waiters
	t.waiters = nil
	t.locks = make(map[ownerKey][]lockEntry)
	t.grace = make(map[ownerKey]*graceEntry)
	t.conns = make(map[ConnID]*connState)
	t.mu.Unlock()

	close(t.done)
	t.wg.Wait()

	for _, w := range pending {
		w.err = ErrLocksCleared
		close(w.ch)
	}
}

// Connect 登记一个已通过传输层认证的连接并返回服务端连接标识。
// admin 为 true 时该连接具备执行 clear-locks 管理操作的身份。
// 连接标识完全由锁表分配，调用方无法指定或伪造。
func (t *Table) Connect(admin bool) (ConnID, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, ErrClosed
	}
	id := t.nextID
	t.nextID++
	t.conns[id] = &connState{admin: admin}
	return id, nil
}

// IssueReclaimToken 为活跃连接发放不透明 reclaim 令牌。
// 令牌必须在断开之前申请；断开后持该令牌的新连接才能在宽限期内重取锁。
// 同一连接重复调用返回同一个令牌。
func (t *Table) IssueReclaimToken(id ConnID) (ReclaimToken, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	conn, ok := t.conns[id]
	if !ok {
		return "", ErrUnknownConn
	}
	if conn.reclaimToken == "" {
		token, err := newReclaimToken()
		if err != nil {
			return "", err
		}
		conn.reclaimToken = token
	}
	return conn.reclaimToken, nil
}

// Disconnect 注销连接：其阻塞请求立即失败移除，持有的锁进入宽限期。
// 对未知或已断开的连接调用为空操作。
func (t *Table) Disconnect(id ConnID) {
	t.mu.Lock()
	conn, ok := t.conns[id]
	if !ok {
		t.mu.Unlock()
		return
	}
	delete(t.conns, id)

	kept := t.detachWaitersLocked(id, ErrBlockerGone)

	createdGrace := 0
	for key, entries := range t.locks {
		if key.conn != id {
			continue
		}
		delete(t.locks, key)
		t.grace[key] = &graceEntry{
			conn:   key.conn,
			owner:  key.owner,
			locks:  append([]lockEntry(nil), entries...),
			token:  conn.reclaimToken,
			expire: t.clock.Now().Add(GracePeriod),
		}
		createdGrace++
	}
	t.mu.Unlock()

	for _, w := range kept {
		close(w.ch)
	}
	if createdGrace > 0 {
		t.kickJanitor()
	}
}

// janitor 是唯一负责宽限期过期回收的后台协程，避免每个断连派生定时器协程。
func (t *Table) janitor() {
	defer t.wg.Done()

	var timerC <-chan time.Time
	for {
		// timerC == nil 表示当前没有在途定时器：重新计算最早到期时间并注册；
		// 否则挂起等待在途定时器、新断连事件或关闭，避免假时钟下空转。
		clock := t.clock
		if timerC == nil {
			t.mu.Lock()
			next := time.Time{}
			for _, entry := range t.grace {
				if next.IsZero() || entry.expire.Before(next) {
					next = entry.expire
				}
			}
			t.mu.Unlock()

			if next.IsZero() {
				// 没有宽限期记录，挂起等待新的断连或关闭。
				select {
				case <-t.done:
					return
				case <-t.kick:
					continue
				}
			}

			wait := next.Sub(clock.Now())
			if wait < 0 {
				wait = 0
			}
			timerC = clock.After(wait)
		}

		select {
		case <-t.done:
			return
		case <-t.kick:
			// 新的宽限期记录可能更早到期，丢弃在途定时器并重新计算。
			timerC = nil
			continue
		case fired := <-timerC:
			t.mu.Lock()
			for key, entry := range t.grace {
				if !entry.expire.After(fired) {
					if entry.token != "" {
						t.expiredT[entry.token] = struct{}{}
					}
					delete(t.grace, key)
				}
			}
			t.pumpLocked()
			t.mu.Unlock()
			timerC = nil
		}
	}
}

func (t *Table) kickJanitor() {
	select {
	case t.kick <- struct{}{}:
	default:
	}
}

// TryLock 非阻塞地申请区间锁。冲突（含与宽限期内的锁冲突）时返回
// ErrConflict。同属主对同一区间重新加锁按 POSIX 语义替换锁类型，
// 相邻同类型区间自动合并。
func (t *Table) TryLock(id ConnID, owner LkOwner, rng ByteRange, typ LockType) error {
	if typ != ReadLock && typ != WriteLock {
		return fmt.Errorf("locktable: unknown lock type %d", typ)
	}
	if err := validateRange(rng); err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	if _, ok := t.conns[id]; !ok {
		return ErrUnknownConn
	}
	if t.conflictsLocked(id, owner, rng, typ) {
		return ErrConflict
	}
	t.addRangeLocked(id, owner, rng, typ)
	t.pumpLocked()
	return nil
}

// Reclaim 在宽限期内凭断开前发放的令牌，把旧连接的锁原样转移到新连接。
// 旧锁的属主、区间与锁类型保持不变；成功后宽限期记录与令牌立即失效。
func (t *Table) Reclaim(newConn ConnID, owner LkOwner, token ReclaimToken) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	if _, ok := t.conns[newConn]; !ok {
		return ErrUnknownConn
	}

	// 宽限期已过的令牌只会命中过期集合，杜绝凭旧令牌重新获得锁。
	if _, expired := t.expiredT[token]; expired {
		return ErrGraceExpired
	}

	matched := (*graceEntry)(nil)
	matchedKey := ownerKey{}
	for key, entry := range t.grace {
		if entry.owner == owner {
			matched = entry
			matchedKey = key
			break
		}
	}
	if matched == nil {
		// 不存在该属主的宽限记录：可能从未持锁、已被重取或已过期清除。
		return ErrGraceExpired
	}
	if matched.token == "" {
		return ErrNoReclaimToken
	}
	if subtle.ConstantTimeCompare([]byte(matched.token), []byte(token)) != 1 {
		return ErrTokenMismatch
	}

	restored := append([]lockEntry(nil), matched.locks...)
	delete(t.grace, matchedKey)

	key := ownerKey{conn: newConn, owner: owner}
	existing := t.locks[key]
	for _, entry := range restored {
		existing = insertEntry(existing, entry)
	}
	t.locks[key] = existing
	t.pumpLocked()
	return nil
}

// ClearLocks 执行 clear-locks 类管理操作。
//
// filter 为 nil 时清空全部活跃锁、宽限期记录与阻塞队列（队列请求以
// ErrLocksCleared 失败）；filter 非 nil 时只清除与之重叠的锁区间（可
// 拆分），并重新评估阻塞队列。无论哪种形式，都要求 id 对应的连接在
// Connect 时声明过管理身份，否则返回 ErrPermissionDenied。
func (t *Table) ClearLocks(id ConnID, filter *ByteRange) error {
	if filter != nil {
		if err := validateRange(*filter); err != nil {
			return err
		}
	}

	t.mu.Lock()
	conn, ok := t.conns[id]
	if !ok {
		t.mu.Unlock()
		return ErrUnknownConn
	}
	if !conn.admin {
		t.mu.Unlock()
		return ErrPermissionDenied
	}

	if filter == nil {
		pending := t.waiters
		t.waiters = nil
		t.locks = make(map[ownerKey][]lockEntry)
		t.grace = make(map[ownerKey]*graceEntry)
		t.mu.Unlock()

		for _, w := range pending {
			w.err = ErrLocksCleared
			close(w.ch)
		}
		return nil
	}

	for key, entries := range t.locks {
		kept := make([]lockEntry, 0, len(entries)+1)
		for _, entry := range entries {
			pieces, _ := subtract(entry.rng, *filter)
			for _, piece := range pieces {
				kept = append(kept, lockEntry{rng: piece, typ: entry.typ})
			}
		}
		if len(kept) == 0 {
			delete(t.locks, key)
		} else {
			t.locks[key] = kept
		}
	}
	for key, entry := range t.grace {
		kept := make([]lockEntry, 0, len(entry.locks)+1)
		for _, lock := range entry.locks {
			pieces, _ := subtract(lock.rng, *filter)
			for _, piece := range pieces {
				kept = append(kept, lockEntry{rng: piece, typ: lock.typ})
			}
		}
		if len(kept) == 0 {
			delete(t.grace, key)
		} else {
			entry.locks = kept
		}
	}
	t.pumpLocked()
	t.mu.Unlock()
	return nil
}

// HeldLocks 返回某连接上某属主当前持有的锁快照，主要用于测试与诊断。
func (t *Table) HeldLocks(id ConnID, owner LkOwner) []HeldLock {
	t.mu.Lock()
	defer t.mu.Unlock()
	entries := t.locks[ownerKey{conn: id, owner: owner}]
	out := make([]HeldLock, 0, len(entries))
	for _, entry := range entries {
		out = append(out, HeldLock{Range: entry.rng, Type: entry.typ})
	}
	return out
}

// Pending 返回阻塞队列当前长度，主要用于测试与诊断。
func (t *Table) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.waiters)
}

// GraceCount 返回当前处于宽限期内的属主记录数，主要用于测试与诊断。
func (t *Table) GraceCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.grace)
}

// Lock 阻塞申请区间锁：无法立即满足时进入 FIFO 队列。ctx 取消时请求
// 从队列移除并返回 ctx.Err()；连接在等待中断开则返回 ErrBlockerGone。
func (t *Table) Lock(ctx context.Context, id ConnID, owner LkOwner, rng ByteRange, typ LockType) error {
	if ctx == nil {
		return errors.New("locktable: nil context")
	}
	if typ != ReadLock && typ != WriteLock {
		return fmt.Errorf("locktable: unknown lock type %d", typ)
	}
	if err := validateRange(rng); err != nil {
		return err
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrClosed
	}
	if _, ok := t.conns[id]; !ok {
		t.mu.Unlock()
		return ErrUnknownConn
	}
	if !t.conflictsLocked(id, owner, rng, typ) {
		t.addRangeLocked(id, owner, rng, typ)
		t.pumpLocked()
		t.mu.Unlock()
		return nil
	}

	w := &waiter{
		conn:  id,
		owner: owner,
		rng:   rng,
		typ:   typ,
		ch:    make(chan struct{}),
	}
	t.waiters = append(t.waiters, w)
	t.mu.Unlock()

	select {
	case <-ctx.Done():
		t.mu.Lock()
		if !t.removeWaiterLocked(w) {
			// 队列已在取消生效前授予本请求，接受授予结果。
			t.mu.Unlock()
			<-w.ch
			return w.err
		}
		t.mu.Unlock()
		return ctx.Err()
	case <-w.ch:
		return w.err
	}
}

// Unlock 释放本连接上属主 owner 持有的、与 rng 重叠的锁区间。
// 属主键严格按 (当前连接, owner) 解析：其他连接即便使用相同 lk-owner
// 也无法触及这些锁。区间可以是已持有锁的子区间，剩余部分被拆分保留；
// 本连接该属主在 rng 上没有任何锁时返回 ErrLockNotHeld。
func (t *Table) Unlock(id ConnID, owner LkOwner, rng ByteRange) error {
	if err := validateRange(rng); err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	if _, ok := t.conns[id]; !ok {
		return ErrUnknownConn
	}
	key := ownerKey{conn: id, owner: owner}
	entries, ok := t.locks[key]
	if !ok {
		return ErrLockNotHeld
	}

	result := make([]lockEntry, 0, len(entries)+1)
	freed := false
	for _, entry := range entries {
		pieces, overlaps := subtract(entry.rng, rng)
		if overlaps {
			freed = true
		}
		for _, piece := range pieces {
			result = append(result, lockEntry{rng: piece, typ: entry.typ})
		}
	}
	if !freed {
		return ErrLockNotHeld
	}

	if len(result) == 0 {
		delete(t.locks, key)
	} else {
		t.locks[key] = result
	}
	t.pumpLocked()
	return nil
}
