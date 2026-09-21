package pubsub

import (
	"context"
	"sync"
	"sync/atomic"
)

// offerOutcome 是单次入队的结果。
type offerOutcome int

const (
	offerDelivered offerOutcome = iota // 成功进入队列
	offerDropped                       // 队列满且策略为丢弃
	offerCanceled                      // 阻塞期间 ctx 取消或订阅已关闭
)

// subscription 是内部订阅状态。
//
// 同步模型：
//   - ch 本身即有界队列（缓冲容量 = cap），背压由 channel 原生提供；
//   - 每个订阅者的入队发生在 Publish 的独立 goroutine 中，因此一个慢订阅者
//     只会延迟本次 Publish 返回，不会使其他订阅者无声丢消息；
//   - closeCtx 在取消订阅/关闭代理时取消，用于唤醒 PolicyBlock 的阻塞发送；
//   - offerWG 跟踪所有在途入队；ch 的关闭在唯一的收尾 goroutine 中、于
//     offerWG.Wait() 之后进行，保证绝不会“向已关闭 channel 发送”。
type subscription[T any] struct {
	broker *Broker[T]

	mu       sync.Mutex
	closing  bool
	closeCtx context.Context
	cancel   context.CancelCauseFunc
	offerWG  sync.WaitGroup // 所有在途 offer

	policy Policy
	onDrop OnDropFunc[T]
	ch     chan T

	handler    HandlerFunc[T]
	handlerCfg handlerOptions[T]

	dropped atomic.Int64 // 累计显式丢弃数
	unsub   sync.Once
}

// offer 尝试把消息放入该订阅者的有界队列。watcher 在入队成功或终止时停止。
func (s *subscription[T]) offer(ctx context.Context, msg T) offerOutcome {
	if err := ctx.Err(); err != nil {
		return offerCanceled
	}

	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return offerCanceled
	}
	s.offerWG.Add(1)
	s.mu.Unlock()
	defer s.offerWG.Done()

	if s.policy == PolicyDropNewest {
		// 非阻塞入队：满则显式丢弃，绝不拖慢发布者。
		select {
		case s.ch <- msg:
			return offerDelivered
		default:
			s.dropped.Add(1)
			if s.onDrop != nil {
				s.onDrop(msg)
			}
			return offerDropped
		}
	}

	// PolicyBlock：阻塞直到入队、调用方 ctx 取消、或订阅被关闭。
	// 即使两个 case 同时就绪而随机走了取消分支，消息若已进入 ch，收尾
	// goroutine 也会等 offerWG 后再关闭 ch，因此不会发生发送到已关闭 channel。
	select {
	case s.ch <- msg:
		return offerDelivered
	case <-ctx.Done():
		return offerCanceled
	case <-s.closeCtx.Done():
		return offerCanceled
	}
}

// beginTeardown 标记停止接收新消息并唤醒阻塞发布者，幂等。
// 唯一的 channel 关闭由收尾 goroutine 在所有在途 offer 结束后执行。
func (s *subscription[T]) beginTeardown() {
	s.unsub.Do(func() {
		b := s.broker
		s.mu.Lock()
		s.closing = true
		s.cancel(errSubscriptionClosing)
		s.mu.Unlock()

		b.mu.Lock()
		delete(b.subs, s)
		b.mu.Unlock()

		b.workers.Add(1)
		go func() {
			defer b.workers.Done()
			s.offerWG.Wait()
			close(s.ch) // 唯一关闭点：此后不存在任何发送者
		}()
	})
}

// Subscription 是返回给用户的订阅句柄。
type Subscription[T any] struct {
	s *subscription[T]
}

// C 返回只读接收 channel。订阅者队列中已承诺的消息排空后 channel 被关闭。
func (sub *Subscription[T]) C() <-chan T { return sub.s.ch }

// Dropped 返回因 PolicyDropNewest 而显式丢弃的消息总数。
func (sub *Subscription[T]) Dropped() int64 { return sub.s.dropped.Load() }

// Unsubscribe 取消订阅。幂等：重复调用不会 panic。
//
// 已进入该订阅者有界队列的消息仍保留在 C() 中并在最终关闭 channel 前可被
// 消费；调用方应继续 range C() 直到其关闭。
func (sub *Subscription[T]) Unsubscribe() {
	sub.s.beginTeardown()
}
