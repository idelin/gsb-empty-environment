package main

import (
	"encoding/json"
	"fmt"
	"sort"
)

// MaxRows 是题目允许的总行数上限。
const MaxRows = 25

// RowRef 通过 表名+行ID 定位一行。
type RowRef struct {
	Table string `json:"table"`
	Row   string `json:"row"`
}

func (r RowRef) String() string { return r.Table + "." + r.Row }

// Row 是输入中的一行数据。Weight 缺省为 1。
type Row struct {
	ID     string                 `json:"id"`
	Weight *int64                 `json:"weight,omitempty"`
	Values map[string]interface{} `json:"values"`
}

// Table 描述一张表及其唯一键。
type Table struct {
	Name       string     `json:"name"`
	UniqueKeys [][]string `json:"unique_keys"`
	Rows       []Row      `json:"rows"`
}

// ForeignKey 描述 子表.列 -> 父表.列 的引用关系。
type ForeignKey struct {
	ChildTable   string `json:"child_table"`
	ChildColumn  string `json:"child_column"`
	ParentTable  string `json:"parent_table"`
	ParentColumn string `json:"parent_column"`
}

func (f ForeignKey) String() string {
	return fmt.Sprintf("%s.%s -> %s.%s", f.ChildTable, f.ChildColumn, f.ParentTable, f.ParentColumn)
}

// Conflict 是用户指定的行间冲突：两行不得同时保留。
type Conflict struct {
	A      RowRef `json:"a"`
	B      RowRef `json:"b"`
	Reason string `json:"reason"`
}

// Input 是修复器的完整输入。
type Input struct {
	Tables      []Table      `json:"tables"`
	ForeignKeys []ForeignKey `json:"foreign_keys"`
	Conflicts   []Conflict   `json:"conflicts"`
}

// fkReq 是一条外键需求：child 若保留，candidates 中至少一行须保留。
// candidates 为空表示悬空引用（被引用的父行根本不存在），child 必须删除。
type fkReq struct {
	child      int
	candidates []int
	fk         ForeignKey
	childValue string
}

// ukGroup 是某个唯一键下取值相同的一组行（组内至多保留一行）。
type ukGroup struct {
	table    string
	keyIndex int
	columns  []string
	members  []int
}

// model 是把 Input 编译后的内部表示：全部约束归约为
// 「互斥对」与「蕴含（保留则要求候选集非空保留）」两类，交给统一的全局求解器。
type model struct {
	rowRef        []RowRef
	weight        []int64
	index         map[RowRef]int
	mutex         []uint32 // mutex[i] 的第 j 位置位表示 i 与 j 不得同时保留
	reqs          []fkReq
	dangling      []bool
	groups        []ukGroup
	conflictPairs [][2]int
	conflictText  map[[2]int]string
	fks           []ForeignKey
	tables        []Table
}

