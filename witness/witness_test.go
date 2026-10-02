package witness

import (
	"testing"
	"time"
)

// fakeClock 可手动推进的时钟。
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestSingleWriterAtAnyMoment(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	w := NewCore()
	w.SetClock(clk.now)

	// dc1 先申请,成功,epoch=1
	l1, ok := w.Acquire("dc1", time.Second)
	if !ok || l1.Epoch != 1 || l1.Holder != "dc1" {
		t.Fatalf("dc1 首次申请失败: %+v ok=%v", l1, ok)
	}
	// 租约有效期内 dc2 申请,必须被拒绝
	if _, ok := w.Acquire("dc2", time.Second); ok {
		t.Fatal("租约有效期内 dc2 不应拿到租约")
	}
	// 租约过期后 dc2 申请成功,epoch 递增
	clk.advance(2 * time.Second)
	l2, ok := w.Acquire("dc2", time.Second)
	if !ok || l2.Epoch != 2 || l2.Holder != "dc2" {
		t.Fatalf("过期后 dc2 应拿到新租约: %+v ok=%v", l2, ok)
	}
}

func TestRenewAndFencing(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	w := NewCore()
	w.SetClock(clk.now)

	l1, _ := w.Acquire("dc1", time.Second)
	// 持有者本人续租成功
	r, ok := w.Renew("dc1", l1.Epoch, time.Second)
	if !ok || r.Epoch != l1.Epoch {
		t.Fatalf("合法续租应成功: %+v ok=%v", r, ok)
	}
	// epoch 错误(伪造凭证)续租被拒
	if _, ok := w.Renew("dc1", l1.Epoch+99, time.Second); ok {
		t.Fatal("错误 epoch 的续租必须被拒绝")
	}
	// 他人冒名续租被拒
	if _, ok := w.Renew("dc2", l1.Epoch, time.Second); ok {
		t.Fatal("他人冒名续租必须被拒绝")
	}
	// 租约易主后,旧持有者的旧 epoch 续租被拒 —— "凭证已失效"
	clk.advance(2 * time.Second)
	if _, ok := w.Acquire("dc2", time.Second); !ok {
		t.Fatal("dc2 应接管租约")
	}
	if _, ok := w.Renew("dc1", l1.Epoch, time.Second); ok {
		t.Fatal("旧主机房的失效凭证续租必须被拒绝")
	}
}

func TestEpochMonotonic(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	w := NewCore()
	w.SetClock(clk.now)
	var last uint64
	for i := 0; i < 10; i++ {
		l, ok := w.Acquire("dc", time.Second)
		if !ok || l.Epoch <= last {
			t.Fatalf("epoch 必须严格递增: last=%d got=%+v", last, l)
		}
		last = l.Epoch
		clk.advance(2 * time.Second)
	}
}
