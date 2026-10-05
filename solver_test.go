package main

import (
	"fmt"
	"strings"
	"testing"
)

func buildTestModel(t *testing.T, specJSON string) *Model {
	t.Helper()
	spec, err := ParseSpec([]byte(specJSON))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	m, err := BuildModel(spec, 25)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	return m
}

func deletedSet(m *Model, res *Result) map[string]bool {
	out := map[string]bool{}
	for _, i := range res.DeletedRows {
		out[m.Rows[i].Ref()] = true
	}
	return out
}

func keptSet(m *Model, res *Result) map[string]bool {
	out := map[string]bool{}
	for _, i := range res.KeptRows {
		out[m.Rows[i].Ref()] = true
	}
	return out
}

// 关键场景：分别修每条约束再拼接，得不到全局最优。
// 唯一键要求 u1/u2 删一个；若只看唯一键删权重小的 u1(w=1)，
// 会级联删掉 o1(w=100)，总删除权重 101。
// 全局最优是删 u2(w=5) 并级联删 o2(w=4)，总删除权重 9。
const shopSpec = `{
  "tables": [
    {"name": "users", "unique_keys": [["email"]], "rows": [
      {"id": "u1", "weight": 1, "fields": {"id": 1, "email": "a@x.com"}},
      {"id": "u2", "weight": 5, "fields": {"id": 2, "email": "a@x.com"}}
    ]},
    {"name": "orders", "rows": [
      {"id": "o1", "weight": 100, "fields": {"id": 1, "user_id": 1}},
      {"id": "o2", "weight": 4,  "fields": {"id": 2, "user_id": 2}}
    ]}
  ],
  "foreign_keys": [
    {"name": "orders_user", "child_table": "orders", "child_field": "user_id",
     "parent_table": "users", "parent_field": "id"}
  ]
}`

func TestGlobalOptimumBeatsLocalFix(t *testing.T) {
	m := buildTestModel(t, shopSpec)
	res := Solve(m)
	if !res.Optimal {
		t.Fatal("expected optimal result")
	}
	if res.DeletedWeight != 9 {
		t.Fatalf("deleted weight = %d, want 9 (局部修复会得到 101)", res.DeletedWeight)
	}
	del := deletedSet(m, res)
	if !del["users#u2"] || !del["orders#o2"] || len(del) != 2 {
		t.Fatalf("deleted = %v, want {users#u2, orders#o2}", del)
	}
	if v := Verify(m, res.State); len(v) > 0 {
		t.Fatalf("verify failed: %v", v)
	}
}

func TestReasonClosureCascade(t *testing.T) {
	m := buildTestModel(t, shopSpec)
	res := Solve(m)
	reasons := ComputeReasons(m, res.State)

	idx := -1
	for i, r := range m.Rows {
		if r.Ref() == "orders#o2" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("orders#o2 not found")
	}
	rs := reasons[idx]
	if len(rs) != 1 || rs[0].Kind != "cascade" {
		t.Fatalf("orders#o2 reasons = %+v, want single cascade", rs)
	}
	// 闭包：级联原因必须携带父行 users#u2 的唯一键原因
	if len(rs[0].Children) != 1 || rs[0].Children[0].Kind != "parent_deleted" {
		t.Fatalf("cascade children = %+v", rs[0].Children)
	}
	grand := rs[0].Children[0].Children
	if len(grand) != 1 || grand[0].Kind != "unique_key" {
		t.Fatalf("parent reasons = %+v, want unique_key", grand)
	}
	if !strings.Contains(grand[0].Message, "users#u1") {
		t.Fatalf("unique_key reason should reference kept row users#u1: %s", grand[0].Message)
	}
}

func TestDanglingFK(t *testing.T) {
	m := buildTestModel(t, `{
	  "tables": [
	    {"name": "users", "rows": [
	      {"id": "u1", "weight": 1, "fields": {"id": 1}}
	    ]},
	    {"name": "orders", "rows": [
	      {"id": "o1", "weight": 3, "fields": {"id": 1, "user_id": 99}}
	    ]}
	  ],
	  "foreign_keys": [
	    {"child_table": "orders", "child_field": "user_id",
	     "parent_table": "users", "parent_field": "id"}
	  ]
	}`)
	res := Solve(m)
	del := deletedSet(m, res)
	if !del["orders#o1"] || len(del) != 1 {
		t.Fatalf("deleted = %v, want {orders#o1}", del)
	}
	reasons := ComputeReasons(m, res.State)
	for i, r := range m.Rows {
		if r.Ref() == "orders#o1" {
			if len(reasons[i]) != 1 || reasons[i][0].Kind != "dangling_fk" {
				t.Fatalf("reasons = %+v, want dangling_fk", reasons[i])
			}
		}
	}
}

func TestConflictPair(t *testing.T) {
	m := buildTestModel(t, `{
	  "tables": [
	    {"name": "profiles", "rows": [
	      {"id": "p1", "weight": 2, "fields": {"id": 1}},
	      {"id": "p2", "weight": 7, "fields": {"id": 2}}
	    ]}
	  ],
	  "conflicts": [
	    {"name": "same-person", "rows": ["profiles#p1", "profiles#p2"]}
	  ]
	}`)
	res := Solve(m)
	del := deletedSet(m, res)
	if !del["profiles#p1"] || len(del) != 1 {
		t.Fatalf("deleted = %v, want {profiles#p1}", del)
	}
	if v := Verify(m, res.State); len(v) > 0 {
		t.Fatalf("verify failed: %v", v)
	}
}

