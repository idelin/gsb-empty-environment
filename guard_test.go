package metaguard

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func mustIssue(t *testing.T, g *Guard, prefix, source string) string {
	t.Helper()
	id, err := g.Issue(prefix, source)
	if err != nil {
		t.Fatalf("Issue(%q, %q): %v", prefix, source, err)
	}
	return id
}

func reasonOf(t *testing.T, err error) RejectReason {
	t.Helper()
	var rej *Rejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *Rejection, got %v", err)
	}
	return rej.Reason
}

func TestHappyPathOneTimeUse(t *testing.T) {
	g := NewGuard()
	id := mustIssue(t, g, "/v1/meta", "10.0.0.5")

	if err := g.Authorize(id, "/v1/meta/instance-id", "10.0.0.5"); err != nil {
		t.Fatalf("first use should succeed: %v", err)
	}
	err := g.Authorize(id, "/v1/meta/instance-id", "10.0.0.5")
	if got := reasonOf(t, err); got != ReasonTokenConsumed {
		t.Fatalf("second use: reason = %s, want %s", got, ReasonTokenConsumed)
	}
}

// 越权类别一：路径遍历。
func TestRejectPathTraversal(t *testing.T) {
	g := NewGuard()

	cases := []string{
		"/v1/meta/../admin/credentials",
		"/v1/../../etc/passwd",
		"/v1/meta/%2e%2e/..", // contains a literal ".." segment
		"../v1/meta",         // not absolute after traversal
	}
	for _, p := range cases {
		id := mustIssue(t, g, "/v1/meta", "10.0.0.5")
		err := g.Authorize(id, p, "10.0.0.5")
		if got := reasonOf(t, err); got != ReasonPathTraversal {
			t.Errorf("path %q: reason = %s, want %s", p, got, ReasonPathTraversal)
		}
	}

	// Traversal in the issuance prefix is rejected at Issue time.
	if _, err := g.Issue("/v1/../v2", "10.0.0.5"); err == nil {
		t.Error("Issue with traversal prefix should fail")
	}
}

// 越权类别二：跨版本请求。
func TestRejectCrossVersion(t *testing.T) {
	g := NewGuard()
	id := mustIssue(t, g, "/v1/meta", "10.0.0.5")

	err := g.Authorize(id, "/v2/meta/instance-id", "10.0.0.5")
	if got := reasonOf(t, err); got != ReasonCrossVersion {
		t.Fatalf("reason = %s, want %s", got, ReasonCrossVersion)
	}

	// Failed authorization must not consume the token.
	if err := g.Authorize(id, "/v1/meta/instance-id", "10.0.0.5"); err != nil {
		t.Fatalf("token should still be usable after rejection: %v", err)
	}
}

// 越权类别三：来源伪造。
func TestRejectSourceSpoofing(t *testing.T) {
	g := NewGuard()
	id := mustIssue(t, g, "/v1/meta", "10.0.0.5")

	err := g.Authorize(id, "/v1/meta/instance-id", "192.168.1.9")
	if got := reasonOf(t, err); got != ReasonSourceMismatch {
		t.Fatalf("reason = %s, want %s", got, ReasonSourceMismatch)
	}
}

// 前缀逃逸：同源同版本但越出签发前缀。
func TestRejectPrefixEscape(t *testing.T) {
	g := NewGuard()
	id := mustIssue(t, g, "/v1/meta", "10.0.0.5")

	err := g.Authorize(id, "/v1/admin/keys", "10.0.0.5")
	if got := reasonOf(t, err); got != ReasonPrefixMismatch {
		t.Fatalf("reason = %s, want %s", got, ReasonPrefixMismatch)
	}
}

// 并发复用：N 个 goroutine 竞争同一令牌，恰好一个成功，
// 其余被串行拒绝，且冲突被记录到审计日志。
func TestConcurrentReuseSerializedAndLogged(t *testing.T) {
	g := NewGuard()
	// Widen the in-flight window so the race reliably overlaps.
	g.preValidate = func() { time.Sleep(20 * time.Millisecond) }

	id := mustIssue(t, g, "/v1/meta", "10.0.0.5")

	const racers = 16
	var wg sync.WaitGroup
	allowed := make(chan string, racers)
	conflicts := make(chan RejectReason, racers)

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			err := g.Authorize(id, fmt.Sprintf("/v1/meta/req-%d", n), "10.0.0.5")
			if err == nil {
				allowed <- fmt.Sprintf("req-%d", n)
				return
			}
			var rej *Rejection
			if !errors.As(err, &rej) {
				t.Errorf("unexpected error type: %v", err)
				return
			}
			conflicts <- rej.Reason
		}(i)
	}
	wg.Wait()
	close(allowed)
	close(conflicts)

	if n := len(allowed); n != 1 {
		t.Fatalf("allowed count = %d, want exactly 1", n)
	}
	seenConcurrent := false
	for r := range conflicts {
		if r == ReasonConcurrentUse {
			seenConcurrent = true
			continue
		}
		if r != ReasonTokenConsumed {
			t.Errorf("unexpected rejection reason: %s", r)
		}
	}
	if !seenConcurrent {
		t.Error("expected at least one concurrent-reuse rejection")
	}

	// 审计日志：恰好一条允许记录，且至少一条冲突记录。
	var allowedEntries, conflictEntries int
	for _, e := range g.AuditLog() {
		if e.Allowed {
			allowedEntries++
		}
		if e.Conflict && e.Reason == ReasonConcurrentUse {
			conflictEntries++
		}
	}
	if allowedEntries != 1 {
		t.Errorf("audit allowed entries = %d, want 1", allowedEntries)
	}
	if conflictEntries == 0 {
		t.Error("audit log missing concurrent-reuse conflict entries")
	}
}

func TestUnknownTokenRejected(t *testing.T) {
	g := NewGuard()
	err := g.Authorize("no-such-token", "/v1/meta/x", "10.0.0.5")
	if got := reasonOf(t, err); got != ReasonUnknownToken {
		t.Fatalf("reason = %s, want %s", got, ReasonUnknownToken)
	}
}

func TestIssueValidation(t *testing.T) {
	g := NewGuard()
	if _, err := g.Issue("", "src"); err == nil {
		t.Error("empty prefix should fail")
	}
	if _, err := g.Issue("relative/path", "src"); err == nil {
		t.Error("relative prefix should fail")
	}
	if _, err := g.Issue("/v1/meta", ""); err == nil {
		t.Error("empty source should fail")
	}
}
