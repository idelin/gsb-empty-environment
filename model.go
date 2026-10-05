package main

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ---------- Spec（输入描述） ----------

type Spec struct {
	Tables      []TableSpec    `json:"tables"`
	ForeignKeys []FKSpec       `json:"foreign_keys"`
	Conflicts   []ConflictSpec `json:"conflicts"`
}

type TableSpec struct {
	Name       string     `json:"name"`
	UniqueKeys [][]string `json:"unique_keys"`
	Rows       []RowSpec  `json:"rows"`
}

type RowSpec struct {
	ID     string                 `json:"id"`
	Weight *int64                 `json:"weight"`
	Fields map[string]interface{} `json:"fields"`
}

type FKSpec struct {
	Name        string `json:"name"`
	ChildTable  string `json:"child_table"`
	ChildField  string `json:"child_field"`
	ParentTable string `json:"parent_table"`
	ParentField string `json:"parent_field"`
}

func (fk *FKSpec) DisplayName() string {
	if fk.Name != "" {
		return fk.Name
	}
	return fmt.Sprintf("%s.%s→%s.%s", fk.ChildTable, fk.ChildField, fk.ParentTable, fk.ParentField)
}

type ConflictSpec struct {
	Name string   `json:"name"`
	Rows []string `json:"rows"` // "table#id"，同一集合内至多保留一行
}

// ---------- Model（求解内部表示） ----------

type Row struct {
	Index  int
	Table  string
	ID     string
	Weight int64
	Fields map[string]interface{}
}

func (r *Row) Ref() string { return r.Table + "#" + r.ID }

// UniqueGroup 表示某个唯一键取值相同的行集合（成员数 >= 2 才是约束）。
type UniqueGroup struct {
	Table   string
	Key     []string
	Value   string
	Members []int
}

type ConflictSet struct {
	Name string
	Rows []int
}

type Model struct {
	Rows         []*Row
	Groups       []*UniqueGroup
	groupsOf     [][]*UniqueGroup
	FKs          []*FKSpec
	fkCandidates [][][]int // [fkIdx][rowIdx] -> 候选父行；nil 表示该行不是该外键的子行
	Conflicts    []*ConflictSet
	conflictsOf  [][]int
	TotalWeight  int64
}

func ParseSpec(data []byte) (*Spec, error) {
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("解析 spec JSON 失败: %v", err)
	}
	return &spec, nil
}

