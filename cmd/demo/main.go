// demo 演示会话版本令牌在异步复制集群中的行为：
// 会话迁移后读己之写、故障下显式超时、重启后令牌保持有效、令牌不混用。
package main

import (
	"errors"
	"fmt"

	session "sessiontoken"
)

func printTrace(sim *session.Simulator, from int) {
	for _, line := range sim.Trace[from:] {
		fmt.Println("   ", line)
	}
}

func main() {
	sim := session.NewSimulator()
	a := session.NewNode(sim, "A", session.NewMemDisk(), 10)
	b := session.NewNode(sim, "B", session.NewMemDisk(), 10)
	session.NewNode(sim, "C", session.NewMemDisk(), 10)
	a.SetPeers("B", "C")
	b.SetPeers("A", "C")
	sim.Node("C").SetPeers("A", "B")

	fmt.Println("== 1. 会话 s1 在 A 写入 k=v1（写确认携带版本） ==")
	s1 := session.NewSession(sim, "s1")
	vv, _ := s1.Write("A", "k", "v1")
	fmt.Printf("    ack ver=%v, token=%v\n", vv, s1.Token.Versions["k"])
	printTrace(sim, 0)

	fmt.Println("== 2. 会话迁移到 B 读取：等待复制到达后返回新值（读己之写） ==")
	mark := len(sim.Trace)
	got, err := s1.Migrate().Read("B", "k", 50)
	fmt.Printf("    read(B) -> %q, err=%v (t=%d)\n", got, err, sim.Now)
	printTrace(sim, mark)

	fmt.Println("== 3. 故障注入丢弃 A->C 复制：读超时显式标记一致性未满足 ==")
	sim.Fault = func(mc session.MsgContext) (bool, int64) {
		if mc.From == "A" && mc.To == "C" {
			return true, 0
		}
		return false, 10
	}
	s2 := session.NewSession(sim, "s2")
	if _, err := s2.Write("A", "k2", "w"); err != nil {
		panic(err)
	}
	mark = len(sim.Trace)
	got, err = s2.Migrate().Read("C", "k2", 30)
	fmt.Printf("    read(C) -> %q, err=%v\n", got, err)
	if !errors.Is(err, session.ErrConsistencyTimeout) {
		panic("expected explicit consistency timeout")
	}
	printTrace(sim, mark)

	fmt.Println("== 4. A 崩溃并重启：令牌继续有效，不静默降级 ==")
	sim.Fault = nil
	mark = len(sim.Trace)
	a.Crash()
	a.Restart()
	got, err = s1.Migrate().Read("A", "k", 50)
	fmt.Printf("    read(A) after restart -> %q, err=%v\n", got, err)
	printTrace(sim, mark)

	fmt.Println("== 5. 令牌混用被拒绝 ==")
	s3 := session.NewSession(sim, "s3")
	s3.Token = s1.Token
	_, err = s3.Read("A", "k", 10)
	fmt.Printf("    s3 using s1's token -> err=%v\n", err)

	fmt.Println("DEMO OK")
}
