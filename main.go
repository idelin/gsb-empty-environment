package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

// 分组限流模拟器：从文件或标准输入读取场景 JSON，输出每轮许可序列与不变量验证报告。
// 若任一不变量（保底 / 总量 / 组内先来先服务）被破坏，进程以非零状态退出。
func main() {
	var file string
	flag.StringVar(&file, "f", "", "scenario file (default: read from stdin)")
	flag.Parse()

	var data []byte
	var err error
	if file == "" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(file)
	}
	if err != nil {
		fatal("read input: %v", err)
	}

	var sc Scenario
	if err := json.Unmarshal(data, &sc); err != nil {
		fatal("parse scenario: %v", err)
	}

	sim, err := NewSimulator(sc)
	if err != nil {
		fatal("invalid scenario: %v", err)
	}

	rounds := sim.Run(sc)
	out := Output{
		Rounds: rounds,
		Report: Verify(sc, rounds),
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatal("encode output: %v", err)
	}
	if !out.Report.OK() {
		os.Exit(1)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "groupsim: "+format+"\n", args...)
	os.Exit(2)
}
