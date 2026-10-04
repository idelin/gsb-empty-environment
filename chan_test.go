package workchan

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// recvResult 收集一次 Recv 调用的完整结果。
type recvResult struct {
	val int
	ok  bool
	err error
}

func recvAsync(c *Chan[int], ctx context.Context) <-chan recvResult {
	done := make(chan recvResult, 1)
	go func() {
		v, ok, err := c.Recv(ctx)
		done <- recvResult{v, ok, err}
	}()
	return done
}

func sendAsync(c *Chan[int], ctx context.Context, v int) <-chan error {
	done := make(chan error, 1)
	go func() { done <- c.Send(ctx, v) }()
	return done
}

// TestCloseIdempotentAndSendRejected：Close 幂等，关闭后 Send 被拒绝。
func TestCloseIdempotentAndSendRejected(t *testing.T) {
	c := New[int](1)
	c.Close()
	c.Close()
	c.Close()
	if err := c.Send(context.Background(), 1); err != ErrClosed {
		t.Fatalf("Send after Close = %v, want ErrClosed", err)
	}
	if _, ok, err := c.Recv(context.Background()); ok || err != nil {
		t.Fatalf("Recv on empty closed channel = ok=%v err=%v, want closed flag", ok, err)
	}
}

// TestDrainAfterClose：关闭后缓冲区已有值按 FIFO 顺序取完，然后返回关闭标记。
func TestDrainAfterClose(t *testing.T) {
	c := New[int](3)
	for _, v := range []int{10, 20, 30} {
		if err := c.Send(context.Background(), v); err != nil {
			t.Fatalf("Send(%d) = %v", v, err)
		}
	}
	c.Close()
	for _, want := range []int{10, 20, 30} {
		v, ok, err := c.Recv(context.Background())
		if !ok || err != nil || v != want {
			t.Fatalf("Recv = (%d,%v,%v), want (%d,true,nil)", v, ok, err, want)
		}
	}
	if _, ok, err := c.Recv(context.Background()); ok || err != nil {
		t.Fatalf("Recv after drain = ok=%v err=%v, want closed flag", ok, err)
	}
}

// TestCloseWakesBlockedSenders：容量 0，两个发送者入队阻塞后 Close，
// 两者都以 ErrClosed 唤醒，且值永远不会被接收。
func TestCloseWakesBlockedSenders(t *testing.T) {
	c := New[int](0)
	enqueued := make(chan struct{}, 2)
	c.hookSendEnqueued = func() { enqueued <- struct{}{} }

	d1 := sendAsync(c, context.Background(), 1)
	<-enqueued // 屏障：发送者 1 已入队
	d2 := sendAsync(c, context.Background(), 2)
	<-enqueued // 屏障：发送者 2 已入队

	c.Close()
	for i, d := range []<-chan error{d1, d2} {
		if err := <-d; err != ErrClosed {
			t.Fatalf("blocked sender %d got %v, want ErrClosed", i, err)
		}
	}
	// 被拒绝的值不得出现在通道中。
	if _, ok, err := c.Recv(context.Background()); ok || err != nil {
		t.Fatalf("Recv after close = ok=%v err=%v, want closed flag (rejected values leaked)", ok, err)
	}
}

// TestCloseWakesBlockedReceivers：阻塞的接收者在 Close 时被唤醒并返回关闭标记。
func TestCloseWakesBlockedReceivers(t *testing.T) {
	c := New[int](0)
	enqueued := make(chan struct{}, 1)
	c.hookRecvEnqueued = func() { enqueued <- struct{}{} }

	done := recvAsync(c, context.Background())
	<-enqueued // 屏障：接收者已入队
	c.Close()
	r := <-done
	if r.ok || r.err != nil {
		t.Fatalf("blocked receiver got (%d,%v,%v), want closed flag", r.val, r.ok, r.err)
	}
}

// TestRendezvousCap0：容量 0 时发送在配对前不完成；接收到达后双方同时完成。
func TestRendezvousCap0(t *testing.T) {
	c := New[int](0)
	enqueued := make(chan struct{}, 1)
	c.hookSendEnqueued = func() { enqueued <- struct{}{} }

	sd := sendAsync(c, context.Background(), 42)
	<-enqueued // 屏障：发送者已入队，正阻塞等待配对
	select {
	case err := <-sd:
		t.Fatalf("Send completed (%v) without a paired receiver", err)
	default:
	}
	rd := recvAsync(c, context.Background())
	r := <-rd
	if !r.ok || r.err != nil || r.val != 42 {
		t.Fatalf("Recv = %+v, want (42,true,nil)", r)
	}
	if err := <-sd; err != nil {
		t.Fatalf("Send after rendezvous = %v, want nil", err)
	}
}

