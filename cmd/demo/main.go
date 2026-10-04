// Command demo drives three replicas through local synthetic events and
// prints the live set, display value and conflict flag at each step.
package main

import (
	"encoding/json"
	"fmt"

	"example.com/ormap/ormap"
)

func show(m *ormap.Map, key string) {
	fmt.Printf("  [%s] 存活集合:", m.ReplicaID)
	for _, v := range m.Live(key) {
		fmt.Printf(" %q(dot=%s,t=%d)", v.Value, v.Dot, v.Time)
	}
	if disp, ok := m.Display(key); ok {
		fmt.Printf("  展示值=%q", disp.Value)
	} else {
		fmt.Print("  展示值=<无>")
	}
	fmt.Printf("  冲突=%v\n", m.Conflict(key))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	key := "k"
	a, b, c := ormap.NewMap("a"), ormap.NewMap("b"), ormap.NewMap("c")

	fmt.Println("== 1. 三副本并发赋值（逻辑时钟并列，replica ID 决胜展示值）")
	opA := a.Assign(key, "from-a")
	opB := b.Assign(key, "from-b")
	opC := c.Assign(key, "from-c")
	for _, m := range []*ormap.Map{a, b, c} {
		for _, op := range []ormap.Op{opA, opB, opC} {
			must(m.Apply(op))
		}
		show(m, key)
	}

	fmt.Println("== 2. 观察删除：a 只删除自己观察到的 dots，c 的并发赋值存活")
	d := ormap.NewMap("d")
	must(d.Apply(opA))
	must(d.Apply(opC)) // d 只见过 a 和 c 的赋值
	rm := d.Remove(key)
	fmt.Printf("  d 删除携带 dots=%v\n", rm.Dots)
	show(d, key)
	must(d.Apply(opB)) // 删除后才到达的并发赋值
	fmt.Println("  删除后才到达的 from-b（未被观察）:")
	show(d, key)

	fmt.Println("== 3. 状态合并（交换/结合/幂等）+ 重复投递")
	for _, m := range []*ormap.Map{a, b, c} {
		must(m.Apply(rm))
		must(m.Apply(rm)) // 重复投递
		must(m.Merge(d))
		show(m, key)
	}

	fmt.Println("== 4. checkpoint 导出/恢复，压缩墓碑拦截旧消息重放")
	cp := c.Export()
	data, _ := json.MarshalIndent(cp, "", "  ")
	fmt.Printf("  checkpoint: %s\n", data)
	r, err := ormap.Restore(cp)
	must(err)
	for _, op := range []ormap.Op{opA, opB, opC, rm} { // 重放全部旧消息
		must(r.Apply(op))
	}
	fmt.Println("  恢复后重放旧消息（被墓碑拦截，无复活）:")
	show(r, key)
	opNew := r.Assign(key, "after-restore")
	fmt.Printf("  恢复后新赋值 dot=%s（计数器不复用旧 dot）\n", opNew.Version.Dot)
	show(r, key)

	fmt.Println("== 5. 同 dot 不同内容拒绝")
	evil := opA
	evil.Version.Value = "forged"
	fresh := ormap.NewMap("e")
	must(fresh.Apply(opA)) // dot a:1 在此副本仍存活
	if err := fresh.Apply(evil); err != nil {
		fmt.Printf("  操作级拒绝: %v\n", err)
	}
	forged := ormap.NewMap("f")
	must(forged.Apply(evil))
	if err := fresh.Merge(forged); err != nil {
		fmt.Printf("  合并级拒绝: %v\n", err)
	}
}
