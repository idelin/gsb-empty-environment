// bgpstable 是 BGP 稳定路径问题 (SPP) 分析控制面命令行：
// 输入面向目标前缀 d 的 AS 图与严格偏好序，输出 Gao-Rexford 核验、
// 全部稳定路径分配、争议轮结构与最小策略调整集合。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"prescreen.local/ps-f69a1f50e0/pkg/spp"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "bgpstable:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("bgpstable", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "输出 JSON 报告（默认输出中文文本报告）")
	skipRepair := fs.Bool("skip-repair", false, "跳过最小策略调整搜索")
	maxRepair := fs.Int("max-repair", spp.DefaultMaxRepairSize, "最小修复动作集合大小上限")
	onlyVF := fs.Bool("require-valley-free", false, "修复方案必须同时恢复严格无谷模型")
	wheelLimit := fs.Int("wheel-limit", spp.DefaultWheelLimit, "输出争议轮的数量上限")
	if err := fs.Parse(args); err != nil {
		return err
	}
	files := fs.Args()
	if len(files) == 0 {
		return fmt.Errorf("用法: bgpstable [选项] <instance.json> [更多实例...]")
	}

	var reports []*spp.Report
	for _, path := range files {
		cfg, err := spp.LoadConfigFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		report, err := spp.Analyze(cfg, spp.AnalysisOptions{
			WheelLimit: *wheelLimit,
			SkipRepair: *skipRepair,
			Repair: spp.RepairOptions{
				MaxSize:        *maxRepair,
				MaxWheelLimit:  *wheelLimit,
				OnlyValleyFree: *onlyVF,
			},
		})
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		report.InstanceName = filepath.Base(path)
		reports = append(reports, report)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if len(reports) == 1 {
			return enc.Encode(reports[0])
		}
		return enc.Encode(reports)
	}
	for i, r := range reports {
		if i > 0 {
			fmt.Println()
		}
		fmt.Print(r.RenderText())
	}
	return nil
}
