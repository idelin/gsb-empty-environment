// demo 演示优雅离开协议：5 节点集群中 node-3 主动下线，
// 观察平静期内新流量被阻断、在途请求完成、定时器超时后路由条目物理销毁。
package main

import (
	"fmt"
	"time"

	"graceful"
)

func main() {
	quiescent := 300 * time.Millisecond
	c := graceful.NewCluster(quiescent)
	ids := []string{"node-1", "node-2", "node-3", "node-4", "node-5"}
	for _, id := range ids {
		c.Join(id)
	}

	a, _ := c.Node("node-1")
	n3, _ := c.Node("node-3")

	fmt.Printf("[t=%3dms] 集群就绪, node-1 就绪池: %v\n", 0, a.Router.Ready())

	// 模拟一个发往 node-3 的慢请求（在途 RPC）。
	inflightDone := make(chan error, 1)
	go func() {
		inflightDone <- c.Call("node-1", "node-3", func() {
			time.Sleep(150 * time.Millisecond) // 慢请求处理中
		})
	}()
	time.Sleep(20 * time.Millisecond) // 确保请求已进入在途

	// node-3 主动发起 Leave。
	fmt.Printf("[t=%3dms] node-3 发起 Leave, 广播离开意向\n", elapsed())
	n3.Leave()

	// 平静期内：新流量被本地快速阻断。
	err := c.Call("node-1", "node-3", func() { fmt.Println("不应执行") })
	fmt.Printf("[t=%3dms] 平静期内新 RPC -> 快速失败: %v (无 RPC 超时)\n", elapsed(), err)
	fmt.Printf("[t=%3dms] node-1 就绪池: %v (node-3 已移出)\n", elapsed(), a.Router.Ready())

	// 在途请求正常完成。
	if err := <-inflightDone; err != nil {
		fmt.Printf("[t=%3dms] 在途请求失败: %v\n", elapsed(), err)
	} else {
		fmt.Printf("[t=%3dms] 在途慢请求正常完成\n", elapsed())
	}

	// 等待平静期定时器超时，路由条目物理销毁。
	time.Sleep(quiescent)
	_, ok := a.Router.State("node-3")
	fmt.Printf("[t=%3dms] 平静期结束, node-3 路由条目已物理销毁: %v\n", elapsed(), !ok)
	fmt.Printf("[t=%3dms] node-3 最终状态: %v\n", elapsed(), n3.State())
}

var start = time.Now()

func elapsed() int64 {
	return time.Since(start).Nanoseconds() / 1e6
}
