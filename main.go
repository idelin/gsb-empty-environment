package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

// splitLines 把文件内容切成行（不含换行符）。兼容 CRLF，
// 末尾的换行符不产生多余的空行。
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.Replace(s, "\r\n", "\n", -1)
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// formatRange 把半开区间 [lo,hi) 格式化为 1 基闭区间 "start-end"。
// 当 start > end 时表示空区间（在该 start 行之前的插入点）。
func formatRange(lo, hi int) string {
	return fmt.Sprintf("%d-%d", lo+1, hi)
}

func readLines(path string) []string {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取文件失败:", err)
		os.Exit(2)
	}
	return splitLines(string(data))
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintf(os.Stderr, "用法: %s <基线文件> <修改稿A> <修改稿B>\n", os.Args[0])
		os.Exit(2)
	}
	basePath, leftPath, rightPath := os.Args[1], os.Args[2], os.Args[3]
	base := readLines(basePath)
	left := readLines(leftPath)
	right := readLines(rightPath)

	segs := Merge(base, left, right)

	var out strings.Builder
	conflicts := 0
	for _, s := range segs {
		if s.Conflict == nil {
			for _, l := range s.Lines {
				out.WriteString(l)
				out.WriteByte('\n')
			}
			continue
		}
		c := s.Conflict
		conflicts++
		fmt.Fprintf(&out, "<<<<<<< LEFT %s:%s | BASE %s:%s | RIGHT %s:%s\n",
			leftPath, formatRange(c.LeftLo, c.LeftHi),
			basePath, formatRange(c.BaseLo, c.BaseHi),
			rightPath, formatRange(c.RightLo, c.RightHi))
		for _, l := range c.LeftLines {
			out.WriteString(l)
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, "||||||| BASE %s:%s\n", basePath, formatRange(c.BaseLo, c.BaseHi))
		for _, l := range c.BaseLines {
			out.WriteString(l)
			out.WriteByte('\n')
		}
		out.WriteString("=======\n")
		for _, l := range c.RightLines {
			out.WriteString(l)
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, ">>>>>>> RIGHT %s:%s\n", rightPath, formatRange(c.RightLo, c.RightHi))
	}
	os.Stdout.WriteString(out.String())

	if conflicts > 0 {
		fmt.Fprintf(os.Stderr, "存在 %d 处冲突\n", conflicts)
		os.Exit(1)
	}
}
