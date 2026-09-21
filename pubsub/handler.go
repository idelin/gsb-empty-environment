package pubsub

import (
	"context"
	"time"
)

// HandlerFunc 是注入的消息处理任务。返回 error 表示处理失败，将按配置重试。
type HandlerFunc[T any] func(ctx context.Context, msg T) error

// OnFailedFunc 在消息重试用尽仍失败时回调（可用于死信记录）。
type OnFailedFunc[T any] func(msg T, err error)

type handlerOptions[T any] struct {
	maxAttempts int
	backoff     func(attempt int) time.Duration // attempt 为即将进行的重试序号（从 1 开始）
	onFailed    OnFailedFunc[T]
}

// HandlerOption 配置注入的任务处理器。
type HandlerOption[T any] func(*handlerOptions[T])

// WithMaxAttempts 设置最大尝试次数（含首次），默认 1。
func WithMaxAttempts[T any](n int) HandlerOption[T] {
	return func(o *handlerOptions[T]) { o.maxAttempts = n }
}

// WithBackoff 设置第 attempt 次重试前的等待时长（attempt 从 1 开始）。
func WithBackoff[T any](f func(attempt int) time.Duration) HandlerOption[T] {
	return func(o *handlerOptions[T]) { o.backoff = f }
}

// WithOnFailed 设置重试用尽后的失败回调。
func WithOnFailed[T any](f OnFailedFunc[T]) HandlerOption[T] {
	return func(o *handlerOptions[T]) { o.onFailed = f }
}

// WithHandler 为订阅附加一个注入的处理任务。代理会启动独立 worker
// goroutine 消费 C()：handler 返回错误时按 maxAttempts 重试，重试间隔由
// backoff 决定（时钟可通过 WithClock 注入）；用尽后调用 onFailed。
//
// 未使用 WithHandler 时，调用方自行从 Subscription.C() 消费。
func WithHandler[T any](fn HandlerFunc[T], hopts ...HandlerOption[T]) SubOption[T] {
	cfg := handlerOptions[T]{maxAttempts: 1}
	for _, o := range hopts {
		o(&cfg)
	}
	if cfg.maxAttempts < 1 {
		cfg.maxAttempts = 1
	}
	return func(o *subOptions[T]) {
		o.handler = fn
		o.handlerCfg = cfg
	}
}

// runWorker 消费订阅 channel 并执行注入的任务，含失败重试/退避。
func runWorker[T any](b *Broker[T], s *subscription[T], fn HandlerFunc[T], cfg handlerOptions[T]) {
	defer b.workers.Done()
	for msg := range s.ch {
		var err error
		for attempt := 1; attempt <= cfg.maxAttempts; attempt++ {
			err = fn(context.Background(), msg)
			if err == nil {
				break
			}
			if attempt == cfg.maxAttempts || cfg.backoff == nil {
				break
			}
			timer := b.opts.clock.NewTimer(cfg.backoff(attempt))
			<-timer.C()
		}
		if err != nil && cfg.onFailed != nil {
			cfg.onFailed(msg, err)
		}
	}
}
