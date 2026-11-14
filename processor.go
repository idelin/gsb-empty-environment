package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Status 为文件处理结果状态。
type Status string

const (
	StatusSuccess        Status = "SUCCESS"         // 全部明细扣款成功
	StatusPartialFailure Status = "PARTIAL_FAILURE" // 部分明细扣款失败
	StatusFailed         Status = "FAILED"          // 全部明细扣款失败
)

// DetailResult 为单笔明细的处理结果。
type DetailResult struct {
	InstructionID string
	Amount        int64
	OK            bool
	Err           string
}

// Result 为文件处理结果，按文件摘要持久化；重复提交原样返回。
type Result struct {
	Digest     [32]byte
	Header     Header
	Status     Status
	Details    []DetailResult // 去重后的有效明细处理结果
	Duplicates int            // 文件内被去重的重复明细笔数
	Debited    int64          // 实际扣款总额（分）
	Replayed   bool           // 本次调用是否为重放命中
}

// DebitFunc 由调用方注入，执行单笔扣款/入账。返回错误表示该笔失败。
// Processor 保证同一文件摘要下，每条有效指令最多调用一次。
type DebitFunc func(ctx context.Context, d Detail) error

// Store 持久化处理结果，供幂等查询。生产环境应以数据库唯一约束实现。
type Store interface {
	// Load 按文件摘要查询已有结果。
	Load(digest [32]byte) (*Result, bool)
	// Save 保存处理结果。
	Save(digest [32]byte, r *Result) error
}

// MemoryStore 为 Store 的内存实现，适用于单实例部署与测试。
type MemoryStore struct {
	mu      sync.Mutex
	results map[[32]byte]*Result
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{results: make(map[[32]byte]*Result)}
}

func (s *MemoryStore) Load(digest [32]byte) (*Result, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.results[digest]
	return r, ok
}

func (s *MemoryStore) Save(digest [32]byte, r *Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[digest] = r
	return nil
}

// inflight 跟踪正在处理中的文件，使并发重复提交等待同一结果。
type inflight struct {
	done chan struct{}
}

// Processor 为批量付款文件幂等处理器。
type Processor struct {
	keys     KeyStore
	store    Store
	debit    DebitFunc
	mu       sync.Mutex
	inflight map[[32]byte]*inflight
}

func NewProcessor(keys KeyStore, store Store, debit DebitFunc) *Processor {
	return &Processor{
		keys:     keys,
		store:    store,
		debit:    debit,
		inflight: make(map[[32]byte]*inflight),
	}
}

// Process 校验并处理批量付款文件，按文件摘要幂等：
// 同一文件（含重放攻击）重复提交只返回首次处理结果，不会重复扣款。
func (p *Processor) Process(ctx context.Context, f *File) (*Result, error) {
	if err := f.VerifySignature(p.keys); err != nil {
		return nil, err
	}
	digest := f.Digest()

	// 已处理完成：直接返回原结果。
	if r, ok := p.store.Load(digest); ok {
		out := *r
		out.Replayed = true
		return &out, nil
	}

	// 并发去重：同一摘要仅允许一个执行体，其余等待其结果。
	p.mu.Lock()
	if inf, ok := p.inflight[digest]; ok {
		p.mu.Unlock()
		select {
		case <-inf.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if r, ok := p.store.Load(digest); ok {
			out := *r
			out.Replayed = true
			return &out, nil
		}
		return nil, errors.New("netsec: processing finished without stored result")
	}
	inf := &inflight{done: make(chan struct{})}
	p.inflight[digest] = inf
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.inflight, digest)
		p.mu.Unlock()
		close(inf.done)
	}()

	result, err := p.execute(ctx, f, digest)
	if err != nil {
		return nil, err
	}
	if err := p.store.Save(digest, result); err != nil {
		return nil, fmt.Errorf("netsec: save result: %w", err)
	}
	return result, nil
}

func (p *Processor) execute(ctx context.Context, f *File, digest [32]byte) (*Result, error) {
	unique, err := f.Validate()
	if err != nil {
		return nil, err
	}

	r := &Result{
		Digest:     digest,
		Header:     f.Header,
		Details:    make([]DetailResult, 0, len(unique)),
		Duplicates: len(f.Details) - len(unique),
	}
	failures := 0
	for _, d := range unique {
		dr := DetailResult{InstructionID: d.InstructionID, Amount: d.Amount, OK: true}
		if err := p.debit(ctx, d); err != nil {
			dr.OK = false
			dr.Err = err.Error()
			failures++
		} else {
			r.Debited += d.Amount
		}
		r.Details = append(r.Details, dr)
	}
	switch {
	case failures == 0:
		r.Status = StatusSuccess
	case failures == len(unique):
		r.Status = StatusFailed
	default:
		r.Status = StatusPartialFailure
	}
	return r, nil
}