// TestSenderFIFOOrderCap0：按受控次序入队的发送者按 FIFO 完成交付。
func TestSenderFIFOOrderCap0(t *testing.T) {
	c := New[int](0)
	enqueued := make(chan struct{}, 3)
	c.hookSendEnqueued = func() { enqueued <- struct{}{} }

	d1 := sendAsync(c, context.Background(), 1)
	<-enqueued
	d2 := sendAsync(c, context.Background(), 2)
	<-enqueued
	d3 := sendAsync(c, context.Background(), 3)
	<-enqueued

	for i, want := range []int{1, 2, 3} {
		v, ok, err := c.Recv(context.Background())
		if !ok || err != nil || v != want {
			t.Fatalf("Recv #%d = (%d,%v,%v), want (%d,true,nil)", i, v, ok, err, want)
		}
	}
	for i, d := range []<-chan error{d1, d2, d3} {
		if err := <-d; err != nil {
			t.Fatalf("sender %d = %v, want nil", i, err)
		}
	}
}

// TestCancelBlockedSendCap0：取消阻塞发送者后，其值不得被后续接收者看到；
// 后续的配对不受影响。
func TestCancelBlockedSendCap0(t *testing.T) {
	c := New[int](0)
	enqueued := make(chan struct{}, 2)
	c.hookSendEnqueued = func() { enqueued <- struct{}{} }

	ctx, cancel := context.WithCancel(context.Background())
	d1 := sendAsync(c, ctx, 99)
	<-enqueued // 屏障：值 99 已入队阻塞
	cancel()
	if err := <-d1; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Send = %v, want context.Canceled", err)
	}

	// 新配对必须交付新值，且永远收不到 99。
	d2 := sendAsync(c, context.Background(), 7)
	<-enqueued
	v, ok, err := c.Recv(context.Background())
	if !ok || err != nil || v != 7 {
		t.Fatalf("Recv = (%d,%v,%v), want (7,true,nil)", v, ok, err)
	}
	if err := <-d2; err != nil {
		t.Fatalf("second Send = %v, want nil", err)
	}
	c.Close()
	if _, ok, err := c.Recv(context.Background()); ok || err != nil {
		t.Fatalf("canceled value 99 was delivered after cancellation")
	}
}

// TestCancelBlockedRecvCap0：取消阻塞接收者不得吞掉任何值；发送者随后
// 与新的接收者正常配对。
func TestCancelBlockedRecvCap0(t *testing.T) {
	c := New[int](0)
	recvEnqueued := make(chan struct{}, 1)
	sendEnqueued := make(chan struct{}, 1)
	c.hookRecvEnqueued = func() { recvEnqueued <- struct{}{} }
	c.hookSendEnqueued = func() { sendEnqueued <- struct{}{} }

	ctx, cancel := context.WithCancel(context.Background())
	rd := recvAsync(c, ctx)
	<-recvEnqueued // 屏障：接收者已入队
	cancel()
	if r := <-rd; r.ok || !errors.Is(r.err, context.Canceled) {
		t.Fatalf("canceled Recv = %+v, want context.Canceled without value", r)
	}

	// 发送者到来时应跳过已取消的接收者并阻塞等待。
	sd := sendAsync(c, context.Background(), 5)
	<-sendEnqueued
	select {
	case err := <-sd:
		t.Fatalf("Send completed (%v) against a canceled receiver", err)
	default:
	}
	r := <-recvAsync(c, context.Background())
	if !r.ok || r.err != nil || r.val != 5 {
		t.Fatalf("Recv = %+v, want (5,true,nil)", r)
	}
	if err := <-sd; err != nil {
		t.Fatalf("Send = %v, want nil", err)
	}
}

