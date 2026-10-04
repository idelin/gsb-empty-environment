package fieldauth

import (
	"reflect"
	"strings"
	"testing"
)

// userEngine 构造用户档案场景的引擎：
//   - role：只有（旧快照中的）管理员才能变更角色；
//   - email：敏感字段，只有旧快照中的管理员才能修改；
//   - display_name：由 first+last 派生，仅 name-manager 可使其变化；
//   - name_len：由 display_name 派生（验证派生链拓扑序）。
func userEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine(
		[]Rule{
			{
				Field: "role",
				Name:  "role-admin-only",
				Conds: []Cond{{
					View: Old,
					Desc: "only an admin (committed state) may change roles",
					Func: func(rec Record, a Actor) bool {
						return rec["role"] == "admin"
					},
				}},
			},
			{
				Field: "email",
				Name:  "email-admin-only",
				Conds: []Cond{{
					View: Old,
					Desc: "only an admin (committed state) may change email",
					Func: func(rec Record, a Actor) bool {
						return rec["role"] == "admin"
					},
				}},
			},
			{
				Field: "display_name",
				Name:  "display-name-manager-only",
				Conds: []Cond{{
					View: Old,
					Desc: "only a name-manager may change display names",
					Func: func(rec Record, a Actor) bool {
						return a.HasRole("name-manager")
					},
				}},
			},
		},
		[]Deriver{
			{
				Field:   "name_len",
				Depends: []string{"display_name"},
				Recompute: func(rec Record) (interface{}, error) {
					return len(rec["display_name"].(string)), nil
				},
			},
			{
				Field:   "display_name",
				Depends: []string{"first", "last"},
				Recompute: func(rec Record) (interface{}, error) {
					return rec["first"].(string) + " " + rec["last"].(string), nil
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func alice() Record {
	return Record{
		"role":         "user",
		"email":        "alice@example.com",
		"first":        "Alice",
		"last":         "Anderson",
		"display_name": "Alice Anderson",
		"name_len":     14,
	}
}

// 核心安全属性：同一批次里“先把自己改成管理员再改敏感字段”，
// 授权条件读取 Old 快照，不得使用未提交的新身份。
func TestSelfEscalationBatchDenied(t *testing.T) {
	e := userEngine(t)
	d, err := e.Check(Actor{ID: "alice"}, alice(), Update{Set: Record{
		"role":  "admin",
		"email": "attacker@evil.com",
	}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d.Allowed {
		t.Fatal("self-escalation batch must be denied")
	}
	var roleV, emailV bool
	for _, v := range d.Violations {
		switch v.Field {
		case "role":
			roleV = true
		case "email":
			emailV = true
		}
		if v.View != Old {
			t.Errorf("authorization violation must read Old snapshot, got %v", v.View)
		}
	}
	if !roleV || !emailV {
		t.Errorf("expected violations on role and email, got %v", d.Violations)
	}
	// 最小解释：仅 {role: admin} 本身就足以被拒绝。
	if d.Minimal == nil {
		t.Fatal("denied decision must carry minimal explanation")
	}
	want := Record{"role": "admin"}
	if !reflect.DeepEqual(d.Minimal.Assignments, want) {
		t.Errorf("minimal assignments = %v, want %v", d.Minimal.Assignments, want)
	}
}

// 真正的管理员（旧快照即为 admin）可以同批修改 role 与 email。
func TestCommittedAdminBatchAllowed(t *testing.T) {
	e := userEngine(t)
	old := alice()
	old["role"] = "admin"
	d, err := e.Check(Actor{ID: "root", Roles: []string{"name-manager"}}, old, Update{Set: Record{
		"role":  "user",
		"email": "new@example.com",
	}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !d.Allowed {
		t.Fatalf("committed admin batch should be allowed, violations: %v", d.Violations)
	}
	if d.New["email"] != "new@example.com" || d.New["role"] != "user" {
		t.Errorf("unexpected new record: %v", d.New)
	}
}

// 原子性：批量中任一字段被拒绝，整批不得部分生效。
func TestAtomicAllOrNothing(t *testing.T) {
	e := userEngine(t)
	old := alice()
	d, err := e.Check(Actor{ID: "alice", Roles: []string{"name-manager"}}, old, Update{Set: Record{
		"first": "Alicia",          // 合法：派生 display_name 有 name-manager 授权
		"email": "hacked@evil.com", // 非法：非管理员改敏感字段
	}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d.Allowed {
		t.Fatal("batch containing an unauthorized field must be denied as a whole")
	}
	if len(d.Violations) != 1 || d.Violations[0].Field != "email" {
		t.Errorf("expected single email violation, got %v", d.Violations)
	}
	// 最小解释应只保留 email 赋值，first 本身合法。
	want := Record{"email": "hacked@evil.com"}
	if !reflect.DeepEqual(d.Minimal.Assignments, want) {
		t.Errorf("minimal assignments = %v, want %v", d.Minimal.Assignments, want)
	}
}

// 派生字段自动重算并参与检查：普通用户改 first 会联动 display_name，
// 触发其守卫规则而被拒绝；name-manager 则被允许且派生链完整重算。
func TestDerivedFieldRecomputeAndCheck(t *testing.T) {
	e := userEngine(t)

	denied, err := e.Check(Actor{ID: "alice"}, alice(), Update{Set: Record{
		"first": "Alicia",
	}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if denied.Allowed {
		t.Fatal("derived display_name change must be checked and denied")
	}
	found := false
	for _, v := range denied.Violations {
		if v.Field == "display_name" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected violation on derived field display_name, got %v", denied.Violations)
	}

	allowed, err := e.Check(Actor{ID: "alice", Roles: []string{"name-manager"}}, alice(), Update{Set: Record{
		"first": "Alicia",
	}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !allowed.Allowed {
		t.Fatalf("name-manager should be allowed, violations: %v", allowed.Violations)
	}
	if allowed.New["display_name"] != "Alicia Anderson" {
		t.Errorf("display_name = %v, want %v", allowed.New["display_name"], "Alicia Anderson")
	}
	if allowed.New["name_len"] != len("Alicia Anderson") {
		t.Errorf("name_len = %v, want %d", allowed.New["name_len"], len("Alicia Anderson"))
	}
	wantChanged := []string{"display_name", "first", "name_len"}
	if !reflect.DeepEqual(allowed.Changed, wantChanged) {
		t.Errorf("changed = %v, want %v", allowed.Changed, wantChanged)
	}
}

// New 快照条件读取提交后的值：跨字段不变量在合并后的记录上求值。
func TestNewViewConditionsReadPostCommitValues(t *testing.T) {
	e, err := NewEngine([]Rule{{
		Field: "budget",
		Name:  "budget-within-limit",
		Conds: []Cond{{
			View: New,
			Desc: "budget must not exceed limit after the update",
			Func: func(rec Record, a Actor) bool {
				return rec["budget"].(int) <= rec["limit"].(int)
			},
		}},
	}}, nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	old := Record{"budget": 50, "limit": 1000, "note": "ok"}

	// 同批把 limit 降到 100：New 快照看到 200 > 100，拒绝。
	d, err := e.Check(Actor{ID: "bob"}, old, Update{Set: Record{
		"budget": 200,
		"limit":  100,
		"note":   "quarterly adjustment",
	}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d.Allowed {
		t.Fatal("budget exceeding the new limit must be denied")
	}
	// 最小解释：budget 与 limit 任一单独提交都合法，组合才被拒绝；
	// 无关的 note 必须被剔除。
	want := Record{"budget": 200, "limit": 100}
	if !reflect.DeepEqual(d.Minimal.Assignments, want) {
		t.Errorf("minimal assignments = %v, want %v", d.Minimal.Assignments, want)
	}
	if len(d.Minimal.Violations) != 1 || d.Minimal.Violations[0].View != New {
		t.Errorf("unexpected minimal violations: %v", d.Minimal.Violations)
	}
}

// Check 不得修改调用方持有的旧记录与更新内容。
func TestCheckDoesNotMutateInputs(t *testing.T) {
	e := userEngine(t)
	old := alice()
	oldSnapshot := mergeRecords(nil, old)
	set := Record{"first": "Alicia"}
	setSnapshot := mergeRecords(nil, set)
	if _, err := e.Check(Actor{ID: "alice", Roles: []string{"name-manager"}}, old, Update{Set: set}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !reflect.DeepEqual(old, oldSnapshot) {
		t.Errorf("old record mutated: %v", old)
	}
	if !reflect.DeepEqual(set, setSnapshot) {
		t.Errorf("update set mutated: %v", set)
	}
}

// 派生器依赖成环时构建引擎必须报错。
func TestDeriverCycleRejected(t *testing.T) {
	_, err := NewEngine(nil, []Deriver{
		{Field: "a", Depends: []string{"b"}, Recompute: func(rec Record) (interface{}, error) { return nil, nil }},
		{Field: "b", Depends: []string{"a"}, Recompute: func(rec Record) (interface{}, error) { return nil, nil }},
	})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}
