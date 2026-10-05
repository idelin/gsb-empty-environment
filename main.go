// gsq：灰度量化求解器。
// 从标准输入读取直方图（每行 "灰度值 像素数"，# 开头为注释），
// 将非空灰度级压成 K 个整数代表值：先最小化最大绝对误差，再最小化加权平方误差。
// 输出分组边界与调色板。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

func readHistogram() (map[int]int64, error) {
	hist := make(map[int]int64)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var v, c int64
		if _, err := fmt.Sscanf(line, "%d %d", &v, &c); err != nil {
			return nil, fmt.Errorf("第 %d 行格式错误（应为 \"灰度值 像素数\"）: %q", lineNo, line)
		}
		if v < 0 || v > 255 {
			return nil, fmt.Errorf("第 %d 行灰度值 %d 超出 [0,255]", lineNo, v)
		}
		if c < 0 {
			return nil, fmt.Errorf("第 %d 行像素数为负", lineNo)
		}
		hist[int(v)] += c
	}
	return hist, sc.Err()
}

func main() {
	k := flag.Int("k", 0, "分组数 / 调色板大小（必填）")
	flag.Parse()
	if *k <= 0 {
		fmt.Fprintln(os.Stderr, "用法: gsq -k <组数> < 直方图.txt（每行: 灰度值 像素数）")
		os.Exit(2)
	}
	hist, err := readHistogram()
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取直方图失败:", err)
		os.Exit(1)
	}
	var levels []int
	for v, c := range hist {
		if c > 0 { // 空灰度无需强制分组
			levels = append(levels, v)
		}
	}
	sort.Ints(levels)
	if len(levels) == 0 {
		fmt.Println("直方图为空，无灰度级需要量化。")
		return
	}
	w := make([]int64, len(levels))
	for i, v := range levels {
		w[i] = hist[v]
	}

	res := Solve(levels, w, *k)

	var total int64
	for _, c := range hist {
		total += c
	}
	fmt.Printf("灰度级数: %d  像素总数: %d  分组数: %d\n", len(levels), total, len(res.Groups))
	fmt.Printf("最小化后的最大绝对误差: %d\n", res.MaxErr)
	fmt.Printf("该约束下的最小加权平方误差: %d\n", res.SSE)
	fmt.Println("\n分组边界与调色板:")
	fmt.Printf("%-6s %-12s %-8s %-12s %-10s %s\n", "组", "灰度范围", "代表值", "像素数", "最大误差", "平方误差")
	for i, g := range res.Groups {
		fmt.Printf("%-6d [%3d,%3d]   %-8d %-12d %-10d %d\n",
			i+1, g.Lo, g.Hi, g.Rep, g.Count, g.MaxErr, g.SSE)
	}
}
