package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}

func main() {
	inputPath := flag.String("input", "scenario.json", "输入 JSON 文件路径")
	doVerify := flag.Bool("verify", false, "对解析结果做小图穷举核验")
	flag.Parse()

	data, err := os.ReadFile(*inputPath)
	if err != nil {
		fatal(err)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal(fmt.Errorf("解析输入 JSON: %w", err))
	}

	r, err := NewResolver(&in)
	if err != nil {
		fatal(err)
	}
	if !r.Resolve() {
		fmt.Println("解析失败：没有任何装配能完成整图。")
		os.Exit(1)
	}
	printReport(r)

	if *doVerify {
		if err := Verify(&in); err != nil {
			fmt.Println("穷举核验失败:", err)
			os.Exit(1)
		}
		fmt.Println("穷举核验通过：解析结果与全枚举最优一致，所有高候选回退均有冲突依据。")
	}
}

func printReport(r *Resolver) {
	fmt.Println("========== 解析决策（按包名字节序） ==========")
	for i, pkg := range r.order {
		c := r.assigned[pkg]
		mark := ""
		if r.isPrivate[pkg] && c.source == SourcePublic {
			mark = "  ⚠ 私有包落入公源（依赖混淆）"
		}
		fmt.Printf("%d. %-14s -> %-8s [%s]%s\n", i+1, pkg, c.ver, c.source, mark)
	}

	fmt.Println()
	fmt.Println("========== 依赖混淆事件 ==========")
	confused := 0
	var stayed []string
	for _, pkg := range r.order {
		c := r.assigned[pkg]
		if !r.isPrivate[pkg] {
			continue
		}
		if c.source == SourcePublic {
			confused++
			extra := "（私源无此包）"
			if pmax, ok := r.maxVersion(pkg, SourcePrivate); ok {
				extra = fmt.Sprintf("（私源同名包最高版本 %s）", pmax)
			}
			prefix := ""
			if confused == 1 {
				prefix = "【第一次能被依赖混淆替代的安装】 "
			}
			fmt.Printf("%s%s@%s 来自 public %s\n", prefix, pkg, c.ver, extra)
		} else {
			stayed = append(stayed, fmt.Sprintf("%s@%s", pkg, c.ver))
		}
	}
	if confused == 0 {
		fmt.Println("无：所有私包均从私源解析。")
	} else {
		fmt.Printf("共 %d 个私包落入公源。\n", confused)
	}
	if len(stayed) > 0 {
		fmt.Printf("私包保持私源: %v\n", stayed)
	}

	fmt.Println()
	fmt.Println("========== 高候选回退冲突链 ==========")
	any := false
	for _, pkg := range r.order {
		rejs := r.Rejections[pkg]
		if len(rejs) == 0 {
			continue
		}
		any = true
		fmt.Printf("包 %s（最终选定 %s）：\n", pkg, r.assigned[pkg])
		for _, rej := range rejs {
			fmt.Printf("  ✗ %s[%s]:\n", rej.Ver, rej.Source)
			for _, ln := range rej.Chain {
				fmt.Printf("      %s\n", ln)
			}
		}
	}
	if !any {
		fmt.Println("无回退：每个包的最高候选均被直接采纳。")
	}
}
