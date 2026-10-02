// demo 运行样例：同一份员工名册，按三种角色分别导出。
package main

import (
	"crypto/rand"
	"fmt"

	"compliance-export/compliance"
)

func main() {
	roster := []compliance.Employee{
		{ID: "E001", Name: "张伟", Dept: "研发", Title: "高级工程师", Phone: "13812345678", IDCard: "110101199003077788", Salary: 23000},
		{ID: "E002", Name: "李娜", Dept: "财务", Title: "会计", Phone: "13998765432", IDCard: "310104198511123456", Salary: 14500},
		{ID: "E003", Name: "张伟", Dept: "研发", Title: "测试工程师", Phone: "13700001111", IDCard: "440305199912010000", Salary: 12000},
		{ID: "E004", Name: "王强", Dept: "市场", Title: "经理", Phone: "13655556666", IDCard: "500103198802029999", Salary: 28000},
	}

	// 每次导出生成独立的会话密钥：假名/噪声仅在本导出内一致。
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	exporter := compliance.NewExporter(key)

	for _, role := range []compliance.Role{
		compliance.RoleHRAdmin,
		compliance.RoleFinance,
		compliance.RoleContractor,
	} {
		pol := compliance.DefaultPolicies()[role]
		fmt.Printf("=== 角色: %s (合规级别 L%d) ===\n", role, pol.Level)
		csvData, err := exporter.ExportCSV(roster, pol)
		if err != nil {
			panic(err)
		}
		fmt.Print(string(csvData))
		fmt.Println()
	}
}
