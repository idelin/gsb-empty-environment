package main

import "fmt"

func main() {
	r := RunFailoverScenario()
	fmt.Println("=== 事件日志 (单进程事件模拟器, 虚拟时间) ===")
	for _, line := range r.Sim.Log() {
		fmt.Println(line)
	}
	fmt.Println()
	fmt.Println("=== 安全性证据 ===")
	fmt.Print(r.SafetyEvidence())
	fmt.Println()
	fmt.Println("=== 活性证据 ===")
	fmt.Print(r.LivenessEvidence())
}
