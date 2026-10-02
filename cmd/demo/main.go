// demo 演示基于租约的缓存失效一致性保证器的两个场景：
//  1. 租约持有者失联：写入被安全阻塞至租约满 5 秒；
//  2. 租约持有者在线：撤销回调成功，写入提前放行。
//
// 演示在 PipeNetwork（进程内内存网络）上运行标准 HTTP/JSON 协议；
// 真实部署改用 TCP（Server.Start / Client.Start）即可，协议不变。
package main

import (
	"fmt"
	"time"

	"leasecache"
)

func main() {
	start := time.Now()
	log := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		fmt.Printf("[%4.1fs] %s\n", time.Since(start).Seconds(), line)
	}

	pn := leasecache.NewPipeNetwork()

	srv := leasecache.NewServer(5 * time.Second)
	srv.Transport = pn.HTTPTransport()
	srv.Logf = func(format string, args ...interface{}) {
		log("server: "+format, args...)
	}
	srvLn, err := pn.Listen("server")
	if err != nil {
		panic(err)
	}
	if err := srv.StartOn(srvLn); err != nil {
		panic(err)
	}
	defer srv.Close()
	srv.SetFile("X", []byte("v1"))

	newClient := func(id string) *leasecache.Client {
		c := leasecache.NewClient(id, srv.URL())
		c.Transport = pn.HTTPTransport()
		ln, err := pn.Listen(id)
		if err != nil {
			panic(err)
		}
		if err := c.StartOn(ln); err != nil {
			panic(err)
		}
		return c
	}

	log("=== 场景 1：租约持有者失联，写入阻塞至租约过期 ===")

	c1 := newClient("client-1")
	defer c1.Close()
	c2 := newClient("client-2")
	defer c2.Close()

	data, v, _ := c1.Read("X")
	log("client-1: READ X -> %q (version=%d)，获得 5 秒读租约", data, v)

	c1.Disconnect()
	log("client-1: 模拟失联（关闭回调端口），继续持有本地缓存")

	time.Sleep(2 * time.Second)
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		log("client-2: WRITE X = \"v2\" 发起，等待服务端放行...")
		nv, err := c2.Write("X", []byte("v2"))
		if err != nil {
			log("client-2: 写入失败: %v", err)
			return
		}
		log("client-2: WRITE 完成，X version=%d", nv)
	}()

	time.Sleep(1 * time.Second) // t≈3s
	cached, cv, _ := c1.Read("X")
	log("client-1: READ X -> %q (version=%d)（本地缓存，租约仍有效，零脏读）", cached, cv)

	<-writeDone // t≈5s
	data, v, _ = c2.Read("X")
	log("client-2: READ X -> %q (version=%d)（写入已生效）", data, v)

	log("=== 场景 2：租约持有者在线，撤销成功，写入提前放行 ===")

	c3 := newClient("client-3")
	defer c3.Close()

	data, v, _ = c3.Read("X")
	log("client-3: READ X -> %q (version=%d)，获得 5 秒读租约（保持在线）", data, v)

	wStart := time.Now()
	nv, _ := c2.Write("X", []byte("v3"))
	log("client-2: WRITE X = \"v3\" 完成（version=%d），仅等待 %v（撤销回调成功，未等满 5 秒）",
		nv, time.Since(wStart).Round(time.Millisecond))

	data, v, _ = c3.Read("X")
	log("client-3: READ X -> %q (version=%d)（缓存已被撤销，重新取租约）", data, v)
}
