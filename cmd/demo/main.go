// 运行样例: go run ./cmd/demo
package main

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"peritext"
)

func formatString(f map[string]string) string {
	if len(f) == 0 {
		return "(无格式)"
	}
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if f[k] == "true" {
			parts = append(parts, k)
		} else {
			parts = append(parts, k+"="+f[k])
		}
	}
	return strings.Join(parts, " + ")
}

func showChars(d *peritext.Doc) {
	for i, r := range d.Text() {
		fmt.Printf("  %c -> %s\n", r, formatString(d.FormatAt(i)))
	}
}

func showSpans(d *peritext.Doc) {
	for _, s := range d.Spans() {
		fmt.Printf("  %q : %s\n", s.Text, formatString(s.Format))
	}
}

func main() {
	fmt.Println("=== 验收场景: 并发 加粗(AB) vs 下划线(BC) ===")
	base := peritext.New("alice")
	ids := base.Insert(peritext.ID{}, "ABC")
	alice := base.Fork("alice")
	bob := base.Fork("bob")

	alice.AddMarkRange("bold", "true", peritext.DocStart, peritext.After(ids[1]))         // 用户1: AB 加粗
	bob.AddMarkRange("underline", "true", peritext.After(ids[0]), peritext.After(ids[2])) // 用户2: BC 下划线(并发)

	alice.Merge(bob)
	bob.Merge(alice)

	fmt.Printf("合并后文本: %q\n", alice.Text())
	showChars(alice)
	fmt.Println("连续格式区间(无断裂):")
	showSpans(alice)
	fmt.Printf("两副本收敛: %v\n", reflect.DeepEqual(alice.Spans(), bob.Spans()))

	fmt.Println()
	fmt.Println("=== 边界因果判定: 加粗 vs 取消加粗 ===")

	// 1) 因果: bob 看到加粗后再取消 -> 取消生效
	a1 := base.Fork("alice")
	a1.AddMarkRange("bold", "true", peritext.DocStart, peritext.After(ids[2]))
	b1 := a1.Fork("bob")
	b1.RemoveMarkRange("bold", peritext.DocStart, peritext.After(ids[2]))
	a1.Merge(b1)
	fmt.Println("因果(bob 先看到再取消) -> 取消生效:")
	showChars(a1)

	// 2) 并发: 互相没看到 -> 加粗确定性保留
	a2 := base.Fork("alice")
	b2 := base.Fork("bob")
	a2.AddMarkRange("bold", "true", peritext.DocStart, peritext.After(ids[2]))
	b2.RemoveMarkRange("bold", peritext.DocStart, peritext.After(ids[2]))
	a2.Merge(b2)
	b2.Merge(a2)
	fmt.Println("并发(互相没看到) -> 加粗保留, 两副本一致:")
	showChars(a2)
	fmt.Printf("两副本收敛: %v\n", reflect.DeepEqual(a2.Spans(), b2.Spans()))

	fmt.Println()
	fmt.Println("=== 超链接冲突: 重叠区间不同 URL ===")
	a3 := base.Fork("alice")
	b3 := base.Fork("bob")
	a3.AddMarkRange("link", "https://a.example", peritext.DocStart, peritext.After(ids[1]))
	b3.AddMarkRange("link", "https://b.example", peritext.After(ids[0]), peritext.After(ids[2]))
	a3.Merge(b3)
	b3.Merge(a3)
	showChars(a3)
	fmt.Printf("两副本收敛: %v\n", reflect.DeepEqual(a3.Spans(), b3.Spans()))
}
