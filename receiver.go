package aggregator

import "sync"

// Receiver 是幂等接收方：按 Result.ID 去重，
// 同一 ID 无论投递多少次，只生效一次。
// 它是“恢复重发同 ID 不重复计数”的验证基准。
type Receiver struct {
	mu             sync.Mutex
	results        map[string]Result
	totals         map[string]int64
	publishCalls   int
	duplicateCalls int
}

func NewReceiver() *Receiver {
	return &Receiver{
		results: make(map[string]Result),
		totals:  make(map[string]int64),
	}
}

// Publish 实现 Sink。重复 ID 直接确认但不重复计数。
func (r *Receiver) Publish(res Result) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.publishCalls++
	if _, ok := r.results[res.ID]; ok {
		r.duplicateCalls++
		return nil
	}
	r.results[res.ID] = res
	r.totals[res.Key] += res.Value
	return nil
}

// Total 返回某 key 已生效的结果值之和。
func (r *Receiver) Total(key string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.totals[key]
}

// UniqueCount 返回已生效的去重结果数。
func (r *Receiver) UniqueCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.results)
}

// PublishCalls 返回收到的投递总次数（含重复）。
func (r *Receiver) PublishCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.publishCalls
}

// DuplicateCalls 返回被去重掉的重复投递次数。
func (r *Receiver) DuplicateCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.duplicateCalls
}

// Has 判断某 ID 是否已生效。
func (r *Receiver) Has(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.results[id]
	return ok
}
