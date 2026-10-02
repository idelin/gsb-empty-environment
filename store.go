// Package columnar 实现结构化 JSON 扁平化列式投影器。
//
// 核心思想：把半结构化 JSON 文档流按“字段路径”拆解成独立的虚拟列
// （map[path][]value），查询只关心少数叶子路径时，通过流式
// json.Decoder 只物化相关列，无关子树仅做词法跳过（skip），
// 不反序列化为任何 Go 值。
package columnar

import "sort"

// Store 是列式存储：每个字段路径一列，每行对应一篇输入文档。
type Store struct {
	columns map[string][]any
	rows    int
}

func newStore() *Store {
	return &Store{columns: make(map[string][]any)}
}

// Rows 返回行数（即输入文档数）。
func (s *Store) Rows() int { return s.rows }

// Paths 返回已推断出的全部字段路径（字典序）。
func (s *Store) Paths() []string {
	paths := make([]string, 0, len(s.columns))
	for p := range s.columns {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// Column 返回某条路径对应的整列值；路径不存在时 ok=false。
func (s *Store) Column(path string) (col []any, ok bool) {
	col, ok = s.columns[path]
	return col, ok
}

// MustColumn 同 Column，不存在时返回 nil。
func (s *Store) MustColumn(path string) []any { return s.columns[path] }

// addRow 把一篇文档抽取出的 (path -> value) 写入列存。
// 对本文档缺失的列补 nil，对首次出现的列向前回填 nil，保证所有列等长。
func (s *Store) addRow(vals map[string]any) {
	for p := range s.columns {
		if _, ok := vals[p]; !ok {
			s.columns[p] = append(s.columns[p], nil)
		}
	}
	for p, v := range vals {
		col, ok := s.columns[p]
		if !ok {
			col = make([]any, s.rows) // 回填历史行为 nil
		}
		s.columns[p] = append(col, v)
	}
	s.rows++
}
