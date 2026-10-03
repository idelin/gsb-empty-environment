// 演示：两个用户并发编辑同一段文字的格式，合并后逐字符查看结果。
package main

import (
	"fmt"
	"sort"
	"strings"

	"peritext"
)

func printFormats(title string, d *peritext.Document) {
	fmt.Println(title)
	text := []rune(d.Text())
	fm := d.Formats()
	for i, ch := range text {
		keys := make([]string, 0, len(fm[i]))
		for k, v := range fm[i] {
			keys = append(keys, k+"="+v)
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			keys = []string{"(无格式)"}
		}
		fmt.Printf("  %c  %s\n", ch, strings.Join(keys, "  "))
	}
	fmt.Println()
}

func main() {
	// 场景 1（验收边界）：用户1 给 AB 加粗，用户2 并发给 BC 加下划线。
	alice := peritext.New("用户1")
	alice.InsertText(0, "ABC")
	bob := peritext.New("用户2")
	bob.Merge(alice) // 双方先同步到同一段文字 "ABC"

	alice.MarkRange("bold", "true", 0, 2)    // 用户1: AB 加粗
	bob.MarkRange("underline", "true", 1, 3) // 用户2: BC 下划线（并发）

	alice.Merge(bob)
	bob.Merge(alice)

	printFormats("场景 1：AB 加粗 与 BC 下划线 并发合并后（双方一致）：", alice)

	// 场景 2：A 加粗、B 并发取消加粗 —— 按 (Lamport, Actor) 确定性裁决。
	a2 := peritext.New("alice")
	a2.InsertText(0, "ABC")
	b2 := peritext.New("bob")
	b2.Merge(a2)

	a2.MarkRange("bold", "true", 0, 3)  // alice: 全部加粗
	b2.MarkRange("bold", "false", 0, 3) // bob: 并发取消加粗

	a2.Merge(b2)
	b2.Merge(a2)
	printFormats("场景 2：并发 加粗 vs 取消加粗（确定性裁决，bob 胜出）：", a2)

	// 场景 3：B 先看到加粗、再取消 —— 因果在后必胜。
	a3 := peritext.New("alice")
	a3.InsertText(0, "ABC")
	b3 := peritext.New("bob")
	b3.Merge(a3)

	a3.MarkRange("bold", "true", 0, 3)
	b3.Merge(a3)                        // bob 先看到加粗
	b3.MarkRange("bold", "false", 0, 3) // 再取消

	a3.Merge(b3)
	b3.Merge(a3)
	printFormats("场景 3：先加粗、后取消（因果在后必胜）：", a3)
}
