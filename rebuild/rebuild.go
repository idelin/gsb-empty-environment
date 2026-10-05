package rebuild

import (
	"fmt"
	"sync"
)

// Service 把读请求路由到当前主索引；切换前后旧索引持续对外服务。
type Service struct {
	mu      sync.RWMutex
	primary *Index
}

func NewService(primary *Index) *Service { return &Service{primary: primary} }

func (s *Service) Get(key string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.primary.Get(key)
}

func (s *Service) Primary() *Index {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.primary
}

func (s *Service) switchTo(idx *Index) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.primary = idx
}

// Rebuilder 在旧索引继续服务的同时，从快照构建新索引并追赶增量。
type Rebuilder struct {
	src    *Source
	oldIdx *Index
	newIdx *Index

	freeze uint64

	mu      sync.Mutex
	pending map[string]uint64 // key -> 新索引尚未确认的最高变更版本
}

// Begin 注册变更监听后冻结版本并装载快照。监听先于快照注册，保证
// 快照与变更流之间没有空隙；已被快照覆盖的变更在确认检查时自然过滤。
func Begin(src *Source, oldIdx *Index) *Rebuilder {
	r := &Rebuilder{
		src:     src,
		oldIdx:  oldIdx,
		newIdx:  NewIndex("new"),
		pending: make(map[string]uint64),
	}
	src.OnChange(func(ev Event) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if cur, ok := r.pending[ev.Key]; !ok || ev.Seq > cur {
			r.pending[ev.Key] = ev.Seq
		}
	})
	data, freeze := src.Snapshot()
	r.freeze = freeze
	r.newIdx.LoadSnapshot(data, freeze)
	return r
}

func (r *Rebuilder) NewIndex() *Index      { return r.newIdx }
func (r *Rebuilder) FreezeVersion() uint64 { return r.freeze }

// CatchUp 把冻结版本之后的全部变更重放到新索引。deliver 可对事件做
// 乱序、重发或（模拟故障时）丢弃；Index.Apply 的按键版本消重保证
// 重发与乱序下结果正确。可反复调用直到无未确认变更。
func (r *Rebuilder) CatchUp(deliver func([]Event) []Event) {
	events := r.src.EventsSince(r.freeze)
	if deliver != nil {
		events = deliver(events)
	}
	for _, ev := range events {
		r.newIdx.Apply(ev)
	}
}

// Unacked 返回尚未在新索引确认的变更。键 k 的版本 v 在新索引持有
// 版本 >= v（含墓碑）时视为已确认。
func (r *Rebuilder) Unacked() map[string]uint64 {
	r.mu.Lock()
	pending := make(map[string]uint64, len(r.pending))
	for k, v := range r.pending {
		pending[k] = v
	}
	r.mu.Unlock()
	unacked := make(map[string]uint64)
	for k, v := range pending {
		if r.newIdx.keyVersion(k) < v {
			unacked[k] = v
		}
	}
	return unacked
}

// TrySwitch 仅在同时满足以下条件时把新索引提升为主索引：
//  1. 新索引水位已追到冻结版本；
//  2. 所有未确认变更均已处理；
//  3. 以源库全扫描为参考的校验通过。
// 无论切换与否都返回新旧索引的水位证据。
func (r *Rebuilder) TrySwitch(svc *Service) Evidence {
	ev := Evidence{
		FreezeVersion: r.freeze,
		OldWatermark:  r.oldIdx.Watermark(),
		NewWatermark:  r.newIdx.Watermark(),
		SourceVersion: r.src.Version(),
		Unacked:       r.Unacked(),
	}
	switch {
	case r.newIdx.Watermark() < r.freeze:
		ev.Reason = fmt.Sprintf("new index watermark %d below freeze version %d",
			r.newIdx.Watermark(), r.freeze)
	case len(ev.Unacked) > 0:
		ev.Reason = fmt.Sprintf("%d unacknowledged change(s) still pending", len(ev.Unacked))
	default:
		ev.Validated = true
		ev.Validation = Validate(r.src, r.newIdx)
		if !ev.Validation.OK() {
			ev.Reason = "full-scan validation failed"
		} else {
			svc.switchTo(r.newIdx)
			ev.Switched = true
			ev.Reason = "caught up to freeze, all changes acknowledged, validation passed"
		}
	}
	return ev
}
