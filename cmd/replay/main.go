// replay 读取事件脚本并确定性重放，输出每一步后的仲裁器状态。
//
// 用法: go run ./cmd/replay scripts/demo.events
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"leasearb"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: replay <script.events>")
		os.Exit(2)
	}
	data, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := leasearb.NewRunner(start, os.Stdout)
	if err := r.Run(string(data)); err != nil {
		fmt.Fprintln(os.Stderr, "replay failed:", err)
		os.Exit(1)
	}
	lease, active := r.Arbiter().Snapshot()
	fmt.Printf("\nfinal: %s active=%v\n", lease, active)
}
