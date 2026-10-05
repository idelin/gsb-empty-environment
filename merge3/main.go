package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

// splitLines 把文件内容切成行（不含换行符）。
// 文件以换行结尾时不产生多余的空行；空文件得到空切片。
func splitLines(data string) []string {
	if data == "" {
		return nil
	}
	lines := strings.Split(data, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// formatRange 把左闭右开区间格式化为 1 起始的闭区间说明。
func formatRange(label string, lo, hi int) string {
	if lo >= hi {
		return fmt.Sprintf("%s: 空（位于第 %d 行前）", label, lo+1)
	}
	return fmt.Sprintf("%s: 第 %d-%d 行", label, lo+1, hi)
}

// render 把分段结果渲染为带冲突标记的文本。
// 冲突标记中标注三方来源区间，返回 (文本, 是否含冲突)。
func render(segs []Segment, labelBase, labelLeft, labelRight string) (string, bool) {
	var b strings.Builder
	hasConflict := false
	for _, s := range segs {
		if !s.Conflict {
			for _, ln := range s.Lines {
				b.WriteString(ln)
				b.WriteByte('\n')
			}
			continue
		}
		hasConflict = true
		c := s.Chunk
		baseRange := formatRange(labelBase, c.BaseLo, c.BaseHi)
		leftRange := formatRange(labelLeft, c.LeftLo, c.LeftHi)
		rightRange := formatRange(labelRight, c.RightLo, c.RightHi)
		fmt.Fprintf(&b, "<<<<<<< %s（%s | %s）\n", labelLeft, leftRange, baseRange)
		for _, ln := range s.Left {
			b.WriteString(ln)
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "||||||| %s（%s）\n", labelBase, baseRange)
		for _, ln := range s.Base {
			b.WriteString(ln)
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "=======（%s）\n", rightRange)
		for _, ln := range s.Right {
			b.WriteString(ln)
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, ">>>>>>> %s（%s | %s）\n", labelRight, rightRange, baseRange)
	}
	return b.String(), hasConflict
}

func main() {
	labelBase := flag.String("label-base", "BASE", "基线侧标签")
	labelLeft := flag.String("label-left", "LEFT", "左侧标签")
	labelRight := flag.String("label-right", "RIGHT", "右侧标签")
	out := flag.String("o", "", "输出文件（默认标准输出）")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: merge3 [选项] <base> <left> <right>\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 3 {
		flag.Usage()
		os.Exit(2)
	}
	read := func(path string) []string {
		data, err := ioutil.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取 %s 失败: %v\n", path, err)
			os.Exit(2)
		}
		return splitLines(string(data))
	}
	base := read(flag.Arg(0))
	left := read(flag.Arg(1))
	right := read(flag.Arg(2))

	segs := Merge(base, left, right)
	text, hasConflict := render(segs, *labelBase, *labelLeft, *labelRight)

	if *out == "" {
		fmt.Print(text)
	} else if err := ioutil.WriteFile(*out, []byte(text), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "写入 %s 失败: %v\n", *out, err)
		os.Exit(2)
	}
	if hasConflict {
		os.Exit(1)
	}
}
