// Package freeze 实现终端防护场景下的进程树冻结协议。
//
// 枚举与冻结不是原子操作：子进程可能在枚举之后继续派生，进程也可能
// 跨树迁入（被重新挂接到树内），PID 还可能被复用。协议以可信启动代
// （PID + Start）标识进程实例，分轮执行“枚举-冻结”，直到某一整轮
// 快照中的父子闭包完全被冻结集合覆盖，才判定成功；在轮次上限内无法
// 稳定时返回 ErrIncomplete 而非成功。任何非成功结局都会按冻结的
// 逆序（反向依赖）恢复，且仅恢复本次运行实际冻结的实例。
package freeze

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// Key 可信地标识一个进程实例。PID 会被复用，必须连同启动代一起比较。
type Key struct {
	PID   int
	Start uint64
}

// Process 是单次枚举得到的进程快照。
type Process struct {
	PID   int
	PPID  int
	Start uint64
}

// Enumerator 枚举当前进程表。由宿主注入，便于平台适配与测试。
type Enumerator interface {
	Enumerate(ctx context.Context) ([]Process, error)
}

// Freezer 冻结/恢复单个进程实例。由宿主注入。
//
// Freeze 与 Thaw 都必须在校验 (pid, start) 匹配后才执行，防止 PID
// 复用导致误伤。若目标进程已被其他主体冻结，Freeze 应返回
// ErrAlreadyFrozen。
type Freezer interface {
	Freeze(ctx context.Context, pid int, start uint64) error
	Thaw(ctx context.Context, pid int, start uint64) error
}

var (
	// ErrIncomplete 表示在轮次上限内冻结集合无法覆盖稳定的父子闭包。
	ErrIncomplete = errors.New("freeze: process tree did not stabilize")
	// ErrAlreadyFrozen 由 Freezer.Freeze 返回，表示进程已被其他主体冻结。
	ErrAlreadyFrozen = errors.New("freeze: process already frozen")
	// ErrRootChanged 表示可信根在冻结期间退出或其 PID 被复用。
	ErrRootChanged = errors.New("freeze: trusted root changed")
)

const defaultMaxRounds = 8

// Controller 执行冻结协议。
type Controller struct {
	Enumerator Enumerator
	Freezer    Freezer
	// MaxRounds 为枚举-冻结轮次上限；<= 0 时使用默认值。
	MaxRounds int
}

// Result 描述一次成功的冻结。
type Result struct {
	// Frozen 为本次运行实际冻结且结束时仍存活的进程实例，
	// 按冻结先后排序（父先于子）。
	Frozen []Key
	// Rounds 为达到稳定所用的轮次。
	Rounds int
}

// FreezeTree 冻结以 root 为根的进程树，直到冻结集合覆盖稳定的父子闭包。
//
// 成功时返回 Result。任何失败（枚举错误、冻结被拒绝、根进程变化、
// 超过轮次上限仍未稳定）都会先按反向依赖顺序恢复本次运行冻结的所有
// 实例，再返回错误；已被其他主体冻结的实例不属于本次运行，不会被恢复。
func (c *Controller) FreezeTree(ctx context.Context, root Key) (Result, error) {
	maxRounds := c.MaxRounds
	if maxRounds <= 0 {
		maxRounds = defaultMaxRounds
	}

	var frozen []Key // 本次运行冻结的实例，按冻结顺序
	frozenSet := make(map[Key]struct{})
	external := make(map[Key]struct{}) // 已被其他主体冻结的实例

	rollback := func(cause error) (Result, error) {
		errs := []error{cause}
		for i := len(frozen) - 1; i >= 0; i-- {
			if err := c.Freezer.Thaw(ctx, frozen[i].PID, frozen[i].Start); err != nil {
				errs = append(errs, fmt.Errorf("thaw %+v: %w", frozen[i], err))
			}
		}
		return Result{}, errors.Join(errs...)
	}

	for round := 1; ; round++ {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}

		snap, err := c.Enumerator.Enumerate(ctx)
		if err != nil {
			return rollback(fmt.Errorf("enumerate: %w", err))
		}

		byPID := make(map[int]Process, len(snap))
		children := make(map[int][]int)
		for _, p := range snap {
			byPID[p.PID] = p
			children[p.PPID] = append(children[p.PPID], p.PID)
		}

		rootProc, ok := byPID[root.PID]
		if !ok || rootProc.Start != root.Start {
			return rollback(fmt.Errorf("%w: pid %d", ErrRootChanged, root.PID))
		}

		// 剔除已退出或 PID 被复用的冻结实例：它们不再是威胁，
		// 也不应再被恢复（Thaw 的启动代校验会拒绝复用后的 PID）。
		alive := frozen[:0]
		for _, key := range frozen {
			if p, ok := byPID[key.PID]; ok && p.Start == key.Start {
				alive = append(alive, key)
			} else {
				delete(frozenSet, key)
			}
		}
		frozen = alive

		// 自根向下计算父子闭包，BFS 保证父先于子。
		var closure []Process
		inClosure := map[int]bool{root.PID: true}
		queue := []int{root.PID}
		for len(queue) > 0 {
			pid := queue[0]
			queue = queue[1:]
			closure = append(closure, byPID[pid])
			kids := children[pid]
			sort.Ints(kids)
			for _, cp := range kids {
				if !inClosure[cp] {
					inClosure[cp] = true
					queue = append(queue, cp)
				}
			}
		}

		var pending []Process
		for _, p := range closure {
			key := Key{PID: p.PID, Start: p.Start}
			if _, ok := frozenSet[key]; ok {
				continue
			}
			if _, ok := external[key]; ok {
				continue
			}
			pending = append(pending, p)
		}

		// 整轮快照中闭包完全被冻结集合覆盖：所有树内进程均已冻结，
		// 冻结进程无法再派生，闭包达到稳定。
		if len(pending) == 0 {
			return Result{Frozen: append([]Key(nil), frozen...), Rounds: round}, nil
		}
		if round >= maxRounds {
			return rollback(fmt.Errorf("%w: round %d still has %d unfrozen members",
				ErrIncomplete, round, len(pending)))
		}

		for _, p := range pending {
			key := Key{PID: p.PID, Start: p.Start}
			switch err := c.Freezer.Freeze(ctx, p.PID, p.Start); {
			case err == nil:
				frozen = append(frozen, key)
				frozenSet[key] = struct{}{}
			case errors.Is(err, ErrAlreadyFrozen):
				external[key] = struct{}{}
			default:
				return rollback(fmt.Errorf("freeze pid %d: %w", p.PID, err))
			}
		}
	}
}
