package main

import (
	"fmt"

	"signalguard"
)

func main() {
	fmt.Println("=== 1. healthy reactive graph ===")
	healthy()

	fmt.Println()
	fmt.Println("=== 2. malicious self-write loop (guard demo) ===")
	malicious()
}

func healthy() {
	g := signalguard.NewGraph()
	count := signalguard.NewSignal(g, "count", 0)
	double := signalguard.NewComputed(g, "double", func() int { return count.Get() * 2 })
	signalguard.NewEffect(g, "log", func() {
		fmt.Printf("count=%d double=%d\n", count.Get(), double.Get())
	})
	for i := 1; i <= 3; i++ {
		count.Set(i)
	}
}

func malicious() {
	g := signalguard.NewGraph() // iteration limit: 50
	a := signalguard.NewSignal(g, "A", 0)
	defer func() {
		r := recover()
		if r == nil {
			fmt.Println("no panic (unexpected!)")
			return
		}
		if err, ok := r.(*signalguard.CircularSignalUpdate); ok {
			fmt.Printf("guard intercepted the dead loop:\n%s\n", err)
			return
		}
		panic(r)
	}()
	// 恶意逻辑: Effect 读取 Signal A, 同时无条件写回 Signal A。
	signalguard.NewEffect(g, "inc", func() {
		a.Set(a.Get() + 1)
	})
}
