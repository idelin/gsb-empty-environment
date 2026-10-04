package workchan

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 线性化检查器：枚举所有满足"响应先于调用"实时先后关系的操作顺序，
// 与抽象通道模型（有界 FIFO 队列 + 关闭标志 + 容量 0 的 rendezvous）
// 逐一比对。任一顺序被模型接受，则该历史可线性化。

type opKind int

const (
	opSend opKind = iota
	opRecv
	opClose
)

// opRes 是操作结果的分类。
type opRes int

const (
	resOK       opRes = iota // Send 交付成功 / Recv 取到值 / Close 完成
	resClosed                // Send 返回 ErrClosed / Recv 返回关闭标记
	resCanceled              // 返回 ctx.Err()
)

type op struct {
	kind    opKind
	val     int // Send 的值或 Recv 收到的值（唯一 ID）
	res     opRes
	inv, rs int64 // 调用与响应的逻辑时间戳
}

// model 是抽象通道状态。
type model struct {
	capacity    int
	queue       []int
	closed      bool
	hasInflight bool // 容量 0：已配对的发送，等待相邻的接收
	inflight    int
}

// apply 尝试在模型上执行一个操作，返回新状态与是否合法。
func (m model) apply(o op) (model, bool) {
	// rendezvous 配对后，唯一合法的下一步是取走该值的接收。
	if m.hasInflight {
		if o.kind == opRecv && o.res == resOK && o.val == m.inflight {
			m.hasInflight = false
			return m, true
		}
		return m, false
	}
	switch o.kind {
	case opSend:
		switch o.res {
		case resCanceled:
			return m, true // 取消的发送是空操作
		case resClosed:
			return m, m.closed
		}
		if m.closed {
			return m, false
		}
		if m.capacity == 0 {
			m.hasInflight = true
			m.inflight = o.val
			return m, true
		}
		if len(m.queue) >= m.capacity {
			return m, false
		}
		// 复制切片，避免分支间共享底层数组。
		nq := make([]int, len(m.queue)+1)
		copy(nq, m.queue)
		nq[len(m.queue)] = o.val
		m.queue = nq
		return m, true
	case opRecv:
		switch o.res {
		case resCanceled:
			return m, true // 取消的接收是空操作
		case resClosed:
			return m, m.closed && len(m.queue) == 0
		}
		if len(m.queue) == 0 || m.queue[0] != o.val {
			return m, false
		}
		m.queue = m.queue[1:]
		return m, true
	case opClose:
		m.closed = true
		return m, true
	}
	return m, false
}

