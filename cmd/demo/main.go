// demo simulates 1000 user sessions stuck on node A, then kills A and
// shows the sessions being evenly redistributed to B and C.
package main

import (
	"fmt"
	"sort"

	"stickyfailover/sticky"
)

func printDistribution(title string, dist map[string]int) {
	ids := make([]string, 0, len(dist))
	for id := range dist {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fmt.Println(title)
	for _, id := range ids {
		fmt.Printf("  node %s: %d sessions\n", id, dist[id])
	}
}

func main() {
	m := sticky.NewManager("A", "B", "C")

	// B、C 暂时离线，1000 个用户会话全部粘到节点 A。
	_ = m.SetNodeHealth("B", false)
	_ = m.SetNodeHealth("C", false)

	cookies := make([]string, 1000)
	for i := range cookies {
		cookies[i] = fmt.Sprintf("user-cookie-%04d", i)
		if _, err := m.Bind(cookies[i]); err != nil {
			panic(err)
		}
	}
	printDistribution("== before failure ==", m.Distribution())

	// B、C 恢复上线，随后节点 A 宕机，系统捕获异常并故障转移。
	_ = m.SetNodeHealth("B", true)
	_ = m.SetNodeHealth("C", true)
	migrated, err := m.HandleNodeFailure("A")
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nnode A is DOWN -> failover migrated %d sessions\n\n", migrated)
	printDistribution("== after failover ==", m.Distribution())

	fmt.Println("\n== sample rewritten binding headers ==")
	for _, i := range []int{0, 1, 2, 500, 999} {
		s := m.Session(cookies[i])
		node, _ := m.Route(cookies[i])
		fmt.Printf("  %s -> route=%s  %s: %q\n",
			s.ID, node, sticky.BindingHeader, s.Header[sticky.BindingHeader])
	}
}
