package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	instancePath := flag.String("i", "instance.json", "实例 JSON 文件路径")
	flag.Parse()

	inst, err := loadInstance(*instancePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载实例失败:", err)
		os.Exit(1)
	}
	if err := inst.validate(); err != nil {
		fmt.Fprintln(os.Stderr, "实例不合法:", err)
		os.Exit(1)
	}
	fmt.Printf("实例: %s（物品 %d，仓位 %d，场景 %d）\n\n", inst.Name, inst.Items, inst.Bins, len(inst.Scenarios))

	st := runStatic(inst)
	runRecourse(inst, st)
}
