package relalg

import "testing"

func TestOptimizePredicatePushDown(t *testing.T) {
	plan := Optimize(mustPlan(t, sampleSQL))
	want := `Project(u.name)
└── Join(INNER, u.id = o.uid)
    ├── Filter(u.age > 18)
    │   └── Scan(users AS u)
    └── Scan(orders AS o)`
	if got := Format(plan); got != want {
		t.Fatalf("optimized plan:\n%s\nwant:\n%s", got, want)
	}
}

func TestOptimizeJoinKeyMerge(t *testing.T) {
	sql := "SELECT * FROM a JOIN b ON a.x = b.x WHERE a.y = b.y AND a.z > 5 AND b.w < 10"
	plan := Optimize(mustPlan(t, sql))
	want := `Join(INNER, a.x = b.x AND a.y = b.y)
├── Filter(a.z > 5)
│   └── Scan(a)
└── Filter(b.w < 10)
    └── Scan(b)`
	if got := Format(plan); got != want {
		t.Fatalf("optimized plan:\n%s\nwant:\n%s", got, want)
	}
}

func TestOptimizeResidualFilter(t *testing.T) {
	// 跨两侧的非等值条件无法下推，保留在 Join 之上。
	sql := "SELECT a.x FROM a JOIN b ON a.x = b.x WHERE a.v > b.w"
	plan := Optimize(mustPlan(t, sql))
	want := `Project(a.x)
└── Filter(a.v > b.w)
    └── Join(INNER, a.x = b.x)
        ├── Scan(a)
        └── Scan(b)`
	if got := Format(plan); got != want {
		t.Fatalf("optimized plan:\n%s\nwant:\n%s", got, want)
	}
}

func TestOptimizeProjectionElimination(t *testing.T) {
	// 投影列与子算子输出完全一致，Project 被消除。
	sql := "SELECT u.id, u.name FROM users u WHERE u.id > 1"
	plan := Optimize(mustPlan(t, sql))
	want := `Filter(u.id > 1)
└── Scan(users AS u)`
	if got := Format(plan); got != want {
		t.Fatalf("optimized plan:\n%s\nwant:\n%s", got, want)
	}
}

func TestOptimizeStarProjectionElimination(t *testing.T) {
	plan := Optimize(mustPlan(t, "SELECT * FROM users u WHERE u.age > 18"))
	want := `Filter(u.age > 18)
└── Scan(users AS u)`
	if got := Format(plan); got != want {
		t.Fatalf("optimized plan:\n%s\nwant:\n%s", got, want)
	}
}

func TestOptimizeIdempotent(t *testing.T) {
	once := Format(Optimize(mustPlan(t, sampleSQL)))
	twice := Format(Optimize(Optimize(mustPlan(t, sampleSQL))))
	if once != twice {
		t.Fatalf("Optimize not idempotent:\n%s\nvs:\n%s", once, twice)
	}
}
