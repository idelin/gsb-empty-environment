package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrVerificationFailed 用于 errors.Is 判断：片段完整解压但校验未通过。
var ErrVerificationFailed = errors.New("fragment verification failed")

// StepResult 是解压器单步产出。所有输出只进入宿主提供的隔离缓冲，
// Cost 声明这一步实际消耗的工作量。Done=true 表示片段已完整解压。
type StepResult struct {
	Output []byte
	Cost   Cost
	Done   bool
}

// Decompressor 是可注入的步骤式解压器。宿主每个解压尝试创建一个实例，
// 失败或成功后都会调用 Close 释放底层资源。
type Decompressor interface {
	Next(ctx context.Context) (StepResult, error)
	Close() error
}

// DecompressorFactory 按压缩片段构造解压器，便于注入测试替身或真实格式实现。
type DecompressorFactory interface {
	NewDecompressor(fragment []byte) (Decompressor, error)
}

// DecompressorFunc 把普通函数适配成 DecompressorFactory。
type DecompressorFunc func(fragment []byte) (Decompressor, error)

func (f DecompressorFunc) NewDecompressor(fragment []byte) (Decompressor, error) {
	return f(fragment)
}

// Verifier 对完整解压出的隔离输出做片段级校验（摘要 / 签名 / 魔数等）。
// 只有校验通过，输出才会被提交给调用方；否则丢弃且永不外泄。
type Verifier interface {
	Verify(ctx context.Context, fragment, isolatedOutput []byte) error
}

// VerifierFunc 把普通函数适配成 Verifier。
type VerifierFunc func(ctx context.Context, fragment, isolatedOutput []byte) error

func (f VerifierFunc) Verify(ctx context.Context, fragment, isolatedOutput []byte) error {
	return f(ctx, fragment, isolatedOutput)
}

// Fragment 是一次解压调用的输入。同一 RequestID 下重复提交相同 Payload
// 即“失败片段重试”，宿主沿用同一请求预算。
type Fragment struct {
	RequestID string
	Payload   []byte
}

// Processor 是供解压宿主调用的预算组件门面。并发安全。
type Processor struct {
	mu       sync.Mutex
	budget   *Budget
	factory  DecompressorFactory
	verifier Verifier
}

// NewProcessor 组装预算、解压器工厂与校验器。
func NewProcessor(budget *Budget, factory DecompressorFactory, verifier Verifier) *Processor {
	if budget == nil || factory == nil || verifier == nil {
		panic("netsec: budget, factory and verifier are required")
	}
	return &Processor{budget: budget, factory: factory, verifier: verifier}
}

// Budget 暴露请求级预算，供宿主查询累计用量。
func (p *Processor) Budget() *Budget { return p.budget }

// Process 执行一次（可能是重试的）片段解压：
//
//  1. 绑定授权请求：相同 RequestID 沿用累计预算，新 RequestID 才清零；
//  2. 逐步驱动注入的解压器，每一步先预检预算、再按实际成本记账；
//  3. 全部输出写入仅本地可见的隔离缓冲，调用方此时拿不到任何字节；
//  4. 解压完成后做片段校验，通过才返回输出副本，失败则连同缓冲一起丢弃。
//
// 任何一步失败（解压器错误、预算耗尽、校验失败、上下文取消）都不会提交输出。
func (p *Processor) Process(ctx context.Context, frag Fragment) (output []byte, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if frag.RequestID == "" {
		return nil, errors.New("netsec: fragment missing RequestID")
	}
	attempt, _ := p.budget.BindRequest(frag.RequestID)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 预检前置：预算已耗尽时既不分配解压器也不驱动任何步骤，
	// 使失败重试的资源与 CPU 消耗在预算用尽后立即归零。
	if err := p.budget.Exhausted(attempt); err != nil {
		return nil, err
	}

	dec, err := p.factory.NewDecompressor(frag.Payload)
	if err != nil {
		return nil, fmt.Errorf("netsec: create decompressor: %w", err)
	}
	defer func() { _ = dec.Close() }()

	isolated := make([]byte, 0)

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// 循环内复检，覆盖单步记账后刚好触顶的情况。
		if err := p.budget.Exhausted(attempt); err != nil {
			return nil, err
		}

		step, stepErr := dec.Next(ctx)
		if stepErr != nil {
			return nil, fmt.Errorf("netsec: decompression step failed: %w", stepErr)
		}

		// 先记账后落盘：记账被拒则隔离缓冲连同本步输出一起丢弃。
		if err := p.budget.Charge(step.Cost, attempt); err != nil {
			return nil, err
		}
		isolated = append(isolated, step.Output...)

		if step.Done {
			break
		}
	}

	if err := p.verifier.Verify(ctx, frag.Payload, isolated); err != nil {
		// 校验失败：隔离缓冲不返回，由 GC 回收；预算仍保留累计值。
		return nil, fmt.Errorf("%w: %v", ErrVerificationFailed, err)
	}

	// 校验通过才提交；返回拷贝，调用方无法改写宿主内部缓冲。
	committed := make([]byte, len(isolated))
	copy(committed, isolated)
	return committed, nil
}
