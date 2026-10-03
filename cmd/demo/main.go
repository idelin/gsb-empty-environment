// demo 模拟验收场景：1000 个用户会话绑定在节点 A，
// A 宕机后被均匀平摊给 B 和 C，并在会话头中重写绑定标记。
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"

	"sticky"
)

func main() {
	a, b, c := sticky.NewNode("A"), sticky.NewNode("B"), sticky.NewNode("C")
	m := sticky.NewManager("demo-salt", a, b, c)

	// 阶段 1：B、C 尚未上线，1000 个用户会话全部绑定到 A。
	b.SetHealthy(false)
	c.SetHealthy(false)

	cookies := make([]*http.Cookie, 1000)
	for i := range cookies {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if _, _, err := m.Route(rec, req); err != nil {
			panic(err)
		}
		cookies[i] = rec.Result().Cookies()[0]
	}
	fmt.Println("== 阶段 1：初始绑定（仅 A 在线） ==")
	printDist(m)

	// 阶段 2：B、C 上线，随后 A 宕机，触发故障转移。
	b.SetHealthy(true)
	c.SetHealthy(true)
	moved, err := m.Failover("A")
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n== 阶段 2：节点 A 宕机，%d 个会话被重定向 ==\n", moved)
	printDist(m)

	// 阶段 3：抽样验证会话头中的绑定标记已被重写。
	fmt.Println("\n== 阶段 3：抽样验证绑定标记重写 ==")
	for _, i := range []int{0, 1, 2, 500, 999} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(cookies[i])
		rec := httptest.NewRecorder()
		if _, _, err := m.Route(rec, req); err != nil {
			panic(err)
		}
		fmt.Printf("cookie=%s  ->  %s: %s\n",
			cookies[i].Value, sticky.BindingHeader, rec.Header().Get(sticky.BindingHeader))
	}
}

func printDist(m *sticky.Manager) {
	dist := m.Distribution()
	ids := make([]string, 0, len(dist))
	for id := range dist {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Printf("  节点 %s: %d 个会话\n", id, dist[id])
	}
}
