package columnar

import (
	"encoding/json"
	"io"
	"strings"
)

// Stats 记录一次投影扫描的物化/跳过情况，用于验证“只读相关列”。
type Stats struct {
	Docs            int      // 处理的文档数
	ValuesCaptured  int      // 实际物化的值个数
	TokensSkipped   int      // 仅词法跳过、未反序列化的 token 数
	SkippedSubtrees []string // 被整体跳过的子树根路径
}

// Flatten 全量模式：推断文档流中出现的所有字段路径，
// 把每条路径的值抽取为独立虚拟列。
// r 中可包含多篇首尾相接的 JSON 文档（如 NDJSON）。
func Flatten(r io.Reader) (*Store, error) {
	store, _, err := run(r, matchAllSelector())
	return store, err
}

// Project 投影模式：只抽取 paths 指定的列。
// 无关子树仅做词法跳过，不会被反序列化为 Go 值；
// 返回的 Stats 可用于验证跳过行为。
func Project(r io.Reader, paths []string) (*Store, *Stats, error) {
	return run(r, newSelector(paths))
}

func run(r io.Reader, sel *selector) (*Store, *Stats, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber() // 保留数字字面量，避免大整数精度损失

	stats := &Stats{}
	store := newStore()

	for dec.More() {
		row := make(map[string]any)
		emit := func(path string, v any) {
			if strings.Contains(path, "[]") {
				// 数组元素路径：同一行内聚合为切片
				cur, _ := row[path].([]any)
				row[path] = append(cur, v)
			} else {
				row[path] = v
			}
		}
		if err := walk(dec, "", sel, emit, stats); err != nil {
			return nil, nil, err
		}
		store.addRow(row)
		stats.Docs++
	}
	return store, stats, nil
}