func canonicalValue(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// isNull 遵循 SQL 语义：NULL（或缺失）不参与唯一键与外键约束。
func isNull(v interface{}) bool { return v == nil }

// BuildModel 校验输入并把各类约束编译为统一模型。
func BuildModel(in *Input) (*model, error) {
	m := &model{index: make(map[RowRef]int), conflictText: make(map[[2]int]string)}
	tableByName := make(map[string]int)

	total := 0
	for ti := range in.Tables {
		t := &in.Tables[ti]
		if t.Name == "" {
			return nil, fmt.Errorf("存在未命名的表")
		}
		if _, dup := tableByName[t.Name]; dup {
			return nil, fmt.Errorf("表名重复: %s", t.Name)
		}
		tableByName[t.Name] = ti
		total += len(t.Rows)
	}
	if total == 0 {
		return nil, fmt.Errorf("输入不包含任何行")
	}
	if total > MaxRows {
		return nil, fmt.Errorf("总行数 %d 超过上限 %d：本修复器面向小规模精确求解", total, MaxRows)
	}

	for ti := range in.Tables {
		t := &in.Tables[ti]
		seen := make(map[string]bool)
		for ri := range t.Rows {
			r := &t.Rows[ri]
			if r.ID == "" {
				return nil, fmt.Errorf("表 %s 存在未命名行", t.Name)
			}
			if seen[r.ID] {
				return nil, fmt.Errorf("表 %s 中行 ID 重复: %s", t.Name, r.ID)
			}
			seen[r.ID] = true
			w := int64(1)
			if r.Weight != nil {
				w = *r.Weight
			}
			if w < 0 {
				return nil, fmt.Errorf("行 %s.%s 的删除权重为负: %d", t.Name, r.ID, w)
			}
			ref := RowRef{Table: t.Name, Row: r.ID}
			m.index[ref] = len(m.rowRef)
			m.rowRef = append(m.rowRef, ref)
			m.weight = append(m.weight, w)
		}
	}
	n := len(m.rowRef)
	m.mutex = make([]uint32, n)
	m.dangling = make([]bool, n)

	// 唯一键 -> 互斥对 + 分组（供原因与核验使用）
	for ti := range in.Tables {
		t := &in.Tables[ti]
		for ki, cols := range t.UniqueKeys {
			if len(cols) == 0 {
				return nil, fmt.Errorf("表 %s 的第 %d 个唯一键不包含任何列", t.Name, ki)
			}
			byKey := make(map[string][]int)
			for ri := range t.Rows {
				r := &t.Rows[ri]
				skip := false
				key := ""
				for _, c := range cols {
					v, ok := r.Values[c]
					if !ok || isNull(v) {
						skip = true // SQL 语义：含 NULL 的行不参与唯一约束
						break
					}
					key += canonicalValue(v) + "\x00"
				}
				if skip {
					continue
				}
				gi := m.index[RowRef{Table: t.Name, Row: r.ID}]
				byKey[key] = append(byKey[key], gi)
			}
			for _, members := range byKey {
				if len(members) < 2 {
					continue
				}
				sort.Ints(members)
				m.groups = append(m.groups, ukGroup{table: t.Name, keyIndex: ki, columns: cols, members: members})
				for a := 0; a < len(members); a++ {
					for b := a + 1; b < len(members); b++ {
						i, j := members[a], members[b]
						m.mutex[i] |= 1 << uint(j)
						m.mutex[j] |= 1 << uint(i)
					}
				}
			}
		}
	}

	// 指定行间冲突 -> 互斥对
	for ci := range in.Conflicts {
		c := &in.Conflicts[ci]
		i, ok := m.index[c.A]
		if !ok {
			return nil, fmt.Errorf("冲突 %d 引用了不存在的行 %s", ci, c.A)
		}
		j, ok := m.index[c.B]
		if !ok {
			return nil, fmt.Errorf("冲突 %d 引用了不存在的行 %s", ci, c.B)
		}
		if i == j {
			return nil, fmt.Errorf("冲突 %d 的两端是同一行 %s", ci, c.A)
		}
		if i > j {
			i, j = j, i
		}
		m.mutex[i] |= 1 << uint(j)
		m.mutex[j] |= 1 << uint(i)
		m.conflictPairs = append(m.conflictPairs, [2]int{i, j})
		m.conflictText[[2]int{i, j}] = c.Reason
	}

	// 外键 -> 蕴含需求 / 悬空强制删除
	for fi := range in.ForeignKeys {
		fk := in.ForeignKeys[fi]
		cti, ok := tableByName[fk.ChildTable]
		if !ok {
			return nil, fmt.Errorf("外键 %s 的子表不存在", fk)
		}
		pti, ok := tableByName[fk.ParentTable]
		if !ok {
			return nil, fmt.Errorf("外键 %s 的父表不存在", fk)
		}
		parentVals := make(map[string][]int)
		for ri := range in.Tables[pti].Rows {
			r := &in.Tables[pti].Rows[ri]
			v, ok := r.Values[fk.ParentColumn]
			if !ok || isNull(v) {
				continue
			}
			gi := m.index[RowRef{Table: fk.ParentTable, Row: r.ID}]
			key := canonicalValue(v)
			parentVals[key] = append(parentVals[key], gi)
		}
		for ri := range in.Tables[cti].Rows {
			r := &in.Tables[cti].Rows[ri]
			v, ok := r.Values[fk.ChildColumn]
			if !ok || isNull(v) {
				continue // SQL 语义：NULL 不触发外键检查
			}
			gi := m.index[RowRef{Table: fk.ChildTable, Row: r.ID}]
			key := canonicalValue(v)
			cands := parentVals[key]
			if len(cands) == 0 {
				m.dangling[gi] = true
			}
			m.reqs = append(m.reqs, fkReq{child: gi, candidates: cands, fk: fk, childValue: key})
		}
	}
	m.fks = in.ForeignKeys
	m.tables = in.Tables
	return m, nil
}
