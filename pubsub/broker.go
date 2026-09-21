// Package pubsub 实现一个进程内的泛型发布订阅代理。
//
// 每个订阅者拥有独立的有界环形队列。队列满时慢消费者策略可选：
//   - PolicyBlock：发布者阻塞背压，但每个订阅者的入队在各自 goroutine
//     中并行发生，因此一个慢订阅者只会拖慢 Publish 的完成，不会让其他
//     订阅者无声丢消息；
//   - PolicyDropNewest：显式丢弃新消息（非阻塞），丢弃数可统计、可回调。
//
// 并发安全：Publish / Subscribe / Unsubscribe / Close 可从任意 goroutine
// 并发调用。用户接收 channel 只会由该订阅者自己的泵 goroutine 写入，并在
// 队列排空后关闭，因此绝不会发生“向已关闭 channel 发送”。重复取消订阅
// 与重复关闭均为幂等操作。
package pubsub

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed 在代理关闭后调用 Publish / Subscribe 时返回。
var ErrClosed = errors.New("pubsub: broker is closed")

// errSubscriptionClosing 在取消订阅时唤醒阻塞在该订阅者上的发布者。
var errSubscriptionClosing = errors.New("pubsub: subscription is closing")

// Policy 决定订阅者队列已满时的行为。
type Policy int

const (
	// PolicyBlock：队列满时阻塞发布者，直到腾出空间或上下文取消。
	PolicyBlock Policy = iota
	// PolicyDropNewest：队列满时立即丢弃本次新消息，不阻塞发布者。
	PolicyDropNewest
)

// PublishResult 汇总一次发布在所有当前订阅者上的结果。
type PublishResult struct {
	// Delivered 成功进入订阅者队列的份数（每订阅者计一次）。
	Delivered int
	// Dropped 因 PolicyDropNewest 队列满而显式丢弃的份数。
	Dropped int
}

// OnDropFunc 在消息因队列满被显式丢弃时回调。
// 回调在订阅者锁外执行，不得阻塞过久。
type OnDropFunc[T any] func(msg T)

type subOptions[T any] struct {
	policy     Policy
	cap        int
	onDrop     OnDropFunc[T]
	handler    HandlerFunc[T]
	handlerCfg handlerOptions[T]
}

// SubOption 配置订阅。
type SubOption[T any] func(*subOptions[T])

// WithCapacity 设置有界队列容量，必须 >= 1，默认 16。
func WithCapacity[T any](n int) SubOption[T] {
	return func(o *subOptions[T]) { o.cap = n }
}

// WithPolicy 设置慢消费者策略，默认 PolicyBlock。
func WithPolicy[T any](p Policy) SubOption[T] {
	return func(o *subOptions[T]) { o.policy = p }
}

// WithOnDrop 设置 PolicyDropNewest 丢弃消息时的回调。
func WithOnDrop[T any](f OnDropFunc[T]) SubOption[T] {
	return func(o *subOptions[T]) { o.onDrop = f }
}

// Broker 是泛型发布订阅代理。零值不可用，使用 New 创建。
type Broker[T any] struct {
	mu        sync.RWMutex
	subs      map[*subscription[T]]struct{}
	closed    bool
	committed func()         // 仅测试使用：Publish 登记在途后回调
	pubsWG    sync.WaitGroup // 跟踪在途发布（已取过快照、正在入队）
	workers   sync.WaitGroup // 跟踪泵与任务 worker goroutine
	opts      options
}

type options struct {
	clock Clock
}

// Option 配置代理。
type Option func(*options)

// WithClock 注入时钟（任务退避重试使用），默认使用系统时钟。
func WithClock(c Clock) Option {
	return func(o *options) { o.clock = c }
}

// New 创建代理。
func New[T any](opts ...Option) *Broker[T] {
	b := &Broker[T]{subs: make(map[*subscription[T]]struct{})}
	b.opts.clock = realClock{}
	for _, opt := range opts {
		opt(&b.opts)
	}
	return b
}

// Subscribe 创建一个独立有界队列的订阅。订阅者必须持续消费 C() 返回的
// channel；在 PolicyBlock 下，不消费会对发布者形成背压。
//
// 取消订阅见 Subscription.Unsubscribe。
func (b *Broker[T]) Subscribe(opts ...SubOption[T]) (*Subscription[T], error) {
	cfg := subOptions[T]{policy: PolicyBlock, cap: 16}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.cap < 1 {
		return nil, errors.New("pubsub: capacity must be >= 1")
	}

	sub := &subscription[T]{
		broker:     b,
		policy:     cfg.policy,
		onDrop:     cfg.onDrop,
		ch:         make(chan T, cfg.cap), // channel 本身即有界队列
		handler:    cfg.handler,
		handlerCfg: cfg.handlerCfg,
	}
	sub.closeCtx, sub.cancel = context.WithCancelCause(context.Background())

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, ErrClosed
	}
	b.subs[sub] = struct{}{}
	if cfg.handler != nil {
		b.workers.Add(1)
		go runWorker(b, sub, cfg.handler, cfg.handlerCfg)
	}
	b.mu.Unlock()

	return &Subscription[T]{s: sub}, nil
}

// Publish 将消息扇出给当前所有订阅者。
//
// 每个订阅者的入队在独立 goroutine 中并行进行，Publish 等待全部完成：
//   - PolicyBlock 的慢订阅者不会使其他订阅者丢消息，只会延迟本次返回；
//   - ctx 取消时，尚未入队的阻塞订阅者中止等待并返回 ctx.Err()
//     （已入队的消息仍保证会被排空交付）。
func (b *Broker[T]) Publish(ctx context.Context, msg T) (PublishResult, error) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return PublishResult{}, ErrClosed
	}
	snapshot := make([]*subscription[T], 0, len(b.subs))
	for s := range b.subs {
		snapshot = append(snapshot, s)
	}
	b.pubsWG.Add(1)
	if b.committed != nil {
		b.committed()
	}
	b.mu.RUnlock()
	defer b.pubsWG.Done()

	var (
		wg       sync.WaitGroup
		resMu    sync.Mutex
		res      PublishResult
		firstErr error
	)
	wg.Add(len(snapshot))
	for _, s := range snapshot {
		go func(s *subscription[T]) {
			defer wg.Done()
			outcome := s.offer(ctx, msg)
			resMu.Lock()
			switch outcome {
			case offerDelivered:
				res.Delivered++
			case offerDropped:
				res.Dropped++
			default:
				if firstErr == nil {
					firstErr = ctx.Err()
					if firstErr == nil {
						firstErr = context.Canceled
					}
				}
			}
			resMu.Unlock()
		}(s)
	}
	wg.Wait()
	return res, firstErr
}

// Close 关闭代理。语义：
//   - Close 返回前，等待所有在途 Publish 完成入队（已“承诺”的消息）；
//   - 随后等待每个订阅者把自己的有界队列排空到用户 channel 并关闭之；
//   - 关闭后新的 Publish / Subscribe 返回 ErrClosed；
//   - 重复关闭是幂等的；
//   - ctx 超时/取消时调用方可提前返回，后台排空仍会完成。
func (b *Broker[T]) Close(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	snapshot := make([]*subscription[T], 0, len(b.subs))
	for s := range b.subs {
		snapshot = append(snapshot, s)
	}
	b.mu.Unlock()

	// 等待在途发布全部完成入队，以及所有泵/worker 排空退出。
	done := make(chan struct{})
	go func() {
		b.pubsWG.Wait()
		for _, s := range snapshot {
			s.beginTeardown() // 停止接收；已入队消息保留，随后关闭 channel
		}
		b.workers.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
