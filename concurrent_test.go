package netsec

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentNavigationAndCalls 在 -race 下并发驱动导航与回调调用,
// 验证代次复核不会产生数据竞争,并且任何被拒绝的调用都不会触达原生函数。
func TestConcurrentNavigationAndCalls(t *testing.T) {
	table := NewCapabilityTable()
	app := NewTupleOrigin("https", "app.example.com", "")
	var nativeCalls int64
	fn := NativeFunc(func(args []any) (any, error) {
		atomic.AddInt64(&nativeCalls, 1)
		return nil, nil
	})
	if err := table.Grant("host.race", app, FrameMain, fn); err != nil {
		t.Fatalf("grant failed: %v", err)
	}

	frame := NewFrame("frame-race", FrameMain)
	frame.Navigate(NavigationRequest{URL: "https://app.example.com/start", Initiator: app})

	var wg sync.WaitGroup

	// 导航线程:在授权来源与无权 data: 文档之间反复跳转并推进代次。
	wg.Add(1)
	go func() {
		defer wg.Done()
		navs := []NavigationRequest{
			{URL: "https://app.example.com/page", Initiator: app},
			{URL: "data:text/html,blocked", Initiator: app},
			{URL: "about:blank", Initiator: app},
		}
		for i := 0; i < 1000; i++ {
			frame.Navigate(navs[i%len(navs)])
		}
	}()

	// 调用线程:持续绑定并调用,拒绝必须只表现为错误,绝不触达原生函数。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				cb, err := table.Bind("host.race", frame)
				if err != nil {
					if !errors.Is(err, ErrOpaqueOrigin) && !errors.Is(err, ErrCapabilityNotRegistered) {
						t.Errorf("unexpected bind error: %v", err)
						return
					}
					continue
				}
				if _, err := cb.Call(frame, nil); err != nil {
					switch {
					case errors.Is(err, ErrStaleGeneration),
						errors.Is(err, ErrOpaqueOrigin),
						errors.Is(err, ErrCapabilityNotRegistered):
					default:
						t.Errorf("unexpected call error: %v", err)
						return
					}
				}
			}
		}()
	}

	// 直接 Invoke 线程:同样只允许成功或明确的哨兵错误。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			if _, err := table.Invoke("host.race", frame, nil); err != nil {
				switch {
				case errors.Is(err, ErrOpaqueOrigin),
					errors.Is(err, ErrCapabilityNotRegistered),
					errors.Is(err, ErrStaleGeneration):
				default:
					t.Errorf("unexpected invoke error: %v", err)
					return
				}
			}
		}
	}()

	// 授权管理线程:并发撤销/重授,验证表内部锁的正确性。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			table.Revoke("host.race", app, FrameMain)
			_ = table.Grant("host.race", app, FrameMain, fn)
		}
	}()

	wg.Wait()

	// 确定性收尾不变量:把帧固定导航到 data: 不透明文档后,
	// 并发冲击绑定与调用,原生函数计数器必须纹丝不动。
	frame.Navigate(NavigationRequest{URL: "data:text/html,locked", Initiator: app})
	before := atomic.LoadInt64(&nativeCalls)
	var denyWG sync.WaitGroup
	for w := 0; w < 8; w++ {
		denyWG.Add(1)
		go func() {
			defer denyWG.Done()
			for i := 0; i < 250; i++ {
				if _, err := table.Invoke("host.race", frame, nil); !errors.Is(err, ErrOpaqueOrigin) {
					t.Errorf("opaque invoke must return ErrOpaqueOrigin, got %v", err)
					return
				}
				if _, err := table.Bind("host.race", frame); !errors.Is(err, ErrOpaqueOrigin) {
					t.Errorf("opaque bind must return ErrOpaqueOrigin, got %v", err)
					return
				}
			}
		}()
	}
	denyWG.Wait()

	if got := atomic.LoadInt64(&nativeCalls); got != before {
		t.Fatalf("opaque-frame flood must not reach native function: calls increased from %d to %d", before, got)
	}
}
