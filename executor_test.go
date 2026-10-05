package executor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// 排空路径：所有任务在期限内完成，不触发取消。
func TestShutdownDrainsBeforeDeadline(t *testing.T) {
	exec := New()
	for i := 0; i < 5; i++ {
		if _, err := exec.Submit(func(ctx context.Context) (any, error) {
			time.Sleep(10 * time.Millisecond)
			return "ok", nil
		}); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}

	report := exec.Shutdown(2 * time.Second)
	if !report.Drained {
		t.Fatal("expected drained shutdown")
	}
	if !report.CanceledAt.IsZero() {
		t.Fatal("drained shutdown must not request cancellation")
	}
	if len(report.Unfinished) != 0 {
		t.Fatalf("unexpected unfinished: %v", report.Unfinished)
	}
	if report.Completed != 5 {
		t.Fatalf("Completed = %d, want 5", report.Completed)
	}
}

// 超时路径：到期请求取消并返回未完成清单；忽略取消的任务
// 迟到完成后，结果仍被安全接收，且清单不再包含它。
func TestShutdownTimeoutAndLateResults(t *testing.T) {
	exec := New()

	release := make(chan struct{})
	stubbornID, err := exec.Submit(func(ctx context.Context) (any, error) {
		<-release // 忽略取消，直到测试放行
		return "late", nil
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	cooperativeDone := make(chan struct{})
	cooperativeID, err := exec.Submit(func(ctx context.Context) (any, error) {
		defer close(cooperativeDone)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	start := time.Now()
	report := exec.Shutdown(100 * time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Shutdown blocked too long: %v", elapsed)
	}
	if report.Drained {
		t.Fatal("expected timeout shutdown")
	}
	if report.CanceledAt.IsZero() {
		t.Fatal("expected cancellation timestamp")
	}
	if len(report.Unfinished) != 2 {
		t.Fatalf("Unfinished = %v, want 2 tasks", report.Unfinished)
	}

	// 配合取消的任务应已退出。
	select {
	case <-cooperativeDone:
	case <-time.After(time.Second):
		t.Fatal("cooperative task did not observe cancellation")
	}

	// 关闭后拒绝新任务。
	if _, err := exec.Submit(func(context.Context) (any, error) { return nil, nil }); !errors.Is(err, ErrShutdown) {
		t.Fatalf("Submit after shutdown: %v", err)
	}

	// 配合取消的任务完成后，重复查询的报告不应再把它列入清单。
	deadline := time.Now().Add(time.Second)
	for {
		report, ok := exec.Report()
		if !ok {
			t.Fatal("Report before shutdown completed")
		}
		if !contains(report.Unfinished, cooperativeID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cooperative task %s still listed as unfinished", cooperativeID)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 放行忽略取消的任务：关闭返回后，迟到结果仍须安全接收。
	close(release)
	deadline = time.Now().Add(time.Second)
	for {
		found := false
		for _, res := range exec.Results() {
			if res.ID == stubbornID {
				found = true
				if res.Value != "late" {
					t.Fatalf("late result value = %v", res.Value)
				}
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("late result was not recorded")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 迟到完成后，清单不得再包含该任务。
	report, ok := exec.Report()
	if !ok {
		t.Fatal("Report not ready")
	}
	if contains(report.Unfinished, stubbornID) {
		t.Fatalf("completed task %s re-listed as unfinished", stubbornID)
	}
	if len(report.Unfinished) != 0 {
		t.Fatalf("Unfinished = %v, want empty", report.Unfinished)
	}
	if report.Completed != 2 {
		t.Fatalf("Completed = %d, want 2", report.Completed)
	}
	if exec.Pending() != 0 {
		t.Fatalf("Pending = %d, want 0", exec.Pending())
	}
}

// 报告可重复查询且结果一致；并发调用 Shutdown 只执行一次关闭流程。
func TestReportRepeatableAndShutdownIdempotent(t *testing.T) {
	exec := New()
	release := make(chan struct{})
	id, err := exec.Submit(func(ctx context.Context) (any, error) {
		<-release
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	const callers = 8
	reports := make([]Report, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reports[i] = exec.Shutdown(50 * time.Millisecond)
		}(i)
	}
	wg.Wait()

	for i, report := range reports {
		if report.Drained {
			t.Fatalf("report %d: expected timeout shutdown", i)
		}
		if len(report.Unfinished) != 1 || report.Unfinished[0] != id {
			t.Fatalf("report %d: Unfinished = %v, want [%s]", i, report.Unfinished, id)
		}
		if !report.ShutdownAt.Equal(reports[0].ShutdownAt) {
			t.Fatalf("report %d: ShutdownAt differs", i)
		}
	}

	// 任务完成后再次查询，所有并发报告对应的清单都应收敛为空。
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		report, _ := exec.Report()
		if len(report.Unfinished) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s still listed after completion", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// 未关闭时 Report 不可用。
func TestReportBeforeShutdown(t *testing.T) {
	exec := New()
	if _, ok := exec.Report(); ok {
		t.Fatal("Report should not be ready before Shutdown")
	}
}

// 空执行器立即排空。
func TestShutdownWithNoTasks(t *testing.T) {
	exec := New()
	report := exec.Shutdown(time.Second)
	if !report.Drained || len(report.Unfinished) != 0 || report.Completed != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func contains(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