// linearizable 用回溯枚举所有满足实时先后关系的顺序。
func linearizable(ops []op, capacity int) bool {
	n := len(ops)
	placed := make([]bool, n)
	var search func(model, int) bool
	search = func(m model, k int) bool {
		if k == n {
			return !m.hasInflight // 成功的 rendezvous 发送必须有配对接收
		}
		for i := 0; i < n; i++ {
			if placed[i] {
				continue
			}
			// 实时先后：凡响应早于 ops[i] 调用的操作必须先放置。
			ready := true
			for j := 0; j < n; j++ {
				if !placed[j] && ops[j].rs < ops[i].inv {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			next, ok := m.apply(ops[i])
			if !ok {
				continue
			}
			placed[i] = true
			if search(next, k+1) {
				return true
			}
			placed[i] = false
		}
		return false
	}
	return search(model{capacity: capacity}, 0)
}

func classifySend(err error) opRes {
	switch {
	case err == nil:
		return resOK
	case errors.Is(err, ErrClosed):
		return resClosed
	default:
		return resCanceled
	}
}

// TestLinearizabilityCheckerSanity：检查器自身必须接受合法历史、拒绝非法历史。
func TestLinearizabilityCheckerSanity(t *testing.T) {
	mk := func(kind opKind, val int, res opRes, inv, rs int64) op {
		return op{kind: kind, val: val, res: res, inv: inv, rs: rs}
	}
	cases := []struct {
		name string
		cap  int
		ops  []op
		want bool
	}{
		{"cap0配对", 0, []op{
			mk(opSend, 1, resOK, 1, 4), mk(opRecv, 1, resOK, 2, 3),
		}, true},
		{"cap0发送成功但无配对接收", 0, []op{
			mk(opSend, 1, resOK, 1, 2),
		}, false},
		{"收到从未发送的值", 1, []op{
			mk(opRecv, 9, resOK, 1, 2),
		}, false},
		{"cap1两次成功发送无接收", 1, []op{
			mk(opSend, 1, resOK, 1, 2), mk(opSend, 2, resOK, 3, 4),
		}, false},
		{"cap1并发两发送可线性化", 1, []op{
			mk(opSend, 1, resOK, 1, 4), mk(opSend, 2, resOK, 2, 5),
			mk(opRecv, 1, resOK, 3, 6),
		}, true},
		{"FIFO顺序违反", 2, []op{
			mk(opSend, 1, resOK, 1, 2), mk(opSend, 2, resOK, 3, 4),
			mk(opRecv, 2, resOK, 5, 6),
		}, false},
		{"取消发送的值被收到", 0, []op{
			mk(opSend, 1, resCanceled, 1, 4), mk(opRecv, 1, resOK, 2, 3),
		}, false},
		{"取消发送是空操作", 1, []op{
			mk(opSend, 1, resCanceled, 1, 2),
		}, true},
		{"未关闭却收到关闭标记", 1, []op{
			mk(opRecv, 0, resClosed, 1, 2),
		}, false},
		{"关闭后排空再收到关闭标记", 2, []op{
			mk(opSend, 1, resOK, 1, 2), mk(opClose, 0, resOK, 3, 4),
			mk(opRecv, 1, resOK, 5, 6), mk(opRecv, 0, resClosed, 7, 8),
		}, true},
		{"关闭后发送成功", 1, []op{
			mk(opClose, 0, resOK, 1, 2), mk(opSend, 1, resOK, 3, 4),
		}, false},
		{"关闭后被拒发送", 1, []op{
			mk(opClose, 0, resOK, 1, 2), mk(opSend, 1, resClosed, 3, 4),
		}, true},
	}
	for _, tc := range cases {
		if got := linearizable(tc.ops, tc.cap); got != tc.want {
			t.Errorf("%s: linearizable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestLinearizabilityRandom：随机并发工作负载，记录历史并验证可线性化。
// 每个值带唯一 ID，取消的发送若其值被收到、或值丢失/重复，检查器都会拒绝。
func TestLinearizabilityRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for trial := 0; trial < 150; trial++ {
		capacity := rng.Intn(3) // 0, 1, 2
		c := New[int](capacity)
		var clock atomic.Int64
		var mu sync.Mutex
		var ops []op

		nSend := 2 + rng.Intn(2) // 2..3
		nRecv := 2 + rng.Intn(2)
		withClose := rng.Intn(2) == 0

		var wg sync.WaitGroup
		for i := 0; i < nSend; i++ {
			useCancel := rng.Intn(2) == 0
			delay := time.Duration(rng.Intn(3)) * time.Millisecond
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				ctx := context.Background()
				if useCancel {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(context.Background())
					time.AfterFunc(delay, cancel)
					defer cancel()
				}
				val := trial*100 + id
				inv := clock.Add(1)
				err := c.Send(ctx, val)
				rs := clock.Add(1)
				mu.Lock()
				ops = append(ops, op{kind: opSend, val: val, res: classifySend(err), inv: inv, rs: rs})
				mu.Unlock()
			}(i)
		}
		for i := 0; i < nRecv; i++ {
			useCancel := rng.Intn(2) == 0
			delay := time.Duration(rng.Intn(3)) * time.Millisecond
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx := context.Background()
				if useCancel {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(context.Background())
					time.AfterFunc(delay, cancel)
					defer cancel()
				}
				inv := clock.Add(1)
				v, ok, err := c.Recv(ctx)
				rs := clock.Add(1)
				res := resOK
				switch {
				case err != nil:
					res = resCanceled
				case !ok:
					res = resClosed
				}
				mu.Lock()
				ops = append(ops, op{kind: opRecv, val: v, res: res, inv: inv, rs: rs})
				mu.Unlock()
			}()
		}
		if withClose {
			delay := time.Duration(rng.Intn(3)) * time.Millisecond
			wg.Add(1)
			go func() {
				defer wg.Done()
				time.Sleep(delay)
				inv := clock.Add(1)
				c.Close()
				rs := clock.Add(1)
				mu.Lock()
				ops = append(ops, op{kind: opClose, inv: inv, rs: rs})
				mu.Unlock()
			}()
		}
		// 看门狗：保证所有阻塞操作最终被 Close 唤醒。
		allDone := make(chan struct{})
		go func() { wg.Wait(); close(allDone) }()
		select {
		case <-allDone:
		case <-time.After(500 * time.Millisecond):
		}
		// 最终关闭（幂等）也要记入历史，否则排空阶段的关闭标记无法解释。
		inv := clock.Add(1)
		c.Close()
		rs := clock.Add(1)
		mu.Lock()
		ops = append(ops, op{kind: opClose, inv: inv, rs: rs})
		mu.Unlock()
		wg.Wait()

		// 排空剩余缓冲，作为历史的最后一段（顺序确定）。
		for {
			inv := clock.Add(1)
			v, ok, err := c.Recv(context.Background())
			rs := clock.Add(1)
			if err != nil {
				t.Fatalf("trial %d: drain err = %v", trial, err)
			}
			res := resOK
			if !ok {
				res = resClosed
			}
			ops = append(ops, op{kind: opRecv, val: v, res: res, inv: inv, rs: rs})
			if !ok {
				break
			}
		}

		if !linearizable(ops, capacity) {
			t.Fatalf("trial %d (cap=%d): history not linearizable:\n%+v", trial, capacity, ops)
		}
	}
}