func canonicalValue(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func BuildModel(spec *Spec, maxRows int) (*Model, error) {
	m := &Model{}
	tableSeen := map[string]bool{}
	refIndex := map[string]int{}
	rowCount := 0

	for ti := range spec.Tables {
		t := &spec.Tables[ti]
		if t.Name == "" {
			return nil, fmt.Errorf("存在未命名表")
		}
		if tableSeen[t.Name] {
			return nil, fmt.Errorf("表 %q 重复定义", t.Name)
		}
		tableSeen[t.Name] = true
		rowSeen := map[string]bool{}
		for ri := range t.Rows {
			rs := &t.Rows[ri]
			if rs.ID == "" {
				return nil, fmt.Errorf("表 %q 存在缺少 id 的行", t.Name)
			}
			if rowSeen[rs.ID] {
				return nil, fmt.Errorf("表 %q 中行 %q 重复定义", t.Name, rs.ID)
			}
			rowSeen[rs.ID] = true
			w := int64(1)
			if rs.Weight != nil {
				w = *rs.Weight
				if w < 0 {
					return nil, fmt.Errorf("行 %s#%s 权重为负数", t.Name, rs.ID)
				}
			}
			row := &Row{Index: len(m.Rows), Table: t.Name, ID: rs.ID, Weight: w, Fields: rs.Fields}
			if row.Fields == nil {
				row.Fields = map[string]interface{}{}
			}
			refIndex[row.Ref()] = row.Index
			m.Rows = append(m.Rows, row)
			m.TotalWeight += w
		}
		rowCount += len(t.Rows)
	}
	if maxRows > 0 && rowCount > maxRows {
		return nil, fmt.Errorf("行数 %d 超过上限 %d（可用 -max-rows 调整，但问题规模设计上限为 25 行）", rowCount, maxRows)
	}

	// 唯一键分组
	m.groupsOf = make([][]*UniqueGroup, len(m.Rows))
	for ti := range spec.Tables {
		t := &spec.Tables[ti]
		for _, key := range t.UniqueKeys {
			if len(key) == 0 {
				return nil, fmt.Errorf("表 %q 存在空唯一键", t.Name)
			}
			byVal := map[string][]int{}
			for ri := range t.Rows {
				idx := refIndex[t.Name+"#"+t.Rows[ri].ID]
				vals := make([]interface{}, len(key))
				for i, f := range key {
					vals[i] = m.Rows[idx].Fields[f]
				}
				cv := canonicalValue(vals)
				byVal[cv] = append(byVal[cv], idx)
			}
			var vals []string
			for v := range byVal {
				vals = append(vals, v)
			}
			sort.Strings(vals)
			for _, v := range vals {
				members := byVal[v]
				if len(members) < 2 {
					continue
				}
				g := &UniqueGroup{Table: t.Name, Key: key, Value: v, Members: members}
				m.Groups = append(m.Groups, g)
				for _, mi := range members {
					m.groupsOf[mi] = append(m.groupsOf[mi], g)
				}
			}
		}
	}

	// 外键：为每个子行解析候选父行
	m.fkCandidates = make([][][]int, len(spec.ForeignKeys))
	for fi := range spec.ForeignKeys {
		fk := &spec.ForeignKeys[fi]
		if !tableSeen[fk.ChildTable] {
			return nil, fmt.Errorf("外键 %q: 未知子表 %q", fk.DisplayName(), fk.ChildTable)
		}
		if !tableSeen[fk.ParentTable] {
			return nil, fmt.Errorf("外键 %q: 未知父表 %q", fk.DisplayName(), fk.ParentTable)
		}
		parentByVal := map[string][]int{}
		for _, row := range m.Rows {
			if row.Table != fk.ParentTable {
				continue
			}
			v, ok := row.Fields[fk.ParentField]
			if !ok {
				continue
			}
			cv := canonicalValue(v)
			parentByVal[cv] = append(parentByVal[cv], row.Index)
		}
		m.FKs = append(m.FKs, fk)
		m.fkCandidates[fi] = make([][]int, len(m.Rows))
		for _, row := range m.Rows {
			if row.Table != fk.ChildTable {
				continue
			}
			v, ok := row.Fields[fk.ChildField]
			if !ok || v == nil {
				continue // SQL 语义：NULL 外键表示无引用，不施加约束
			}
			cands := parentByVal[canonicalValue(v)]
			if cands == nil {
				cands = []int{}
			}
			m.fkCandidates[fi][row.Index] = cands
		}
	}

	// 行间冲突（团语义：同一集合内至多保留一行）
	m.conflictsOf = make([][]int, len(m.Rows))
	for ci := range spec.Conflicts {
		c := &spec.Conflicts[ci]
		set := &ConflictSet{Name: c.Name}
		if set.Name == "" {
			set.Name = fmt.Sprintf("conflict-%d", ci+1)
		}
		seen := map[int]bool{}
		for _, ref := range c.Rows {
			idx, ok := refIndex[ref]
			if !ok {
				return nil, fmt.Errorf("冲突规则 %q 引用了未知行 %q", set.Name, ref)
			}
			if seen[idx] {
				continue
			}
			seen[idx] = true
			set.Rows = append(set.Rows, idx)
		}
		if len(set.Rows) < 2 {
			return nil, fmt.Errorf("冲突规则 %q 至少需要两个不同的行", set.Name)
		}
		m.Conflicts = append(m.Conflicts, set)
		for _, mi := range set.Rows {
			m.conflictsOf[mi] = append(m.conflictsOf[mi], len(m.Conflicts)-1)
		}
	}
	return m, nil
}
