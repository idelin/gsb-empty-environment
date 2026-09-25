// Package mutex 实现同主机多进程互斥协议：
//
//   - 锁状态以 JSON 记录持久化在调用方指定目录（<name>.lock.json）。
//   - 元数据的读-改-写通过 <name>.sync 上的 flock(2) 串行化，
//     保证同一时刻只有一个进程能提交新记录（compare-and-swap）。
//   - 互斥语义本身由"租约 + 进程活性 + 进程启动标识"共同决定，
//     可区分 live / lost（失联）/ stale（陈旧）三种持有状态。
//   - 每次获取产生单调递增的隔离令牌（fencing token），
//     下游资源用 ValidateToken 拒绝过期持有者的提交。
//
// 崩溃恢复与断电保证的边界见仓库 README.md。
package mutex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const defaultLeaseTTL = 5 * time.Second

// Hooks 是故障注入点，用于在测试中模拟提交路径上的崩溃。
// 返回非 nil 错误等价于"进程在该时刻宕机"。
type Hooks struct {
	// BeforeCommit 在新记录写入临时文件之后、fsync 之前调用。
	BeforeCommit func() error
	// AfterCommit 在新记录已原子落盘（rename + fsync）之后、
	// Acquire 返回之前调用；用于模拟"已提交但结果未知"的窗口。
	AfterCommit func() error
}

// Options 为可选注入项；nil 字段使用生产默认值。
type Options struct {
	Clock     Clock
	Inspector Inspector
	Hooks     Hooks
	LeaseTTL  time.Duration
}

// Handle 是一个进程内对某把锁的句柄。同一进程可持有多个 Handle
// （各自 OwnerID 不同），跨进程通过目录中的文件协调。
type Handle struct {
	dir      string
	lockPath string
	syncPath string
	ownerID  string
	clock    Clock
	insp     Inspector
	hooks    Hooks
	ttl      time.Duration

	mu    sync.Mutex
	held  bool
	token uint64
}

