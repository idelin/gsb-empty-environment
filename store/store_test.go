package store

import (
	"errors"
	"testing"
)

func TestFencingTokenGate(t *testing.T) {
	s := New()

	// epoch=0 永远非法
	if err := s.Put(0, "k", "v"); !errors.Is(err, ErrStaleFencingToken) {
		t.Fatalf("epoch=0 必须被拒: %v", err)
	}
	// 首个合法写入
	if err := s.Put(1, "a", "from-dc1"); err != nil {
		t.Fatalf("epoch=1 写入应成功: %v", err)
	}
	// 更高 epoch(新主机房接管)写入成功
	if err := s.Put(2, "b", "from-dc2"); err != nil {
		t.Fatalf("epoch=2 写入应成功: %v", err)
	}
	// 旧主机房带着过时 epoch 的迟到写入必须被拒 —— 杜绝双写的最后一道闸
	if err := s.Put(1, "c", "stale-from-dc1"); !errors.Is(err, ErrStaleFencingToken) {
		t.Fatalf("过时 epoch 必须被拒: %v", err)
	}
	if _, ok := s.Get("c"); ok {
		t.Fatal("被拒的写入不应落盘")
	}
	if v, _ := s.Get("a"); v != "from-dc1" {
		t.Fatalf("已有数据不应被破坏, got %q", v)
	}
	if s.LastEpoch() != 2 {
		t.Fatalf("LastEpoch 应为 2, got %d", s.LastEpoch())
	}
}
