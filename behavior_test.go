package repartition

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestNoEarlyVisibility 验证所有旧分区到达切换偏移前，新拓扑记录绝不可见。
func TestNoEarlyVisibility(t *testing.T) {
	b, err := Open(filepath.Join(t.TempDir(), "b"), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	wl := buildWorkload()
	for _, r := range wl.records {
		if err := b.Ingest(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.NotifyCutover(0, 2); err != nil {
		t.Fatal(err)
	}

	st := b.Snapshot()
	if st.Switched {
		t.Fatal("仅一个旧分区到达时屏障不应翻转")
	}
	if st.Pending != 5 {
		t.Fatalf("切换前可见记录数 = %d，期望 5", st.Pending)
	}
	for i := 0; i < 5; i++ {
		r, err := b.TryNext()
		if err != nil {
			t.Fatalf("旧拓扑记录应可见: %v", err)
		}
		if r.Topology {
			t.Fatalf("切换前出现新拓扑记录: %s", r.id())
		}
		if err := b.Ack(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.TryNext(); !errors.Is(err, ErrDone) {
		t.Fatalf("旧拓扑排空后新拓扑仍不可见，期望 ErrDone，得到 %v", err)
	}

	if err := b.NotifyCutover(1, 1); err != nil {
		t.Fatal(err)
	}
	if !b.Snapshot().Switched {
		t.Fatal("全部旧分区到达后屏障必须翻转")
	}
	seen := 0
	for {
		r, err := b.TryNext()
		if errors.Is(err, ErrDone) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !r.Topology {
			t.Fatalf("翻转后不应再投递旧拓扑记录: %s", r.id())
		}
		seen++
		if err := b.Ack(r); err != nil {
			t.Fatal(err)
		}
	}
	if seen != 6 {
		t.Fatalf("翻转后新拓扑记录数 = %d，期望 6", seen)
	}
}

// TestIdempotentNotifications 验证重复记录、重复切换通知和乱序回执的行为。
func TestIdempotentNotifications(t *testing.T) {
	b, err := Open(filepath.Join(t.TempDir(), "b"), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	r := Record{Partition: 0, Offset: 0}
	for i := 0; i < 3; i++ {
		if err := b.Ingest(r); err != nil {
			t.Fatalf("重复摄入应幂等: %v", err)
		}
	}
	if b.Snapshot().KnownCount != 1 {
		t.Fatalf("重复记录被重复持久化")
	}
	if err := b.NotifyCutover(0, 2); err != nil {
		t.Fatal(err)
	}
	if err := b.NotifyCutover(0, 2); err != nil {
		t.Fatalf("重复切换通知应幂等: %v", err)
	}

	got, err := b.TryNext()
	if err != nil {
		t.Fatal(err)
	}
	other := Record{Partition: 1, Offset: 0}
	if err := b.Ack(other); err == nil {
		t.Fatal("非队首记录的回执必须被拒绝")
	}
	if err := b.Ack(got); err != nil {
		t.Fatal(err)
	}
}

// TestFencing 验证切换完成后迟到的旧拓扑记录被隔离拒绝。
func TestFencing(t *testing.T) {
	b, err := Open(filepath.Join(t.TempDir(), "b"), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	if err := b.NotifyCutover(0, 2); err != nil {
		t.Fatal(err)
	}
	if err := b.NotifyCutover(1, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Ingest(Record{Partition: 0, Offset: 3}); !errors.Is(err, ErrFenced) {
		t.Fatalf("切换后旧拓扑记录应被隔离，得到 %v", err)
	}
	if err := b.Ingest(Record{Partition: 1, Offset: 0}); !errors.Is(err, ErrFenced) {
		t.Fatalf("切换后迟到旧记录应被隔离，得到 %v", err)
	}
}

// TestConfigMismatch 验证持久化配置与重开配置不一致时被拒绝。
func TestConfigMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b")
	b, err := Open(path, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Close()

	other := testConfig()
	other.CutOffsets = map[int]int64{0: 99, 1: 1}
	if _, err := Open(path, other); !errors.Is(err, ErrConfigMismatch) {
		t.Fatalf("期望 ErrConfigMismatch，得到 %v", err)
	}
}

// TestCleanRunBaseline 无故障基线：所有记录恰好发布一次。
func TestCleanRunBaseline(t *testing.T) {
	wl := buildWorkload()
	sink := newMemSink()
	runToCompletion(t, filepath.Join(t.TempDir(), "b"), wl, sink, nil)

	got, want := sink.ids(), expectedIDs(wl)
	if len(got) != len(want) {
		t.Fatalf("发布记录数 = %d，期望 %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("发布集合不一致，得到 %v，期望 %v", got, want)
		}
	}
}
