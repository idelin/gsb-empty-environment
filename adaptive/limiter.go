package adaptive

import "sync/atomic"

// Limiter 基于动态阈值的并发准入限制器（负载感知）。
// 许可计数使用原子操作；释放函数幂等，重复调用不会泄漏许可。
type Limiter struct {
	ctrl     *Controller
	inflight int64 // atomic
}

// NewLimiter 创建绑定到控制器的限制器。
func NewLimiter(ctrl *Controller) *Limiter { return &Limiter{ctrl: ctrl} }

// Allow 非抢占式判断当前是否允许准入。
func (l *Limiter) Allow() bool {
	return atomic.LoadInt64(&l.inflight) < int64(l.ctrl.Threshold())
}

// Acquire 尝试获取一个许可。成功返回释放函数（幂等）与 true；
// 达到动态阈值时返回 (nil, false)。
func (l *Limiter) Acquire() (release func(), ok bool) {
	for {
		cur := atomic.LoadInt64(&l.inflight)
		if cur >= int64(l.ctrl.Threshold()) {
			return nil, false
		}
		if atomic.CompareAndSwapInt64(&l.inflight, cur, cur+1) {
			var once int32
			return func() {
				if atomic.CompareAndSwapInt32(&once, 0, 1) {
					atomic.AddInt64(&l.inflight, -1)
				}
			}, true
		}
	}
}

// InFlight 当前在途许可数。
func (l *Limiter) InFlight() int64 { return atomic.LoadInt64(&l.inflight) }