// TestCancelBlockedRecvBuffered：容量 1，阻塞接收者取消后不得吞值：
// 随后发送的值必须完好地交给下一个接收者。
func TestCancelBlockedRecvBuffered(t *testing.T) {
	c := New[int](1)
	enqueued := make(chan struct{}, 1)
	c.hookRecvEnqueued = func() { enqueued <- struct{}{} }

	ctx, cancel := context.WithCancel(context.Background())
	rd := recvAsync(c, ctx)
	<-enqueued // 屏障：接收者已入队
	cancel()
	if r := <-rd; r.ok || !errors.Is(r.err, context.Canceled) {
		t.Fatalf("canceled Recv = %+v, want context.Canceled without value", r)
	}
	// 发送者应跳过已取消的接收者，将值放入缓冲区。
	if err := c.Send(context.Background(), 11); err != nil {
		t.Fatalf("Send = %v", err)
	}
	v, ok, err := c.Recv(context.Background())
	if !ok || err != nil || v != 11 {
		t.Fatalf("Recv = (%d,%v,%v), want (11,true,nil): value swallowed by canceled receiver", v, ok, err)
	}
}

// TestBlockedSenderUnblockedByRecv：容量 1，缓冲区满时发送者阻塞，
// 接收腾出位置后发送者的值按序进入缓冲区。
func TestBlockedSenderUnblockedByRecv(t *testing.T) {
	c := New[int](1)
	if err := c.Send(context.Background(), 1); err != nil {
		t.Fatalf("Send = %v", err)
	}
	enqueued := make(chan struct{}, 1)
	c.hookSendEnqueued = func() { enqueued <- struct{}{} }
	sd := sendAsync(c, context.Background(), 2)
	<-enqueued // 屏障：值 2 的发送者已阻塞（缓冲区满）

	v, ok, err := c.Recv(context.Background())
	if !ok || err != nil || v != 1 {
		t.Fatalf("Recv = (%d,%v,%v), want (1,true,nil)", v, ok, err)
	}
	if err := <-sd; err != nil {
		t.Fatalf("blocked Send = %v, want nil after buffer slot freed", err)
	}
	v, ok, err = c.Recv(context.Background())
	if !ok || err != nil || v != 2 {
		t.Fatalf("Recv = (%d,%v,%v), want (2,true,nil): FIFO order violated", v, ok, err)
	}
}

// TestCancelRaceUniqueOutcome：取消与交付竞争的压力测试。
// 每个值有唯一 ID；结束后关闭并排空通道，验证：
//   - 每个被接收的值都来自一个返回 nil 的 Send（无幻影、无取消后交付）；
//   - 每个值至多被接收一次（无重复）；
//   - 每个返回 nil 的 Send 的值最终都被收到（无丢失）。
func TestCancelRaceUniqueOutcome(t *testing.T) {
	for _, capacity := range []int{0, 1, 3} {
		t.Run(fmt.Sprintf("cap=%d", capacity), func(t *testing.T) {
			c := New[int](capacity)
			const nSend = 60
			const nRecv = 60

			var mu sync.Mutex
			sentOK := map[int]bool{}
			received := map[int]bool{}

			var wg sync.WaitGroup
			for i := 0; i < nSend; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					ctx := context.Background()
					if id%3 != 0 { // 2/3 的发送者带立即取消的 ctx
						var cancel context.CancelFunc
						ctx, cancel = context.WithCancel(context.Background())
						cancel()
					}
					err := c.Send(ctx, id)
					mu.Lock()
					if err == nil {
						sentOK[id] = true
					}
					mu.Unlock()
				}(i)
			}
			for i := 0; i < nRecv; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					ctx := context.Background()
					if i%3 != 0 {
						var cancel context.CancelFunc
						ctx, cancel = context.WithCancel(context.Background())
						cancel()
					}
					v, ok, err := c.Recv(ctx)
					mu.Lock()
					if err == nil && ok {
						if received[v] {
							t.Errorf("value %d received twice", v)
						}
						received[v] = true
					}
					mu.Unlock()
				}(i)
			}
			// 看门狗：后台 ctx 的收发可能因配对不足而阻塞，
			// 超时后由 Close 唤醒全部等待者，保证测试必然终止。
			allDone := make(chan struct{})
			go func() { wg.Wait(); close(allDone) }()
			select {
			case <-allDone:
			case <-time.After(1 * time.Second):
			}
			c.Close()
			wg.Wait()
			// 排空剩余缓冲。
			for {
				v, ok, err := c.Recv(context.Background())
				if err != nil {
					t.Fatalf("drain Recv err = %v", err)
				}
				if !ok {
					break
				}
				if received[v] {
					t.Fatalf("value %d received twice (during drain)", v)
				}
				received[v] = true
			}
			for v := range received {
				if !sentOK[v] {
					t.Fatalf("value %d received but its Send did not return nil", v)
				}
			}
			for v := range sentOK {
				if !received[v] {
					t.Fatalf("value %d sent successfully but never received (lost)", v)
				}
			}
		})
	}
}
