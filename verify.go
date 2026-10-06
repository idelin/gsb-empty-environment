package logalloc

import "fmt"

// Verify 用持久化切点核对两条核心不变量：
//  1. 连续消费：切点及之前的每个序号都已决定（无未封闭空洞）；
//  2. 不重复分配：分配起点严格大于一切已出现序号。
// 重放阶段另已强制：序号不重复分配、决定不重复、切点逐序推进。
func (l *Log) Verify() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for seq := uint64(1); seq <= l.checkpoint; seq++ {
		e, ok := l.entries[seq]
		if !ok {
			return fmt.Errorf("logalloc: 切点 %d 内序号 %d 缺失，消费不连续", l.checkpoint, seq)
		}
		if e.state == stateReserved {
			return fmt.Errorf("logalloc: 切点 %d 内序号 %d 未决定，消费不连续", l.checkpoint, seq)
		}
	}
	for seq := range l.entries {
		if seq >= l.nextSeq {
			return fmt.Errorf("logalloc: 序号 %d 越过分配起点 %d，存在重复分配风险", seq, l.nextSeq)
		}
	}
	return nil
}
