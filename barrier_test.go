package repartition

import (
	"context"
	"errors"
	"sort"
	"sync"
)

var errInjectedCrash = errors.New("注入的写盘崩溃")

// memSink 模拟跨“重启”存活的幂等下游：重复调用 Publish 是安全的，
// 但每条记录只“生效”一次。attempts 记录全部下发尝试，
// effective 记录真正生效的记录，用来验证不重复发布。
type memSink struct {
	mu        sync.Mutex
	published map[string]Record
	order     []string
	attempts  int
}

func newMemSink() *memSink {
	return &memSink{published: map[string]Record{}}
}

func (s *memSink) Publish(_ context.Context, r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	id := r.id()
	if _, ok := s.published[id]; ok {
		return nil // 幂等下游：重复下发不产生重复副作用
	}
	s.published[id] = r
	s.order = append(s.order, id)
	return nil
}

func (s *memSink) attemptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

func (s *memSink) ids() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]string(nil), s.order...)
	sort.Strings(out)
	return out
}

func testConfig() Config {
	return Config{
		OldPartitions: []int{0, 1},
		NewPartitions: []int{10, 11},
		CutOffsets:    map[int]int64{0: 2, 1: 1},
	}
}
