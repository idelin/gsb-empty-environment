package vecpool

// Pipeline 是向量化查询流水线：数据以 Chunk 为单位依次流经
// 所有算子。每经过一个算子，输入 Chunk 立即归还对象池，
// 输出 Chunk 从池中借出——同一时刻流水线中存活的 Chunk
// 数量恒为 3（调用方持有的源块 + 一进一出），因此池容量
// 只需 >= 3 即可支撑任意长度的流水线。
type Pipeline struct {
	pool *Pool
	ops  []Operator
}

// NewPipeline 用给定对象池和算子序列构建流水线。
// 算子按顺序依次执行，ops 数量即流水线级数。
func NewPipeline(pool *Pool, ops ...Operator) *Pipeline {
	return &Pipeline{pool: pool, ops: ops}
}

// Execute 让 src 流经全部算子，最终结果写回 src 并交还给调用方。
// 调用方使用完毕后应调用 pool.Put(src) 归还。
// 整个执行过程不发生任何堆分配。
func (pl *Pipeline) Execute(src *Chunk) *Chunk {
	cur := src
	for _, op := range pl.ops {
		next := pl.pool.Get()
		if next == nil {
			// 池耗尽：按约定不分配堆内存，直接失败。
			// 正常配置下（容量 >= 2）不会走到这里。
			return nil
		}
		op.Process(cur, next)
		if cur != src {
			// 中间 Chunk 用毕即归还，供后续算子复用。
			pl.pool.Put(cur)
		}
		cur = next
	}
	// 将最终结果拷回 src，保持"输入块即输出块"的调用约定，
	// 同时归还流水线末端借出的 Chunk。
	if cur != src {
		src.N = cur.N
		copy(src.Data, cur.Data)
		pl.pool.Put(cur)
	}
	return src
}

// OpCount 返回流水线的算子级数。
func (pl *Pipeline) OpCount() int { return len(pl.ops) }
