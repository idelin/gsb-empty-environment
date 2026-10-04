// Package causal 实现因果投递缓冲：每条消息携带发送者已观察到的各源序号
// （向量时钟），只有当全部前驱都已交付后才交付当前消息。
//
// 投递条件（标准因果广播）：
//   - 对发送者自身 s：m.Clock[s] == delivered[s] + 1（保证同源 FIFO）；
//   - 对其他每个源 p：m.Clock[p] <= delivered[p]。
//
// 待决索引：缓冲不为每条新消息重扫全部待决消息，而是维护
// 「缺失前驱 (源, 序号) -> 等待它的消息集合」的倒排索引。
// 每当某源序号前进一步，只精确唤醒挂在该序号上的等待者。
package causal

// Message 是一条待因果投递的消息。
type Message struct {
	ID      string           // 全局唯一消息 ID
	Sender  string           // 发送者（源）标识
	Clock   map[string]uint64 // 发送者观察到的各源序号（含自身，自身即本条序号）
	Payload interface{}
}

// Gap 描述一个精确的缺失前驱区间 [From, To]（闭区间）。
type Gap struct {
	Sender string
	From   uint64 // 第一个缺失序号（含）
	To     uint64 // 最后一个缺失序号（含）
}

// depKey 是待决索引的键：某源的某个序号。
type depKey struct {
	sender string
	seq    uint64
}

// pendingEntry 是一条被挂起的消息及其仍未满足的前驱集合。
type pendingEntry struct {
	msg     Message
	missing map[depKey]struct{}
}

// Buffer 是因果投递缓冲。非并发安全，调用方需自行加锁。
type Buffer struct {
	delivered map[string]uint64          // 每个源已连续交付到的最大序号
	pending   map[string]*pendingEntry   // 消息 ID -> 待决项
	waiters   map[depKey]map[string]struct{} // 缺失前驱 -> 等待它的消息 ID 集合
}

// New 创建一个空的因果投递缓冲。
func New() *Buffer {
	return &Buffer{
		delivered: make(map[string]uint64),
		pending:   make(map[string]*pendingEntry),
		waiters:   make(map[depKey]map[string]struct{}),
	}
}

// Add 接收一条消息。
//
// 返回值：
//   - ready：因本次调用而变得可交付的消息（含本消息及被级联唤醒者），
//     按因果序排列；若无则为 nil。
//   - gaps：若本消息被挂起，返回精确的缺失前驱区间；否则为 nil。
//
// 重复消息（已交付或已在待决队列中）被静默忽略，不会触发任何唤醒。
func (b *Buffer) Add(m Message) (ready []Message, gaps []Gap) {
	// 已交付过的重复消息：自身序号不超过该源已交付序号。
	if m.Clock[m.Sender] <= b.delivered[m.Sender] {
		return nil, nil
	}
	// 已在待决队列中的重复消息。
	if _, dup := b.pending[m.ID]; dup {
		return nil, nil
	}

	missing, gaps := b.missingDeps(m)
	if len(missing) > 0 {
		// 挂起：把每个缺失前驱登记进倒排索引。
		entry := &pendingEntry{msg: m, missing: missing}
		b.pending[m.ID] = entry
		for k := range missing {
			set := b.waiters[k]
			if set == nil {
				set = make(map[string]struct{})
				b.waiters[k] = set
			}
			set[m.ID] = struct{}{}
		}
		return nil, gaps
	}

	b.deliver(m, &ready)
	return ready, nil
}

// missingDeps 计算 m 的全部缺失前驱。
// 返回每个缺失序号的键集合（用于索引）以及合并后的缺口区间（用于上报）。
func (b *Buffer) missingDeps(m Message) (map[depKey]struct{}, []Gap) {
	missing := make(map[depKey]struct{})
	var gaps []Gap
	for src, clock := range m.Clock {
		// need 是 m 交付前 src 必须已交付到的最大序号。
		// 对发送者自身，本条消息占据 clock 位，故前驱只到 clock-1。
		need := clock
		if src == m.Sender {
			if clock == 0 {
				continue
			}
			need = clock - 1
		}
		have := b.delivered[src]
		if need <= have {
			continue
		}
		for seq := have + 1; seq <= need; seq++ {
			missing[depKey{src, seq}] = struct{}{}
		}
		gaps = append(gaps, Gap{Sender: src, From: have + 1, To: need})
	}
	// 排序保证输出确定性。
	for i := 1; i < len(gaps); i++ {
		for j := i; j > 0 && gaps[j].Sender < gaps[j-1].Sender; j-- {
			gaps[j], gaps[j-1] = gaps[j-1], gaps[j]
		}
	}
	return missing, gaps
}

// deliver 交付 m，并沿待决索引级联唤醒因此满足条件的待决消息。
// 只访问键 (m.Sender, 新交付序号) 上的等待者，绝不全量重扫。
func (b *Buffer) deliver(m Message, ready *[]Message) {
	b.delivered[m.Sender] = m.Clock[m.Sender]
	*ready = append(*ready, m)

	key := depKey{m.Sender, m.Clock[m.Sender]}
	ids := b.waiters[key]
	if len(ids) == 0 {
		return
	}
	delete(b.waiters, key)
	for id := range ids {
		entry := b.pending[id]
		if entry == nil {
			continue
		}
		delete(entry.missing, key)
		if len(entry.missing) == 0 {
			delete(b.pending, id)
			b.deliver(entry.msg, ready)
		}
	}
}

// Delivered 返回源 src 已连续交付到的最大序号。
func (b *Buffer) Delivered(src string) uint64 {
	return b.delivered[src]
}

// Pending 返回当前待决消息数量。
func (b *Buffer) Pending() int {
	return len(b.pending)
}
