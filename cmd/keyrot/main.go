package main

import (
	"fmt"
	"os"

	netsec "example.org/netsec/q0483"
)

// 运行完整模型（至多两次轮换，消息可丢失可重复），打印最短违例轨迹。
func main() {
	res, err := netsec.Explore(netsec.DefaultConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, "explore:", err)
		os.Exit(1)
	}
	fmt.Printf("reachable states before violation: %d\n", res.Reachable)
	if res.Violation == nil {
		fmt.Println("no violation: every delivered plaintext uses a mutually acknowledged key generation")
		return
	}
	fmt.Printf("shortest violation trace (%d steps):\n", res.Violation.Depth())
	for i, step := range res.Violation.Trace {
		fmt.Printf("  %2d. %s\n", i+1, step.Name)
	}
}
