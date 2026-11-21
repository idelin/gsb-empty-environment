package handover

import "fmt"

// Executor 是宿主句柄表需要实现的最小操作面。
// 每个方法必须对单槽修改具备原子语义；ParSwap 由宿主保证整批原子可见。
type Executor interface {
	LiveView
	Swap(a, b int) error
	ParSwap(pairs [][2]int) error
	Move(src, dst int) error
	SetCloexec(slot int, on bool) error
	Close(slot int) error
}

// VerifyExpectations 在执行任何安装操作前，原子比较所有相关槽的代次与
// 对象身份（以及空槽要求）。任一不一致即返回 ErrVerifyFailed，且无副作用。
func (p *Plan) VerifyExpectations(live LiveView) error {
	for slot, want := range p.Expect {
		h, ok := live.HandleAt(slot)
		if want.Empty {
			if ok {
				return fmt.Errorf("%w: slot %d expected empty, holds %s",
					ErrVerifyFailed, slot, h.Object)
			}
			continue
		}
		if !ok {
			return fmt.Errorf("%w: slot %d expected %s, now empty",
				ErrVerifyFailed, slot, want.Object)
		}
		if h.Gen != want.Gen {
			return fmt.Errorf("%w: slot %d gen %d != expected %d",
				ErrVerifyFailed, slot, h.Gen, want.Gen)
		}
		if h.Object != want.Object {
			return fmt.Errorf("%w: slot %d object %s != expected %s",
				ErrVerifyFailed, slot, h.Object, want.Object)
		}
	}
	return nil
}

// Commit 先做原子核对，再逐步执行计划；任一步失败都会把表恢复到核对时的
// 快照，整次交接撤销，返回 ErrCommitFailed。
func (p *Plan) Commit(exec Executor) error {
	return p.CommitWithFault(exec, -1)
}

// CommitWithFault 与 Commit 相同，但允许在执行到第 failAt 个安装操作前注入
// 一次失败，用于演练“安装中途失败必须整体撤销”。failAt < 0 表示不注入。
func (p *Plan) CommitWithFault(exec Executor, failAt int) error {
	if err := p.VerifyExpectations(exec); err != nil {
		return err
	}

	snapshot, snapErr := snapshotExecutor(exec, p)
	if snapErr != nil {
		return snapErr
	}

	for step, op := range p.Ops {
		if step == failAt {
			restoreExecutor(exec, snapshot)
			return fmt.Errorf("%w: injected fault before %s", ErrCommitFailed, op)
		}
		if err := applyOp(exec, op); err != nil {
			restoreExecutor(exec, snapshot)
			return fmt.Errorf("%w: step %d %s: %v", ErrCommitFailed, step, op, err)
		}
	}
	return nil
}

func applyOp(exec Executor, op Op) error {
	switch op.Kind {
	case OpSwap:
		return exec.Swap(op.A, op.B)
	case OpParSwap:
		return exec.ParSwap(op.Pairs)
	case OpLoad:
		return exec.Move(op.B, op.A)
	case OpMove:
		return exec.Move(op.A, op.B)
	case OpStore:
		return exec.Move(op.A, op.B)
	case OpSetCloexec:
		return exec.SetCloexec(op.A, op.On)
	case OpClose:
		return exec.Close(op.A)
	case OpDropTemp, OpBarrier:
		return nil
	default:
		return fmt.Errorf("unknown op %s", op.Kind)
	}
}

type slotSnapshot struct {
	handle  Handle
	present bool
}

func snapshotExecutor(exec Executor, p *Plan) (map[int]slotSnapshot, error) {
	snapshot := make(map[int]slotSnapshot, len(p.Expect))
	for slot := range p.Expect {
		if h, ok := exec.HandleAt(slot); ok {
			snapshot[slot] = slotSnapshot{handle: h, present: true}
		} else {
			snapshot[slot] = slotSnapshot{present: false}
		}
	}
	return snapshot, nil
}

func restoreExecutor(exec Executor, snapshot map[int]slotSnapshot) {
	mt, ok := exec.(*MemTable)
	if !ok {
		// 参考实现之外的执行器必须自行提供事务回滚；此处无法代为恢复。
		return
	}
	mt.restore(snapshot)
}
