package netsec

import (
	"sync"
	"testing"
)

func TestConcurrentAccess(t *testing.T) {
	v := newWithdrawVerifier(t)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_ = v.Records()
				_ = v.LastReport()
				_ = v.RecordCount()
				_ = v.Revision()
				if _, err := v.RegroupAndVerify(); err != nil {
					t.Errorf("并发重分组失败: %v", err)
				}
			}
		}()
	}
	for _, id := range []string{"a1", "a2", "b1", "b2", "a3"} {
		wg.Add(1)
		go func(recordID string) {
			defer wg.Done()
			_, _ = v.Withdraw(recordID)
		}(id)
	}
	wg.Wait()

	// 全部撤回后，全量重分组应得到空合规报告而不是报错。
	report, err := v.RegroupAndVerify()
	if err != nil {
		t.Fatalf("最终核验失败: %v", err)
	}
	if !report.Compliant() || len(report.Groups) != 0 {
		t.Fatalf("空集核验结果错误: %+v", report)
	}
}

func TestGroupChangeKindString(t *testing.T) {
	cases := map[GroupChangeKind]string{
		GroupRemoved:        "removed",
		GroupShrunk:         "shrunk",
		GroupUnchanged:      "unchanged",
		GroupChangeKind(99): "unknown",
	}
	for kind, want := range cases {
		if got := kind.String(); got != want {
			t.Fatalf("GroupChangeKind(%d).String()=%q, 期望 %q", kind, got, want)
		}
	}
}
