// demo 演示异构数据类型转换管道：读取 MySQL 记录，
// 按预设映射规则输出规范化记录与数据清洗异常日志。
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"typepipe"
)

func main() {
	// 1. 按 MySQL 源表结构构建管道（源时区 +08:00）
	p, err := typepipe.NewPipelineFromSchema("mysql", []typepipe.Column{
		{Name: "id", Type: "bigint"},
		{Name: "is_active", Type: "tinyint(1)"},
		{Name: "created_at", Type: "datetime"},
		{Name: "status", Type: "enum('active','pending','banned')"},
		{Name: "score", Type: "smallint"},
		{Name: "uid", Type: "char(36)"},
	}, typepipe.Options{TZOffset: "+08:00"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "build pipeline:", err)
		os.Exit(1)
	}

	// 2. 模拟从 MySQL 读出的记录（第二条含各类坏字段）
	records := []map[string]any{
		{
			"id": int64(1), "is_active": int64(1),
			"created_at": "2024-03-01 10:30:00", "status": "pending",
			"score": int64(90), "uid": "550E8400E29B41D4A716446655440000",
		},
		{
			"id": int64(2), "is_active": int64(5), // tinyint 越域
			"created_at": "not-a-datetime", // 非法时间
			"status":     "ghost",          // 未登记枚举
			"score":      "99999",          // smallint 溢出
			"uid":        "xyz",            // 非法 UUID
		},
	}

	// 3. 转换并输出结果与清洗异常日志
	converted, anomalies := p.ProcessBatch(records)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	fmt.Println("== 转换结果 ==")
	_ = enc.Encode(converted)
	fmt.Println("== 数据清洗异常日志 ==")
	if len(anomalies) == 0 {
		fmt.Println("(无)")
	} else {
		_ = enc.Encode(anomalies)
	}
}