// New 创建句柄。dir 为调用方指定的锁文件目录（不存在则创建），
// name 为锁名（仅允许文件名安全的字符）。
func New(dir, name string, o *Options) (*Handle, error) {
	if dir == "" {
		return nil, errors.New("mutex: empty lock directory")
	}
	if name == "" || strings.ContainsAny(name, `/\`+string(os.PathSeparator)) {
		return nil, fmt.Errorf("mutex: invalid lock name %q", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	h := &Handle{
		dir:      dir,
		lockPath: filepath.Join(dir, name+".lock.json"),
		syncPath: filepath.Join(dir, name+".sync"),
		ownerID:  hex.EncodeToString(id[:]),
		clock:    realClock{},
		insp:     OSInspector{},
		ttl:      defaultLeaseTTL,
	}
	if o != nil {
		if o.Clock != nil {
			h.clock = o.Clock
		}
		if o.Inspector != nil {
			h.insp = o.Inspector
		}
		if o.LeaseTTL > 0 {
			h.ttl = o.LeaseTTL
		}
		h.hooks = o.Hooks
	}
	return h, nil
}

// OwnerID 返回本句柄的随机身份标识。
func (h *Handle) OwnerID() string { return h.ownerID }

// Token 返回本句柄当前持有的隔离令牌；未持有时为 0。
func (h *Handle) Token() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.token
}

// Acquire 尝试获取锁。成功返回隔离令牌；
// 锁被存活持有者占有返回 ErrLockHeld；记录损坏返回 ErrCorruptRecord。
// 持有者失联（租约过期）或陈旧（死亡/PID 复用）时执行接管，令牌 +1。
func (h *Handle) Acquire() (uint64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var token uint64
	err := h.withLock(func() error {
		rec, err := h.readLocked()
		if err != nil {
			return err
		}
		if st := h.classify(rec); st == StateLive {
			if rec.OwnerID == h.ownerID {
				// 上次提交结果未知（AfterCommit 注入点）后的恢复：
				// 记录本来就是我们的，直接恢复持有状态。
				token = rec.Token
				h.held, h.token = true, rec.Token
				return nil
			}
			return fmt.Errorf("%w: pid=%d token=%d", ErrLockHeld, rec.PID, rec.Token)
		}
		self := h.insp.Inspect(os.Getpid())
		host, _ := os.Hostname()
		next := Record{
			Token:      rec.Token + 1,
			OwnerID:    h.ownerID,
			PID:        os.Getpid(),
			Host:       host,
			StartToken: self.StartToken,
			ExpiresAt:  h.clock.Now().Add(h.ttl).UnixNano(),
		}
		if err := h.writeRecordLocked(next); err != nil {
			return err
		}
		token = next.Token
		h.held, h.token = true, next.Token
		return nil
	})
	return token, err
}

// AcquireBlocking 以固定间隔重试 Acquire，直到成功或 ctx 取消。
func (h *Handle) AcquireBlocking(ctx context.Context, retry time.Duration) (uint64, error) {
	for {
		tok, err := h.Acquire()
		if !errors.Is(err, ErrLockHeld) {
			return tok, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(retry):
		}
	}
}

// Renew 延长租约。持有者已失联（被接管或租约过期）时返回错误，
// 此后它不得再凭旧令牌提交任何副作用。
func (h *Handle) Renew() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.withLock(func() error {
		rec, err := h.readLocked()
		if err != nil {
			return err
		}
		if rec.OwnerID != h.ownerID || rec.Token != h.token {
			h.held, h.token = false, 0
			return ErrNotOwner
		}
		if h.expired(rec) {
			h.held, h.token = false, 0
			return ErrLeaseExpired
		}
		rec.ExpiresAt = h.clock.Now().Add(h.ttl).UnixNano()
		return h.writeRecordLocked(rec)
	})
}

// Release 释放锁。记录中的 OwnerID 必须与本句柄一致，
// 否则返回 ErrNotOwner——不能释放别人的锁（包括旧持有者
// 过期后试图释放新持有者的锁）。释放保留 Token 以维持单调性。
func (h *Handle) Release() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.withLock(func() error {
		rec, err := h.readLocked()
		if err != nil {
			return err
		}
		if rec.Free() {
			h.held, h.token = false, 0
			return nil
		}
		if rec.OwnerID != h.ownerID {
			return fmt.Errorf("%w: held by pid=%d token=%d", ErrNotOwner, rec.PID, rec.Token)
		}
		if err := h.writeRecordLocked(Record{Token: rec.Token}); err != nil {
			return err
		}
		h.held, h.token = false, 0
		return nil
	})
}

// ValidateToken 是下游资源的隔离栅栏：仅当 token 等于当前记录的
// 令牌且租约未过期时返回 nil。旧持有者过期/被接管后，其旧令牌
// 必然小于当前令牌，提交会被拒绝。
func (h *Handle) ValidateToken(token uint64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.withLock(func() error {
		rec, err := h.readLocked()
		if err != nil {
			return err
		}
		if token == 0 || rec.Free() || token != rec.Token {
			return fmt.Errorf("%w: got %d, current %d", ErrStaleToken, token, rec.Token)
		}
		if h.expired(rec) {
			return ErrLeaseExpired
		}
		return nil
	})
}

// Inspect 返回当前记录与状态分类，用于诊断与监控。
func (h *Handle) Inspect() (Record, State, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var st State
	var rec Record
	err := h.withLock(func() error {
		r, err := h.readLocked()
		if err != nil {
			return err
		}
		rec, st = r, h.classify(r)
		return nil
	})
	return rec, st, err
}

// classify 将记录分类为 free/live/lost/stale。
func (h *Handle) classify(rec Record) State {
	if rec.Free() {
		return StateFree
	}
	info := h.insp.Inspect(rec.PID)
	if !info.Alive {
		return StateStale // 进程已退出
	}
	if rec.StartToken != "" && info.StartToken != "" && rec.StartToken != info.StartToken {
		return StateStale // PID 被复用：活着的不是原持有者
	}
	// StartToken 任一缺失时无法证伪，按存活保守处理，不抢占。
	if h.expired(rec) {
		return StateLost // 失联：进程在但租约停了
	}
	return StateLive
}

func (h *Handle) expired(rec Record) bool {
	return h.clock.Now().UnixNano() >= rec.ExpiresAt
}

// withLock 在 <name>.sync 上取 flock(LOCK_EX)，串行化元数据的读-改-写。
// 这是协议内部的 CAS 原语；客户端可见的互斥语义由租约层提供。
func (h *Handle) withLock(fn func() error) error {
	f, err := os.OpenFile(h.syncPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close() // 关闭即释放 flock
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	return fn()
}

func (h *Handle) readLocked() (Record, error) {
	data, err := os.ReadFile(h.lockPath)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, nil
	}
	if err != nil {
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, fmt.Errorf("%w: %v", ErrCorruptRecord, err)
	}
	return rec, nil
}

// writeRecordLocked 以"临时文件 + fsync + 原子 rename + 目录 fsync"
// 提交记录，保证掉电后要么看到旧记录要么看到新记录。
func (h *Handle) writeRecordLocked(rec Record) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := h.lockPath + ".tmp." + h.ownerID
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if h.hooks.BeforeCommit != nil {
		if err := h.hooks.BeforeCommit(); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, h.lockPath); err != nil {
		os.Remove(tmp)
		return err
	}
	if h.hooks.AfterCommit != nil {
		if err := h.hooks.AfterCommit(); err != nil {
			return err // 记录已持久化，但提交结果对调用方未知
		}
	}
	return syncDir(h.dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
