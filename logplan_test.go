package logplan

import (
	"reflect"
	"testing"
)

const sampleQuery = "SELECT u.name FROM users u JOIN orders o ON u.id = o.uid WHERE u.age > 18"

func TestParseSampleQuery(t *testing.T) {
	stmt, err := Parse(sampleQuery)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if stmt.Star {
		t.Fatal("expected explicit projection list, got *")
	}
	if len(stmt.Items) != 1 || stmt.Items[0].String() != "u.name" {
		t.Fatalf("unexpected projection items: %v", stmt.Items)
	}
	if stmt.From.Name != "users" || stmt.From.EffectiveAlias() != "u" {
		t.Fatalf("unexpected FROM: %+v", stmt.From)
	}
	if len(stmt.Joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(stmt.Joins))
	}
	j := stmt.Joins[0]
	if j.Table.Name != "orders" || j.Table.EffectiveAlias() != "o" {
		t.Fatalf("unexpected join table: %+v", j.Table)
	}
	if j.On.String() != "u.id = o.uid" {
		t.Fatalf("unexpected ON condition: %s", j.On)
	}
	if stmt.Where == nil || stmt.Where.String() != "u.age > 18" {
		t.Fatalf("unexpected WHERE: %v", stmt.Where)
	}
}

func TestBuildUnoptimizedTree(t *testing.T) {
	plan, err := Build(sampleQuery)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	proj, ok := plan.(*Project)
	if !ok {
		t.Fatalf("root = %T, want *Project", plan)
	}
	filter, ok := proj.Child.(*Filter)
	if !ok {
		t.Fatalf("Project child = %T, want *Filter", proj.Child)
	}
	join, ok := filter.Child.(*Join)
	if !ok {
		t.Fatalf("Filter child = %T, want *Join", filter.Child)
	}
	left, ok := join.Left.(*Scan)
	if !ok || left.Table != "users" || left.Alias != "u" {
		t.Fatalf("Join left = %#v, want Scan(users AS u)", join.Left)
	}
	right, ok := join.Right.(*Scan)
	if !ok || right.Table != "orders" || right.Alias != "o" {
		t.Fatalf("Join right = %#v, want Scan(orders AS o)", join.Right)
	}

	want := `Project(u.name)
└── Filter(u.age > 18)
    └── Join(u.id = o.uid)
        ├── Scan(users AS u)
        └── Scan(orders AS o)
`
	if got := String(plan); got != want {
		t.Fatalf("printed plan mismatch:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestPredicatePushdown(t *testing.T) {
	plan, err := BuildOptimized(sampleQuery)
	if err != nil {
		t.Fatalf("BuildOptimized failed: %v", err)
	}

	proj, ok := plan.(*Project)
	if !ok {
		t.Fatalf("root = %T, want *Project", plan)
	}
	join, ok := proj.Child.(*Join)
	if !ok {
		t.Fatalf("Project child = %T, want *Join (filter must be pushed below)", proj.Child)
	}
	filter, ok := join.Left.(*Filter)
	if !ok {
		t.Fatalf("Join left = %T, want *Filter", join.Left)
	}
	if filter.Predicate.String() != "u.age > 18" {
		t.Fatalf("pushed predicate = %s, want u.age > 18", filter.Predicate)
	}
	scan, ok := filter.Child.(*Scan)
	if !ok || scan.Alias != "u" {
		t.Fatalf("Filter child = %#v, want Scan(users AS u)", filter.Child)
	}
	if _, ok := join.Right.(*Scan); !ok {
		t.Fatalf("Join right = %T, want *Scan", join.Right)
	}

	want := `Project(u.name)
└── Join(u.id = o.uid)
    ├── Filter(u.age > 18)
    │   └── Scan(users AS u)
    └── Scan(orders AS o)
`
	if got := String(plan); got != want {
		t.Fatalf("printed plan mismatch:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestPushdownSplitsConjuncts(t *testing.T) {
	sql := `SELECT u.name FROM users u JOIN orders o ON u.id = o.uid ` +
		`WHERE u.age > 18 AND o.total > 100 AND u.city = o.city`
	plan, err := BuildOptimized(sql)
	if err != nil {
		t.Fatalf("BuildOptimized failed: %v", err)
	}
	want := `Project(u.name)
└── Join(u.id = o.uid AND u.city = o.city)
    ├── Filter(u.age > 18)
    │   └── Scan(users AS u)
    └── Filter(o.total > 100)
        └── Scan(orders AS o)
`
	if got := String(plan); got != want {
		t.Fatalf("printed plan mismatch:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestProjectionEliminationStar(t *testing.T) {
	plan, err := BuildOptimized("SELECT * FROM users u WHERE u.age > 18")
	if err != nil {
		t.Fatalf("BuildOptimized failed: %v", err)
	}
	// No Project node should exist anywhere for SELECT *.
	Walk(plan, func(n Plan) bool {
		if _, isProj := n.(*Project); isProj {
			t.Fatal("unexpected Project node for SELECT *")
		}
		return true
	})
	want := `Filter(u.age > 18)
└── Scan(users AS u)
`
	if got := String(plan); got != want {
		t.Fatalf("printed plan mismatch:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestProjectionEliminationTableStar(t *testing.T) {
	plan, err := BuildOptimized("SELECT u.* FROM users u")
	if err != nil {
		t.Fatalf("BuildOptimized failed: %v", err)
	}
	if _, ok := plan.(*Scan); !ok {
		t.Fatalf("root = %T, want *Scan after eliminating u.* projection", plan)
	}
}

func TestProjectionEliminationNested(t *testing.T) {
	inner := &Project{
		Exprs: []Expr{&ColumnRef{Table: "u", Name: "name"}, &ColumnRef{Table: "u", Name: "age"}},
		Child: &Scan{Table: "users", Alias: "u"},
	}
	outer := &Project{Exprs: []Expr{&ColumnRef{Table: "u", Name: "name"}}, Child: inner}
	got := String(eliminateProjects(outer))
	want := `Project(u.name)
└── Scan(users AS u)
`
	if got != want {
		t.Fatalf("printed plan mismatch:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestWalkPreOrder(t *testing.T) {
	plan, err := Build(sampleQuery)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	var names []string
	Walk(plan, func(n Plan) bool {
		names = append(names, n.Name())
		return true
	})
	want := []string{"Project", "Filter", "Join", "Scan", "Scan"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("walk order = %v, want %v", names, want)
	}
}

func TestParseErrors(t *testing.T) {
	for _, sql := range []string{
		"SELECT FROM users",
		"SELECT u.name users",
		"SELECT u.name FROM users u JOIN orders o",
		"SELECT u.name FROM users u WHERE",
		"SELECT u.name FROM users u WHERE u.age > 18 extra",
	} {
		if _, err := Parse(sql); err == nil {
			t.Fatalf("expected parse error for %q", sql)
		}
	}
}
