// demo：结构化 JSON 扁平化列式投影器运行样例。
//
// 场景：复杂嵌套的业务订单 JSON。先全量扁平化查看推断出的虚拟列，
// 再只投影 "user.address.city" 做城市维度统计——订单商品列表
// （items，每单 1000 个商品）等无关结构只被词法跳过，不反序列化。
package main

import (
	"fmt"
	"strings"

	"jsonproj"
)

func orderJSON(id, city string, items int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"id":%q,"paid":true,"user":{"name":"alice","address":{"city":%q,"zip":"100000"}},`, id, city)
	b.WriteString(`"tags":["vip"],"items":[`)
	for i := 0; i < items; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"sku":"SKU-%d","price":%d,"attrs":{"color":"red","size":"L"}}`, i, 10+i)
	}
	b.WriteString(`],"payment":{"method":"card","detail":{"last4":"4242","exp":"12/30"}}}`)
	return b.String()
}

func main() {
	var input []string
	cities := []string{"Beijing", "Shanghai", "Beijing", "Guangzhou", "Shanghai"}
	for i, c := range cities {
		input = append(input, orderJSON(fmt.Sprintf("ORD-%03d", i+1), c, 1000))
	}
	stream := strings.Join(input, "\n")

	// 1) 全量扁平化：自动推断字段路径 -> 独立虚拟列
	flat, err := columnar.Flatten(strings.NewReader(stream))
	if err != nil {
		panic(err)
	}
	fmt.Printf("== Flatten: %d rows, inferred virtual columns ==\n", flat.Rows())
	for _, p := range flat.Paths() {
		fmt.Printf("  %s\n", p)
	}

	// 2) 列式投影：只取 user.address.city 做城市维度统计
	proj, stats, err := columnar.Project(strings.NewReader(stream), []string{"user.address.city"})
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n== Project(user.address.city) ==\n")
	col, _ := proj.Column("user.address.city")
	counts := map[string]int{}
	for i, v := range col {
		fmt.Printf("  row%d -> %v\n", i, v)
		counts[v.(string)]++
	}
	fmt.Printf("  city counts: %v\n", counts)

	fmt.Printf("\n== Scan stats (只读相关列的证据) ==\n")
	fmt.Printf("  docs processed : %d\n", stats.Docs)
	fmt.Printf("  values captured: %d  (只有城市列被物化)\n", stats.ValuesCaptured)
	fmt.Printf("  tokens skipped : %d  (items 等 %d 个子树仅词法跳过，未反序列化)\n",
		stats.TokensSkipped, len(stats.SkippedSubtrees))
}
