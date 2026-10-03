package relalg

import (
	"reflect"
	"testing"
)

func mustPlan(t *testing.T, sql string) Plan {
	t.Helper()
	stmt, err := ParseSelect(sql)
	if err != nil {
		t.Fatalf("ParseSelect: %v", err)
	}
	plan, err := BuildPlan(stmt)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	return plan
}

func TestBuildPlanShape(t *testing.T) {
	plan := mustPlan(t, sampleSQL)

	proj, ok := plan.(*Project)
	if !ok {
		t.Fatalf("root = %T, want *Project", plan)
	}
	filter, ok := proj.Input.(*Filter)
	if !ok {
		t.Fatalf("project input = %T, want *Filter", proj.Input)
	}
	join, ok := filter.Input.(*Join)
	if !ok {
		t.Fatalf("filter input = %T, want *Join", filter.Input)
	}
	left, ok := join.Left.(*Scan)
	if !ok || left.Table != "users" || left.Alias != "u" {
		t.Fatalf("join left = %#v, want Scan users AS u", join.Left)
	}
	right, ok := join.Right.(*Scan)
	if !ok || right.Table != "orders" || right.Alias != "o" {
		t.Fatalf("join right = %#v, want Scan orders AS o", join.Right)
	}
	if got := filter.Cond.String(); got != "u.age > 18" {
		t.Fatalf("filter cond = %q, want %q", got, "u.age > 18")
	}
	if got := join.Cond.String(); got != "u.id = o.uid" {
		t.Fatalf("join cond = %q, want %q", got, "u.id = o.uid")
	}
}

func TestFormatUnoptimized(t *testing.T) {
	plan := mustPlan(t, sampleSQL)
	want := `Project(u.name)
└── Filter(u.age > 18)
    └── Join(INNER, u.id = o.uid)
        ├── Scan(users AS u)
        └── Scan(orders AS o)`
	if got := Format(plan); got != want {
		t.Fatalf("Format:\n%s\nwant:\n%s", got, want)
	}
}

func TestWalkPreOrder(t *testing.T) {
	plan := mustPlan(t, sampleSQL)
	var got []string
	Walk(plan, func(p Plan) {
		got = append(got, reflect.TypeOf(p).String())
	})
	want := []string{
		"*relalg.Project", "*relalg.Filter", "*relalg.Join",
		"*relalg.Scan", "*relalg.Scan",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Walk order = %v, want %v", got, want)
	}
}

func TestSchema(t *testing.T) {
	plan := mustPlan(t, sampleSQL)
	join := plan.(*Project).Input.(*Filter).Input.(*Join)
	want := []string{"u.age", "u.id", "u.name", "o.uid"}
	if got := join.Schema(); !reflect.DeepEqual(got, want) {
		t.Fatalf("join schema = %v, want %v", got, want)
	}
	if got := plan.Schema(); !reflect.DeepEqual(got, []string{"u.name"}) {
		t.Fatalf("project schema = %v, want [u.name]", got)
	}
}
