// demo 演示基于租约的缓存失效一致性协议（AFS/Leases 风格）：
//
//	场景 A：客户端 1 在线 —— 服务端回调撤销租约，写者立即放行。
//	场景 B：客户端 1 失联 —— 服务端安全阻塞至租约满 5 秒过期后才放行写入。
package main

import (
	"fmt"
	"time"

	"leasecache/lease"
)

var start = time.Now()

func ts() string { return fmt.Sprintf("t=%4.1fs", time.Since(start).Seconds()) }

func main() {
	const leaseDuration = 5 * time.Second

	// 进程内传输（net.Pipe）。生产环境可替换为：
	//   lease.NetTransport{Network: "tcp", ListenAddr: ":9000"}
	tr := lease.NewMemTransport()
	srv := lease.NewServer(leaseDuration, tr)
	ln, err := tr.Listen()
	if err != nil {
		panic(err)
	}
	defer ln.Close()
	srv.Serve(ln)
	addr := ln.Addr().String()

	fmt.Printf("=== 租约服务端已启动 %s（租约时长 %v） ===\n\n", addr, leaseDuration)

	scenarioA(tr, addr)
	fmt.Println()
	start = time.Now()
	scenarioB(tr, addr)
}

// 场景 A：读客户端在线，撤销回调成功，写者近乎立即放行。
func scenarioA(tr *lease.MemTransport, addr string) {
	fmt.Println("--- 场景 A：客户端 1 在线（撤销回调成功） ---")
	c1, _ := lease.NewClient("client-1", addr, tr)
	defer c1.Close()
	c2, _ := lease.NewClient("client-2", addr, tr)
	defer c2.Close()

	c1.Read("X")
	fmt.Printf("[%s] 客户端 1 获得 X 的读租约（5s），内容缓存到本地\n", ts())

	go func() {
		time.Sleep(2 * time.Second)
		fmt.Printf("[%s] 客户端 2 请求写入 X ...\n", ts())
		t0 := time.Now()
		c2.Write("X", []byte("v2"))
		fmt.Printf("[%s] 写入生效（等待 %v）：服务端回调撤销 -> 客户端 1 确认 -> 立即放行\n",
			ts(), time.Since(t0).Round(time.Millisecond))
	}()
	time.Sleep(3 * time.Second)

	got, hit, _ := c1.Read("X")
	fmt.Printf("[%s] 客户端 1 再次读 X：cache-hit=%v 内容=%q（缓存已被撤销，回源读到新值）\n", ts(), hit, got)
}

// 场景 B：读客户端失联，撤销回调失败，写者阻塞至租约自然过期。
func scenarioB(tr *lease.MemTransport, addr string) {
	fmt.Println("--- 场景 B：客户端 1 失联（撤销回调无法送达） ---")
	c1, _ := lease.NewClient("client-1", addr, tr)
	c2, _ := lease.NewClient("client-2", addr, tr)
	defer c2.Close()

	c1.Read("X")
	fmt.Printf("[%s] 客户端 1 获得 X 的读租约（5s），随后失联\n", ts())
	c1.Disconnect()

	done := make(chan struct{})
	go func() {
		time.Sleep(2 * time.Second)
		fmt.Printf("[%s] 客户端 2 请求写入 X ...（服务端撤销通知失败，安全阻塞等待租约过期）\n", ts())
		t0 := time.Now()
		c2.Write("X", []byte("v3"))
		fmt.Printf("[%s] 写入生效（阻塞 %v）：租约自然过期，零读脏保证成立\n",
			ts(), time.Since(t0).Round(time.Millisecond))
		close(done)
	}()
	<-done

	c3, _ := lease.NewClient("client-3", addr, tr)
	defer c3.Close()
	got, _, _ := c3.Read("X")
	fmt.Printf("[%s] 新客户端读 X：内容=%q（写入已生效）\n", ts(), got)
}
