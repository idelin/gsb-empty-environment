package mutex

import "errors"

var (
	// ErrLockHeld：锁被存活的持有者占有，获取失败。
	ErrLockHeld = errors.New("mutex: lock is held by a live owner")
	// ErrNotOwner：调用方不是当前记录的所有者（如释放/续约他人的锁）。
	ErrNotOwner = errors.New("mutex: caller is not the lock owner")
	// ErrLeaseExpired：租约已过期。
	ErrLeaseExpired = errors.New("mutex: lease has expired")
	// ErrStaleToken：隔离令牌已过期/不匹配，下游必须拒绝提交。
	ErrStaleToken = errors.New("mutex: fencing token is stale")
	// ErrCorruptRecord：锁记录损坏（正常路径不会发生，写盘是原子的）。
	ErrCorruptRecord = errors.New("mutex: lock record is corrupt")
)
