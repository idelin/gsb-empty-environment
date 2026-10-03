package sticky

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newABCManager() (*Manager, *Node, *Node, *Node) {
	a, b, c := NewNode("A"), NewNode("B"), NewNode("C")
	return NewManager("test-salt", a, b, c), a, b, c
}

func uid(i int) string { return fmt.Sprintf("user-%04d", i) }

// 节点健康时，同一会话无论绑定多少次都导向同一节点。
func TestStickyBindingStableWhileHealthy(t *testing.T) {
	m, _, _, _ := newABCManager()

	first := make(map[string]string)
	for i := 0; i < 1000; i++ {
		nodeID, err := m.Bind(uid(i))
		if err != nil {
			t.Fatalf("Bind(%d) error: %v", i, err)
		}
		first[uid(i)] = nodeID
	}
	for round := 0; round < 5; round++ {
		for i := 0; i < 1000; i++ {
			nodeID, err := m.Bind(uid(i))
			if err != nil {
				t.Fatalf("re-Bind(%d) error: %v", i, err)
			}
			if nodeID != first[uid(i)] {
				t.Fatalf("session %s drifted: %s -> %s", uid(i), first[uid(i)], nodeID)
			}
		}
	}
}

// 验收边界：1000 个会话绑定在 A；A 宕机后被均匀平摊给 B 和 C。
func TestFailoverEvenRedistribution(t *testing.T) {
	m, a, b, c := newABCManager()

	// 先让 B、C 离线，1000 个会话全部落到 A。
	b.SetHealthy(false)
	c.SetHealthy(false)
	for i := 0; i < 1000; i++ {
		nodeID, err := m.Bind(uid(i))
		if err != nil {
			t.Fatalf("Bind(%d) error: %v", i, err)
		}
		if nodeID != "A" {
			t.Fatalf("session %s bound to %s, want A", uid(i), nodeID)
		}
	}

	// B、C 恢复上线，随后 A 宕机，触发故障转移。
	b.SetHealthy(true)
	c.SetHealthy(true)
	moved, err := m.Failover("A")
	if err != nil {
		t.Fatalf("Failover(A) error: %v", err)
	}
	if moved != 1000 {
		t.Fatalf("moved = %d, want 1000", moved)
	}
	if a.Healthy() {
		t.Fatal("node A should be marked unhealthy after failover")
	}

	dist := m.Distribution()
	if dist["A"] != 0 {
		t.Fatalf("node A still holds %d sessions", dist["A"])
	}
	total := dist["B"] + dist["C"]
	if total != 1000 {
		t.Fatalf("B+C hold %d sessions, want 1000", total)
	}
	// 均匀性：理想值 500/500，允许 ±20% 抖动。
	for _, id := range []string{"B", "C"} {
		if n := dist[id]; n < 400 || n > 600 {
			t.Fatalf("node %s got %d sessions, want within [400, 600]", id, n)
		}
	}
	t.Logf("distribution after failover: B=%d C=%d", dist["B"], dist["C"])

	// 重定向后的绑定保持稳定。
	for i := 0; i < 1000; i++ {
		nodeID, err := m.Bind(uid(i))
		if err != nil {
			t.Fatalf("post-failover Bind(%d) error: %v", i, err)
		}
		if nodeID != "B" && nodeID != "C" {
			t.Fatalf("session %s rebound to %s, want B or C", uid(i), nodeID)
		}
	}
}

// 故障转移后，会话头中的绑定标记被重写为新节点。
func TestBindingHeaderRewrittenAfterFailover(t *testing.T) {
	m, _, b, c := newABCManager()
	b.SetHealthy(false)
	c.SetHealthy(false)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: UserCookie, Value: "user-0001"})
	rec := httptest.NewRecorder()
	if _, nodeID, err := m.Route(rec, req); err != nil {
		t.Fatalf("Route error: %v", err)
	} else if nodeID != "A" {
		t.Fatalf("routed to %s, want A", nodeID)
	}
	if got := rec.Header().Get(BindingHeader); got != "user-0001@A" {
		t.Fatalf("binding header = %q, want %q", got, "user-0001@A")
	}

	b.SetHealthy(true)
	c.SetHealthy(true)
	if _, err := m.Failover("A"); err != nil {
		t.Fatalf("Failover(A) error: %v", err)
	}

	rec2 := httptest.NewRecorder()
	if _, nodeID, err := m.Route(rec2, req); err != nil {
		t.Fatalf("post-failover Route error: %v", err)
	} else if nodeID == "A" {
		t.Fatal("session still routed to failed node A")
	}
	header := rec2.Header().Get(BindingHeader)
	if !strings.HasPrefix(header, "user-0001@") || strings.HasSuffix(header, "@A") {
		t.Fatalf("binding header not rewritten, got %q", header)
	}
	t.Logf("rewritten binding header: %s", header)
}

// 无 Cookie 的请求会被签发新会话标识并写回 Cookie。
func TestRouteIssuesCookieForNewSession(t *testing.T) {
	m, _, _, _ := newABCManager()

	rec := httptest.NewRecorder()
	uid1, _, err := m.Route(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == UserCookie {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value != uid1 {
		t.Fatalf("expected Set-Cookie %s=%s, got %v", UserCookie, uid1, cookie)
	}

	// 携带 Cookie 再次访问，绑定保持不变。
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	uid2, node2, err := m.Route(rec2, req2)
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if uid2 != uid1 {
		t.Fatalf("uid changed: %s -> %s", uid1, uid2)
	}
	if want := rec.Header().Get(BindingHeader); rec2.Header().Get(BindingHeader) != want {
		t.Fatalf("binding header changed: %q -> %q", want, rec2.Header().Get(BindingHeader))
	}
	_ = node2
}

// 没有健康节点时，绑定与故障转移都必须报错。
func TestNoHealthyNode(t *testing.T) {
	m, a, _, _ := newABCManager()
	m.nodes["B"].SetHealthy(false)
	m.nodes["C"].SetHealthy(false)

	if _, err := m.Bind("user-x"); err != nil {
		t.Fatalf("Bind with A healthy error: %v", err)
	}
	if _, err := m.Failover("A"); !errors.Is(err, ErrNoHealthyNode) {
		t.Fatalf("Failover error = %v, want ErrNoHealthyNode", err)
	}
	if a.Healthy() {
		t.Fatal("node A should be marked unhealthy even when failover fails")
	}
	if _, err := m.Bind("user-y"); !errors.Is(err, ErrNoHealthyNode) {
		t.Fatalf("Bind error = %v, want ErrNoHealthyNode", err)
	}
}

// 对未知节点执行故障转移应报错。
func TestFailoverUnknownNode(t *testing.T) {
	m, _, _, _ := newABCManager()
	if _, err := m.Failover("Z"); err == nil {
		t.Fatal("Failover(Z) should fail for unknown node")
	}
}
