package repartition

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestRandomMultiCrash 在一条工作负载上连续注入多次随机位置崩溃，
// 验证反复重启后仍然不丢不重、新拓扑不提前可见。
func TestRandomMultiCrash(t *testing.T) {
	wl := buildWorkload()
	dir := filepath.Join(t.TempDir(), "b")
	sink := newMemSink()
	ingested, notified := 0, 0

	// 用确定性伪随机序列保证可复现。
	seed := uint64(0x9e3779b97f4a7c15)
	next := func() uint64 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return seed
	}

	for round := 0; round < 30; round++ {
		fault := FaultConfig{
			AtAppend:   int(next()%40) + 1,
			BeforeSync: next()%2 == 0,
			Err:        errInjectedCrash,
		}
		b, err := Open(dir, testConfig(), WithFault(fault))
		if err != nil {
			if err != errInjectedCrash {
				t.Fatalf("第 %d 轮 Open 失败: %v", round, err)
			}
			continue
		}
		crashed := errors.Is(driveOnce(t, b, wl, sink, &ingested, &notified), errInjectedCrash)
		_ = b.Close()
		if !crashed {
			break
		}
	}

	// 故障耗尽后用无故障实例收尾，确保全部排空。
	runToCompletion(t, dir, wl, sink, nil)

	got, want := sink.ids(), expectedIDs(wl)
	if len(got) != len(want) {
		t.Fatalf("多轮崩溃后有效发布数 = %d，期望 %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("发布集合不一致，得到 %v，期望 %v", got, want)
		}
	}
}

// TestRunWithBlockingNext 验证 Run 循环通过 Next/Ack 也能完整排空。
func TestRunWithBlockingNext(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "b")
	b, err := Open(dir, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	wl := buildWorkload()
	for _, r := range wl.records {
		if err := b.Ingest(r); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	sink := newMemSink()
	go func() { done <- b.Run(ctx, sink) }()

	for _, ev := range wl.cutovers {
		if err := b.NotifyCutover(ev.Partition, ev.Offset); err != nil {
			t.Fatal(err)
		}
	}

	// Run 会一直阻塞等待新记录；确认全部排空后取消 ctx 收尾。
	deadline := time.Now().Add(5 * time.Second)
	for !b.FullyDrained() {
		if time.Now().After(deadline) {
			t.Fatalf("Run 未能在超时前排空: %+v", b.Snapshot())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("Run 退出错误 = %v，期望 context.Canceled", err)
	}
	if len(sink.published) != len(wl.records) {
		t.Fatalf("Run 有效发布数 = %d，期望 %d", len(sink.published), len(wl.records))
	}
	_ = b.Close()
}
