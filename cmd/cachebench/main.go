// cachebench 运行高并发缓存压力场景并输出 JSON 健康度评估报告。
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"cachebench"
)

func main() {
	cfg := cachebench.DefaultStressConfig()
	flag.IntVar(&cfg.Capacity, "capacity", cfg.Capacity, "cache capacity (entries)")
	flag.IntVar(&cfg.Workers, "workers", cfg.Workers, "concurrent workers")
	flag.IntVar(&cfg.HotKeys, "hot-keys", cfg.HotKeys, "hot key set size")
	phaseMs := flag.Int("phase-ms", 300, "duration of each phase in milliseconds")
	windowMs := flag.Int("window-ms", 10, "metrics window size in milliseconds")
	flag.Parse()

	cfg.Phase = time.Duration(*phaseMs) * time.Millisecond
	cfg.Window = time.Duration(*windowMs) * time.Millisecond

	report := cachebench.RunStressScenario(cfg)
	data, err := report.JSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal report:", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}
