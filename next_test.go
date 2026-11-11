package repartition

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestNextBlocksUntilRecord 验证 Next 在无可见记录时阻塞，记录到达后唤醒。
func TestNextBlocksUntilRecord(t *testing.T) {
	b, err := Open(filepath.Join(t.TempDir(), "b"), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	got := make(chan Record, 1)
	go func() {
		r, err := b.Next(context.Background())
		if err != nil {
			t.Errorf("Next 返回错误: %v", err)
			return
		}
		got <- r
	}()

	time.Sleep(20 * time.Millisecond)
	select {
	case <-got:
		t.Fatal("无记录时 Next 不应返回")
	default:
	}

	want := Record{Partition: 0, Offset: 0}
	if err := b.Ingest(want); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-got:
		if r.id() != want.id() {
			t.Fatalf("Next 返回 %s，期望 %s", r.id(), want.id())
		}
	case <-time.After(time.Second):
		t.Fatal("记录到达后 Next 未被唤醒")
	}
}

// TestNextContextCancel 验证 ctx 取消能解除 Next 阻塞。
func TestNextContextCancel(t *testing.T) {
	b, err := Open(filepath.Join(t.TempDir(), "b"), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := b.Next(ctx)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("期望 context.Canceled，得到 %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ctx 取消后 Next 未退出")
	}
}
