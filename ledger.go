package gsb

import "fmt"

// VerifyLedger 独立遍历所有存活分支的跨度引用，重建每个物理块应有的
// 引用计数与存活字节数，并与存储内的账本计数器逐一核对。
// 任何不一致（引用泄漏、重复释放、账本漂移）都会报错。
func (r *Repo) VerifyLedger() error {
	expect := make(map[uint64]int)
	for name, v := range r.branches {
		pos := 0
		for _, s := range v.spans {
			b := r.store.blocks[s.Block]
			if b == nil {
				return fmt.Errorf("分支 %q 引用了不存在的物理块 %d", name, s.Block)
			}
			if s.Off < 0 || s.Len <= 0 || s.Off+s.Len > len(b.data) {
				return fmt.Errorf("分支 %q 的跨度越界: %+v 块长 %d", name, s, len(b.data))
			}
			expect[s.Block]++
			pos += s.Len
		}
		if pos != v.length {
			return fmt.Errorf("分支 %q 跨度和 %d 与长度 %d 不符", name, pos, v.length)
		}
	}
	live := 0
	for id, b := range r.store.blocks {
		if b.refs != expect[id] {
			return fmt.Errorf("物理块 %d 引用计数 %d，独立遍历期望 %d", id, b.refs, expect[id])
		}
		if b.refs > 0 {
			live += blockBytes(b)
		}
	}
	if live != r.store.live {
		return fmt.Errorf("存活账本不符: 独立遍历 %d 字节，计数器 %d 字节", live, r.store.live)
	}
	return nil
}
