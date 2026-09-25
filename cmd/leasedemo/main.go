package main

import (
	"fmt"
	"log"
	"time"

	"leasefencing/lease"
)

func main() {
	offsets := map[string]time.Duration{
		"A": 200 * time.Millisecond,
		"B": -200 * time.Millisecond,
	}
	sys := lease.NewSystem([]string{"A", "B"}, 4*time.Second, 250*time.Millisecond, offsets)
	sys.Sim.SetDelay("A", lease.ResourceID, 5*time.Second)
	a := sys.Nodes["A"]
	b := sys.Nodes["B"]

	fmt.Printf("t=%s 显式时钟偏差: A=%+v B=%+v (maxClockError=250ms)\n", sys.Sim.Now().Format("15:04:05.000"), offsets["A"], offsets["B"])

	a.Campaign()
	sys.Sim.Advance(100 * time.Millisecond)
	fmt.Printf("t=%s A 成为领导 term=%d\n", sys.Sim.Now().Format("15:04:05.000"), a.Term())

	must(a.RequestWrite("v1-from-A"))
	sys.Sim.Advance(50 * time.Millisecond)
	fmt.Printf("t=%s A 取得 fence=%d 并发出写（链路延迟 5s）\n", sys.Sim.Now().Format("15:04:05.000"), a.LastFence())

	sys.Sim.Pause("A")
	fmt.Printf("t=%s A 暂停（GC/调度停顿），租约继续到期\n", sys.Sim.Now().Format("15:04:05.000"))
	sys.Sim.Advance(4200 * time.Millisecond)

	b.Campaign()
	sys.Sim.Advance(100 * time.Millisecond)
	fmt.Printf("t=%s B 成为新领导 term=%d\n", sys.Sim.Now().Format("15:04:05.000"), b.Term())

	must(b.RequestWrite("v2-from-B"))
	sys.Sim.Advance(200 * time.Millisecond)
	fmt.Printf("t=%s 资源接受 B: fence=%d value=%q\n", sys.Sim.Now().Format("15:04:05.000"), sys.Resource.LastFence(), sys.Resource.Value())

	sys.Sim.Resume("A")
	sys.Sim.Advance(1500 * time.Millisecond)
	fmt.Printf("t=%s A 恢复后角色=%s；A 的旧写到资源被拒绝: %s\n", sys.Sim.Now().Format("15:04:05.000"), a.Role(), sys.Resource.LastRejection)
	fmt.Printf("最终: value=%q lastFence=%d rejected=%d\n", sys.Resource.Value(), sys.Resource.LastFence(), sys.Resource.Rejected)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