// 父字段不唯一时，子行只需至少一个候选父行保留。
// p1/p2 的 id 相同且互相冲突，应保留权重大的 p2 以支撑子行 c1。
func TestMultiCandidateParent(t *testing.T) {
	m := buildTestModel(t, `{
	  "tables": [
	    {"name": "parents", "rows": [
	      {"id": "p1", "weight": 2, "fields": {"id": 1}},
	      {"id": "p2", "weight": 5, "fields": {"id": 1}}
	    ]},
	    {"name": "children", "rows": [
	      {"id": "c1", "weight": 100, "fields": {"id": 9, "pid": 1}}
	    ]}
	  ],
	  "foreign_keys": [
	    {"child_table": "children", "child_field": "pid",
	     "parent_table": "parents", "parent_field": "id"}
	  ],
	  "conflicts": [
	    {"name": "dup-parent", "rows": ["parents#p1", "parents#p2"]}
	  ]
	}`)
	res := Solve(m)
	kept := keptSet(m, res)
	if !kept["parents#p2"] || !kept["children#c1"] || kept["parents#p1"] {
		t.Fatalf("kept = %v, want {parents#p2, children#c1}", kept)
	}
	if v := Verify(m, res.State); len(v) > 0 {
		t.Fatalf("verify failed: %v", v)
	}
}

func TestCompositeUniqueKey(t *testing.T) {
	m := buildTestModel(t, `{
	  "tables": [
	    {"name": "kv", "unique_keys": [["a", "b"]], "rows": [
	      {"id": "r1", "weight": 1, "fields": {"a": 1, "b": 1}},
	      {"id": "r2", "weight": 2, "fields": {"a": 1, "b": 2}},
	      {"id": "r3", "weight": 4, "fields": {"a": 1, "b": 1}}
	    ]}
	  ]
	}`)
	res := Solve(m)
	del := deletedSet(m, res)
	if !del["kv#r1"] || len(del) != 1 {
		t.Fatalf("deleted = %v, want {kv#r1}", del)
	}
}

// 自引用外键的级联：唯一键迫使删除某节点时，其子节点随之级联删除。
func TestSelfReferencingCascade(t *testing.T) {
	m := buildTestModel(t, `{
	  "tables": [
	    {"name": "nodes", "unique_keys": [["email"]], "rows": [
	      {"id": "n1", "weight": 10, "fields": {"id": 1, "email": "x@x.com", "parent_id": null}},
	      {"id": "n2", "weight": 1,  "fields": {"id": 2, "email": "x@x.com", "parent_id": null}},
	      {"id": "n3", "weight": 50, "fields": {"id": 3, "email": "y@y.com", "parent_id": 2}}
	    ]}
	  ],
	  "foreign_keys": [
	    {"child_table": "nodes", "child_field": "parent_id",
	     "parent_table": "nodes", "parent_field": "id"}
	  ]
	}`)
	res := Solve(m)
	del := deletedSet(m, res)
	// 删 n2(w=1) 会级联删 n3(w=50)，总代价 51；删 n1(w=10) 无级联，总代价 10。
	if !del["nodes#n1"] || len(del) != 1 {
		t.Fatalf("deleted = %v, want {nodes#n1}", del)
	}
	if res.DeletedWeight != 10 {
		t.Fatalf("deleted weight = %d, want 10", res.DeletedWeight)
	}
}

func TestEveryDeletedRowHasReason(t *testing.T) {
	m := buildTestModel(t, shopSpec)
	res := Solve(m)
	reasons := ComputeReasons(m, res.State)
	for _, i := range res.DeletedRows {
		if len(reasons[i]) == 0 {
			t.Fatalf("deleted row %s has no reason", m.Rows[i].Ref())
		}
	}
}

func TestMaxRowsLimit(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"tables":[{"name":"t","rows":[`)
	for i := 0; i < 26; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(`{"id":"r%d","fields":{}}`, i))
	}
	sb.WriteString(`]}]}`)
	spec, err := ParseSpec([]byte(sb.String()))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if _, err := BuildModel(spec, 25); err == nil {
		t.Fatal("expected error for 26 rows with limit 25")
	}
}

// 25 行深级联链：r1..r23 依次外键引用前一行；r0 与 r0b 唯一键冲突。
// 删 r0(w=1) 会级联删掉整条链（共 24），全局最优是只删 r0b(w=2)。
func TestDeepCascadeChain25(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"tables":[{"name":"chain","unique_keys":[["email"]],"rows":[`)
	sb.WriteString(`{"id":"r0","weight":1,"fields":{"id":0,"email":"dup@x.com"}},`)
	sb.WriteString(`{"id":"r0b","weight":2,"fields":{"id":100,"email":"dup@x.com"}}`)
	for i := 1; i <= 23; i++ {
		sb.WriteString(fmt.Sprintf(`,{"id":"r%d","weight":1,"fields":{"id":%d,"email":"e%d@x.com","parent":%d}}`, i, i, i, i-1))
	}
	sb.WriteString(`]}],"foreign_keys":[{"child_table":"chain","child_field":"parent","parent_table":"chain","parent_field":"id"}]}`)
	m := buildTestModel(t, sb.String())
	if len(m.Rows) != 25 {
		t.Fatalf("rows = %d, want 25", len(m.Rows))
	}
	res := Solve(m)
	if !res.Optimal {
		t.Fatal("expected optimal result")
	}
	del := deletedSet(m, res)
	if !del["chain#r0b"] || len(del) != 1 || res.DeletedWeight != 2 {
		t.Fatalf("deleted = %v (weight %d), want {chain#r0b} weight 2", del, res.DeletedWeight)
	}
	if v := Verify(m, res.State); len(v) > 0 {
		t.Fatalf("verify failed: %v", v)
	}
}
