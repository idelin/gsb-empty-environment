package sticky

import (
	"fmt"
	"strings"
	"testing"
)

func bindAll(t *testing.T, m *Manager, cookies []string) {
	t.Helper()
	for _, c := range cookies {
		if _, err := m.Bind(c); err != nil {
			t.Fatalf("Bind(%q): %v", c, err)
		}
	}
}

func makeCookies(n int) []string {
	cookies := make([]string, n)
	for i := range cookies {
		cookies[i] = fmt.Sprintf("user-cookie-%04d", i)
	}
	return cookies
}

// 节点健康时，同一 cookie 永远路由到同一节点。
func TestStickyBindingWhileHealthy(t *testing.T) {
	m := NewManager("A", "B", "C")
	cookies := makeCookies(100)
	bindAll(t, m, cookies)

	for _, c := range cookies {
		first, err := m.Route(c)
		if err != nil {
			t.Fatalf("Route: %v", err)
		}
		for i := 0; i < 5; i++ {
			got, err := m.Route(c)
			if err != nil {
				t.Fatalf("Route: %v", err)
			}
			if got != first {
				t.Fatalf("cookie %q flapped: %s -> %s", c, first, got)
			}
		}
		s := m.Session(c)
		if s.NodeID != first {
			t.Fatalf("session bound to %s, route says %s", s.NodeID, first)
		}
		if !strings.Contains(s.Header[BindingHeader], "node="+first) {
			t.Fatalf("binding marker %q does not match node %s", s.Header[BindingHeader], first)
		}
	}

	dist := m.Distribution()
	if dist["A"]+dist["B"]+dist["C"] != 100 {
		t.Fatalf("lost sessions: %v", dist)
	}
}

// 验收边界：1000 个会话绑定在 A；A 宕机；全部基于二级哈希均匀平摊到
// B 和 C，且会话头中的绑定标记被重写。
func TestFailoverEvenRedistribution(t *testing.T) {
	m := NewManager("A", "B", "C")

	// 先让 B、C 离线，迫使 1000 个会话全部绑定到 A。
	for _, id := range []string{"B", "C"} {
		if err := m.SetNodeHealth(id, false); err != nil {
			t.Fatal(err)
		}
	}
	cookies := makeCookies(1000)
	bindAll(t, m, cookies)
	if dist := m.Distribution(); dist["A"] != 1000 {
		t.Fatalf("expected 1000 sessions on A, got %v", dist)
	}

	// B、C 恢复，随后 A 宕机，系统捕获异常并故障转移。
	for _, id := range []string{"B", "C"} {
		if err := m.SetNodeHealth(id, true); err != nil {
			t.Fatal(err)
		}
	}
	migrated, err := m.HandleNodeFailure("A")
	if err != nil {
		t.Fatalf("HandleNodeFailure: %v", err)
	}
	if migrated != 1000 {
		t.Fatalf("migrated %d sessions, want 1000", migrated)
	}

	// 全部离开 A，且均匀平摊到 B、C（50/50 ± 10%）。
	dist := m.Distribution()
	if dist["A"] != 0 {
		t.Fatalf("%d sessions still on dead node A", dist["A"])
	}
	for _, id := range []string{"B", "C"} {
		if dist[id] < 400 || dist[id] > 600 {
			t.Fatalf("uneven redistribution: %v", dist)
		}
	}
	t.Logf("distribution after failover: B=%d C=%d", dist["B"], dist["C"])

	// 每个会话的绑定标记都被重写，且此后保持新的粘性。
	for _, c := range cookies {
		s := m.Session(c)
		if s.NodeID == "A" {
			t.Fatalf("session %s still bound to dead node A", s.ID)
		}
		if s.Generation != 2 {
			t.Fatalf("session %s generation = %d, want 2", s.ID, s.Generation)
		}
		marker := s.Header[BindingHeader]
		want := fmt.Sprintf("node=%s;gen=2;via=failover", s.NodeID)
		if marker != want {
			t.Fatalf("binding marker = %q, want %q", marker, want)
		}
		routed, err := m.Route(c)
		if err != nil {
			t.Fatalf("Route: %v", err)
		}
		if routed != s.NodeID {
			t.Fatalf("session %s not sticky after failover: %s vs %s", s.ID, routed, s.NodeID)
		}
	}
}

// 二级故障：A 宕机迁到 B/C 后，B 再宕机，B 上的会话全部迁到唯一存活的 C。
func TestChainedFailover(t *testing.T) {
	m := NewManager("A", "B", "C")
	cookies := makeCookies(600)
	bindAll(t, m, cookies)

	if _, err := m.HandleNodeFailure("A"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.HandleNodeFailure("B"); err != nil {
		t.Fatal(err)
	}

	dist := m.Distribution()
	if dist["A"] != 0 || dist["B"] != 0 || dist["C"] != 600 {
		t.Fatalf("after chained failover: %v", dist)
	}
	for _, c := range cookies {
		if got, _ := m.Route(c); got != "C" {
			t.Fatalf("cookie %q routed to %s, want C", c, got)
		}
	}
}

// 未调用 HandleNodeFailure 时，Route 也能惰性自愈。
func TestLazyHealOnRoute(t *testing.T) {
	m := NewManager("A", "B")
	c := "user-cookie-lazy"
	s, err := m.Bind(c)
	if err != nil {
		t.Fatal(err)
	}
	dead := s.NodeID
	if err := m.SetNodeHealth(dead, false); err != nil {
		t.Fatal(err)
	}
	got, err := m.Route(c)
	if err != nil {
		t.Fatal(err)
	}
	if got == dead {
		t.Fatalf("Route returned dead node %s", got)
	}
	if !strings.Contains(m.Session(c).Header[BindingHeader], "via=failover") {
		t.Fatalf("binding marker not rewritten: %q", m.Session(c).Header[BindingHeader])
	}
}

// 全部节点宕机时报错；节点恢复后已有绑定保持不变。
func TestNoHealthyNodes(t *testing.T) {
	m := NewManager("A", "B")
	if _, err := m.HandleNodeFailure("A"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.HandleNodeFailure("B"); err != ErrNoHealthyNode {
		t.Fatalf("got %v, want ErrNoHealthyNode", err)
	}
	if _, err := m.Bind("new-cookie"); err != ErrNoHealthyNode {
		t.Fatalf("got %v, want ErrNoHealthyNode", err)
	}
}

func TestUnknownNodeAndCookie(t *testing.T) {
	m := NewManager("A")
	if err := m.SetNodeHealth("Z", true); err != ErrUnknownNode {
		t.Fatalf("got %v, want ErrUnknownNode", err)
	}
	if _, err := m.HandleNodeFailure("Z"); err != ErrUnknownNode {
		t.Fatalf("got %v, want ErrUnknownNode", err)
	}
	if _, err := m.Route("nobody"); err != ErrUnknownCookie {
		t.Fatalf("got %v, want ErrUnknownCookie", err)
	}
}
