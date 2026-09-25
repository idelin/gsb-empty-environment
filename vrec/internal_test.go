package vrec

import (
	"errors"
	"testing"
)

// TestTxnOverlappingReentry 确定性地验证：同一写事务在“上一个操作尚未结束”时
// 再次进入，必须返回 ErrTxnReentrant，而不是阻塞或损坏内部状态。
func TestTxnOverlappingReentry(t *testing.T) {
	db, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	// 直接使用内部 enter/leave 构造“操作进行中”的重叠窗口。
	if err := tx.enter(); err != nil {
		t.Fatalf("first enter: %v", err)
	}
	if err := tx.enter(); !errors.Is(err, ErrTxnReentrant) {
		t.Fatalf("overlapping enter = %v, want ErrTxnReentrant", err)
	}
	tx.leave()

	// 释放后可再次正常进入。
	if err := tx.enter(); err != nil {
		t.Fatalf("enter after leave: %v", err)
	}
	tx.leave()
}
