package repartition

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestInvalidConfig(t *testing.T) {
	cases := []Config{
		{NewPartitions: []int{1}, CutOffsets: map[int]int64{}},
		{OldPartitions: []int{0}, CutOffsets: map[int]int64{}},
		{OldPartitions: []int{0, 0}, NewPartitions: []int{1}, CutOffsets: map[int]int64{0: 1}},
		{OldPartitions: []int{0}, NewPartitions: []int{1, 1}, CutOffsets: map[int]int64{0: 1}},
		{OldPartitions: []int{0}, NewPartitions: []int{1}, CutOffsets: map[int]int64{1: 1}},
	}
	for i, cfg := range cases {
		if _, err := Open(filepath.Join(t.TempDir(), "b"), cfg); err == nil {
			t.Fatalf("用例 %d 应拒绝非法配置", i)
		}
	}
}

func TestIngestValidation(t *testing.T) {
	b, err := Open(filepath.Join(t.TempDir(), "b"), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	if err := b.Ingest(Record{Partition: 7, Offset: 0}); err == nil {
		t.Fatal("未知旧分区应报错")
	}
	if err := b.Ingest(Record{Topology: true, Partition: 7, Offset: 0}); err == nil {
		t.Fatal("未知新分区应报错")
	}
	if err := b.Ingest(Record{Partition: 0, Offset: -1}); err == nil {
		t.Fatal("负偏移应报错")
	}
	if err := b.Ingest(Record{Partition: 0, Offset: 99}); !errors.Is(err, ErrFenced) {
		t.Fatalf("超过切换偏移应 ErrFenced，得到 %v", err)
	}

	if err := b.Ingest(Record{Partition: 1, Offset: 0}); err != nil {
		t.Fatal(err)
	}
	// 完全相同的记录按幂等忽略；同分区更小偏移违反单调性。
	if err := b.Ingest(Record{Partition: 1, Offset: 0, Key: "dup"}); err != nil {
		t.Fatalf("同 ID 重复记录应幂等，得到 %v", err)
	}
	if err := b.Ingest(Record{Partition: 1, Offset: 0}); err != nil {
		t.Fatalf("完全重复记录应幂等，得到 %v", err)
	}

	if err := b.NotifyCutover(7, 0); err == nil {
		t.Fatal("未知分区的切换通知应报错")
	}
	if err := b.NotifyCutover(0, 1); err == nil {
		t.Fatal("偏移与配置不符的切换通知应报错")
	}
}

func TestCloseRejectsOperations(t *testing.T) {
	b, err := Open(filepath.Join(t.TempDir(), "b"), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Ingest(Record{Partition: 0, Offset: 0}); !errors.Is(err, ErrClosed) {
		t.Fatalf("关闭后 Ingest 应 ErrClosed，得到 %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("重复 Close 应幂等，得到 %v", err)
	}
	if !b.Idle() {
		t.Fatal("关闭后 Idle 应为 true")
	}
}
