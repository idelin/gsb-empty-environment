// demo 演示不同角色在同一员工名册上的合规导出结果。
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"privacy-export/masking"
)

func main() {
	ps, err := masking.ParsePolicySet(masking.DefaultPolicyJSON)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exporter := masking.NewExporter(ps).WithRandSeed(42)
	roster := masking.SampleRoster()

	cases := []struct{ role, level, desc string }{
		{"hr_admin", "internal", "HR 管理员：完整名册，手机号/身份证掩码"},
		{"finance", "confidential", "财务：薪资明细可见，手机号保留前3后4，身份证遮蔽"},
		{"contractor", "restricted", "外包：姓名/工号假名化，薪资区间化，联系方式遮蔽"},
		{"analyst", "statistical", "分析师：工号假名化，薪资差分加噪"},
	}
	for _, c := range cases {
		out, err := exporter.Export(c.role, c.level, roster)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("=== 角色 %-10s 合规级别 %-12s | %s ===\n", c.role, c.level, c.desc)
		pretty, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(pretty))
		fmt.Println()
	}
}
