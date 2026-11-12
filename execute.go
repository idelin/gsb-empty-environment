package netsec

import "fmt"

// Driver 由 Go 宿主实现，把复位计划落实到具体设备。
type Driver interface {
	// Reset 复位指定功能块，返回错误表示该步失败。
	Reset(idx int) error
	// Commit 在全部复位成功后调用，解除隔离并把设备交付新租户。
	Commit() error
	// Abort 在任一步失败时调用，设备必须保持（或恢复）隔离状态，
	// 不得交付新租户。
	Abort()
}

// StepError 描述复位序列中某一步的失败。
type StepError struct {
	Step  int // 在执行顺序中的下标
	Block int // 失败的功能块下标
	Err   error
}

func (e *StepError) Error() string {
	return fmt.Sprintf("netsec: reset step %d (block %d) failed: %v", e.Step, e.Block, e.Err)
}

func (e *StepError) Unwrap() error { return e.Err }

// Execute 按 plan.Order 顺序驱动复位。任一步失败立即中止后续步骤，
// 调用 Abort 保持隔离，并返回 *StepError；全部成功后才调用 Commit
// 解除隔离。Commit 失败同样触发 Abort，设备保持隔离。
func Execute(plan Plan, d Driver) error {
	for step, idx := range plan.Order {
		if err := d.Reset(idx); err != nil {
			d.Abort()
			return &StepError{Step: step, Block: idx, Err: err}
		}
	}
	if err := d.Commit(); err != nil {
		d.Abort()
		return fmt.Errorf("netsec: commit failed, device kept isolated: %w", err)
	}
	return nil
}
