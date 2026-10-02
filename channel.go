// Package coroutine 提供仿 Go Channel 语义的协程同步原语，
// 以及一个无锁（lock-free）有界 MPMC 环形队列。
//
// Channel 通过显式的 FIFO 等待队列管理阻塞的协程：
// 通道满时发送协程挂起，通道空时接收协程挂起；
// 对端操作到来时精确唤醒队首的匹配协程（直接交接数据，不经过缓冲）；
// Close 会广播唤醒所有等待中的协程，读端收到 EOF（ok=false），写端 panic。
package coroutine

import "sync"

// recvWaiter 表示一个挂起的接收协程。
type recvWaiter struct {
	ready chan struct{} // 被 close 即表示被唤醒
	val   any           // 唤醒方交接的值
	ok    bool          // true=正常接收，false=通道关闭（EOF）
}

// sendWaiter 表示一个挂起的发送协程。
type sendWaiter struct {
	ready     chan struct{}
	val       any  // 待发送的值，由接收方取走
	closedSig bool // 因 Close 被唤醒时为 true（对应 Go 的 panic 语义）
}

// Channel 是仿 Go channel 语义的协程通道，支持带缓冲与无缓冲两种模式。
// 零值不可用，请使用 New 构造。
type Channel struct {
	mu     sync.Mutex
	buf    []any // 环形缓冲，容量 0 即无缓冲通道
	head   int   // 队首下标
	count  int   // 缓冲中元素个数
	closed bool

	recvQ []*recvWaiter // 挂起的接收协程（FIFO）
	sendQ []*sendWaiter // 挂起的发送协程（FIFO）
}

// New 创建一个 Channel。capacity == 0 时为无缓冲（rendezvous）通道。
func New(capacity int) *Channel {
	if capacity < 0 {
		panic("coroutine: negative channel capacity")
	}
	return &Channel{buf: make([]any, capacity)}
}

// Send 向通道发送一个值。
// 若有挂起的接收协程，直接交接并精确唤醒队首接收者；
// 否则若缓冲未满则写入缓冲立即返回；
// 否则挂起当前协程，直到被某个接收操作唤醒。
// 通道已关闭时 panic（与 Go 一致）。
func (c *Channel) Send(v any) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		panic("send on closed channel")
	}
	// 有等待的接收者：直接交接，精确唤醒队首。
	if len(c.recvQ) > 0 {
		w := c.recvQ[0]
		c.recvQ = c.recvQ[1:]
		w.val = v
		w.ok = true
		close(w.ready)
		c.mu.Unlock()
		return
	}
	// 缓冲未满：入队后立即返回。
	if c.count < len(c.buf) {
		c.buf[(c.head+c.count)%len(c.buf)] = v
		c.count++
		c.mu.Unlock()
		return
	}
	// 缓冲已满（或无缓冲）：挂起当前协程。
	w := &sendWaiter{ready: make(chan struct{}), val: v}
	c.sendQ = append(c.sendQ, w)
	c.mu.Unlock()

	<-w.ready // 挂起，直到接收方取走值或通道被关闭
	if w.closedSig {
		panic("send on closed channel")
	}
}

// Recv 从通道接收一个值。
// 返回 (value, true) 表示正常接收；(nil, false) 表示通道已关闭且缓冲耗尽（EOF）。
// 缓冲为空且无挂起发送者时，挂起当前协程，直到被某个发送操作或 Close 唤醒。
func (c *Channel) Recv() (any, bool) {
	c.mu.Lock()
	// 缓冲非空：取队首；若有挂起的发送者，把它的值补进缓冲并精确唤醒它。
	if c.count > 0 {
		v := c.buf[c.head]
		c.buf[c.head] = nil
		c.head = (c.head + 1) % len(c.buf)
		c.count--
		if len(c.sendQ) > 0 {
			w := c.sendQ[0]
			c.sendQ = c.sendQ[1:]
			c.buf[(c.head+c.count)%len(c.buf)] = w.val
			c.count++
			close(w.ready)
		}
		c.mu.Unlock()
		return v, true
	}
	// 无缓冲通道且已有挂起的发送者：直接交接。
	if len(c.sendQ) > 0 {
		w := c.sendQ[0]
		c.sendQ = c.sendQ[1:]
		v := w.val
		close(w.ready)
		c.mu.Unlock()
		return v, true
	}
	// 已关闭且缓冲耗尽：EOF。
	if c.closed {
		c.mu.Unlock()
		return nil, false
	}
	// 挂起当前协程。
	w := &recvWaiter{ready: make(chan struct{})}
	c.recvQ = append(c.recvQ, w)
	c.mu.Unlock()

	<-w.ready // 挂起，直到发送方交接值或通道被关闭
	return w.val, w.ok
}

// Close 关闭通道，广播唤醒所有挂起的协程：
// 等待中的接收者收到 EOF（ok=false），等待中的发送者 panic。
// 重复 Close 会 panic（与 Go 一致）。
func (c *Channel) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		panic("close of closed channel")
	}
	c.closed = true
	for _, w := range c.recvQ {
		w.ok = false // EOF
		close(w.ready)
	}
	c.recvQ = nil
	for _, w := range c.sendQ {
		w.closedSig = true
		close(w.ready)
	}
	c.sendQ = nil
	c.mu.Unlock()
}

// Closed 报告通道是否已关闭。
func (c *Channel) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
