// 演示：带版本约束的配置信息租约绑定器。
//
// 场景：配置中心修改数据库密码 -> 发布新配置并收回旧租约 ->
// 脱网慢节点因旧租约自然失效而主动停止接收流量。
package main

import (
	"fmt"
	"time"

	"configlease"
)

func tryServe(n *lease.Node, req string) {
	resp, err := n.Serve(req)
	if err != nil {
		fmt.Printf("  [reject] %s: %v\n", n.Name(), err)
		return
	}
	fmt.Printf("  [ok]     %s\n", resp)
}

// renewLoop 模拟真实节点的后台续期协程：到期前持续续期，保持租约有效。
func renewLoop(n *lease.Node, interval time.Duration, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-time.After(interval):
			if err := n.Renew(); err != nil {
				return // 租约失效，停止续期（节点已拒绝服务）
			}
		}
	}
}

func main() {
	ttl := 2 * time.Second
	center := lease.NewCenter(ttl, map[string]string{"db_password": "old-secret"})

	fmt.Println("== 1. 三个节点启动，向配置中心租用配置 (v1) ==")
	fast1 := lease.NewNode("fast-1", center)
	fast2 := lease.NewNode("fast-2", center)
	slow := lease.NewNode("slow-offline", center)
	for _, n := range []*lease.Node{fast1, fast2, slow} {
		if err := n.Start(); err != nil {
			panic(err)
		}
		tryServe(n, "SELECT 1")
	}

	// 在线节点启动后台续期，保持租约有效。
	stop := make(chan struct{})
	defer close(stop)
	go renewLoop(fast1, ttl/2, stop)
	go renewLoop(fast2, ttl/2, stop)

	fmt.Println("\n== 2. 慢节点脱网（无法接收推送，也无法续期）==")
	slow.SetOnline(false)
	fmt.Println("  slow-offline 网络中断")

	fmt.Println("\n== 3. 配置中心修改数据库密码，发布新配置并收回旧租约 ==")
	cfg := center.Update(map[string]string{"db_password": "new-secret"})
	fmt.Printf("  配置中心: configVersion=%d db_password=new-secret，旧租约已全部撤销\n", cfg.Version)
	for _, n := range []*lease.Node{fast1, fast2, slow} {
		if err := center.Publish(n); err != nil {
			fmt.Printf("  推送 %s 失败: %v（等待其旧租约自然过期）\n", n.Name(), err)
		} else {
			fmt.Printf("  推送 %s 成功: 新配置 + 新租约凭证\n", n.Name())
		}
	}

	fmt.Println("\n== 4. 快节点使用新配置接客；慢节点在旧租约到期前仍短暂服务 ==")
	tryServe(fast1, "SELECT 1")
	tryServe(slow, "SELECT 1")

	fmt.Printf("\n== 5. 等待旧租约自然过期（TTL=%s）==\n", ttl)
	time.Sleep(ttl + 200*time.Millisecond)

	fmt.Println("\n== 6. 慢节点旧租约已失效，主动停止接收流量，防止用旧密码操作脏数据 ==")
	tryServe(slow, "UPDATE users SET ...")
	fmt.Println("  " + slow.Status())

	fmt.Println("\n== 7. 快节点靠后台续期保持有效租约，服务全程不中断 ==")
	tryServe(fast1, "SELECT 1")
	tryServe(fast2, "SELECT 1")

	fmt.Println("\n== 8. 慢节点恢复联网，重新租用新配置后恢复服务 ==")
	slow.SetOnline(true)
	if err := slow.Start(); err != nil {
		panic(err)
	}
	tryServe(slow, "SELECT 1")

	fmt.Println("\n最终状态:")
	for _, n := range []*lease.Node{fast1, fast2, slow} {
		fmt.Println("  " + n.Status())
	}
}
