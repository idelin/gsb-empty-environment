package logplan

// BuildPlan converts a SelectStmt AST into a logical operator tree.
//
// The canonical (unoptimized) shape follows relational-algebra order:
//
//	Project            (omitted for SELECT *)
//	└── Filter         (omitted without WHERE)
//	    └── Join ...   (one per JOIN clause, left-deep)
//	        └── Scan   (FROM table)
func BuildPlan(stmt *SelectStmt) Plan {
	var plan Plan = &Scan{Table: stmt.From.Name, Alias: stmt.From.EffectiveAlias()}
	for _, j := range stmt.Joins {
		right := &Scan{Table: j.Table.Name, Alias: j.Table.EffectiveAlias()}
		plan = &Join{Cond: j.On, Left: plan, Right: right}
	}
	if stmt.Where != nil {
		plan = &Filter{Predicate: stmt.Where, Child: plan}
	}
	if !stmt.Star {
		plan = &Project{Exprs: stmt.Items, Child: plan}
	}
	return plan
}

// Build parses SQL and builds the unoptimized logical plan.
func Build(sql string) (Plan, error) {
	stmt, err := Parse(sql)
	if err != nil {
		return nil, err
	}
	return BuildPlan(stmt), nil
}

// BuildOptimized parses SQL, builds the logical plan, and applies the
// optimization rules (predicate pushdown, projection elimination).
func BuildOptimized(sql string) (Plan, error) {
	plan, err := Build(sql)
	if err != nil {
		return nil, err
	}
	return Optimize(plan), nil
}

// Optimize applies the rewrite rules bottom-up:
//  1. predicate pushdown (filter conditions moved below joins, merged
//     into join conditions when they reference both sides)
//  2. projection elimination (redundant Project nodes removed)
func Optimize(p Plan) Plan {
	return eliminateProjects(pushDownPredicates(p))
}
