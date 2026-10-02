// Command demo runs the decision-table optimizer on a 10-rule business
// discount table: it detects the rule-3/rule-7 conflict, applies a fix,
// compresses the table into wildcard rules and prints the minimal tree.
package main

import (
	"fmt"
	"math"

	dt "decisiontable"
)

var (
	vipDomain     = []dt.Value{true, false}
	channelDomain = []dt.Value{"online", "offline"}
)

func dims() []dt.Dim {
	return []dt.Dim{
		{Name: "VIP", Domain: vipDomain},
		{Name: "Amount", Numeric: true},
		{Name: "Channel", Domain: channelDomain},
	}
}

func any() dt.Cond { return dt.AnyCond{} }

func vip(v bool) dt.Cond { return dt.NewSetCond(vipDomain, v) }

func channel(v string) dt.Cond { return dt.NewSetCond(channelDomain, v) }

// businessRules returns the 10 business-line discount rules.
func businessRules() []dt.Rule {
	rule := func(id int, conds []dt.Cond, action string) dt.Rule {
		return dt.Rule{ID: id, Conds: conds, Action: action}
	}
	return []dt.Rule{
		rule(1, []dt.Cond{vip(false), dt.NewRangeCond(0, 100, false, true), any()}, "1.0"),
		rule(2, []dt.Cond{vip(false), dt.NewRangeCond(100, 500, false, true), any()}, "0.95"),
		rule(3, []dt.Cond{vip(true), dt.NewRangeCond(500, math.Inf(1), false, true), any()}, "0.8"),
		rule(4, []dt.Cond{vip(true), dt.NewRangeCond(100, 500, false, true), any()}, "0.9"),
		rule(5, []dt.Cond{vip(true), dt.NewRangeCond(0, 100, false, true), any()}, "0.95"),
		rule(6, []dt.Cond{vip(false), dt.NewRangeCond(500, math.Inf(1), false, true), channel("online")}, "0.9"),
		rule(7, []dt.Cond{vip(true), dt.NewRangeCond(500, math.Inf(1), false, true), channel("offline")}, "0.7"),
		rule(8, []dt.Cond{vip(false), dt.NewRangeCond(500, math.Inf(1), false, true), channel("offline")}, "0.9"),
		rule(9, []dt.Cond{vip(true), dt.NewRangeCond(0, 100, false, true), channel("offline")}, "0.95"),
		rule(10, []dt.Cond{vip(false), dt.NewRangeCond(500, 1000, false, true), any()}, "0.9"),
	}
}

func main() {
	d := dims()
	rules := businessRules()

	fmt.Println("=== 1. Original decision table (10 business rules) ===")
	fmt.Print(dt.Table{Dims: d, Rules: rules})

	fmt.Println("=== 2. Conflict detection ===")
	conflicts := dt.DetectConflicts(rules)
	if len(conflicts) == 0 {
		fmt.Println("no conflicts")
	} else {
		for _, c := range conflicts {
			fmt.Printf("CONFLICT: rule %d (discount %s) vs rule %d (discount %s)\n",
				c.A.ID, c.A.Action, c.B.ID, c.B.Action)
			fmt.Printf("          overlap region: %s\n", dt.FormatRegion(d, c.Region))
		}
	}

	fmt.Println("=== 3. Fix conflict: align rule 7 with rule 3 (discount 0.8) ===")
	fixed := make([]dt.Rule, len(rules))
	copy(fixed, rules)
	for _, c := range conflicts {
		for i := range fixed {
			if fixed[i].ID == c.B.ID {
				fmt.Printf("rule %d: %s -> %s\n", c.B.ID, fixed[i].Action, c.A.Action)
				fixed[i].Action = c.A.Action
			}
		}
	}
	if n := len(dt.DetectConflicts(fixed)); n != 0 {
		fmt.Printf("still %d conflicts after fix, aborting\n", n)
		return
	}
	fmt.Println("re-check: no conflicts remain")

	fmt.Println("=== 4. Compress rules (merge into wildcard rules) ===")
	compressed := dt.Compress(fixed)
	fmt.Print(dt.Table{Dims: d, Rules: compressed})
	fmt.Printf("rules: %d -> %d (Channel dimension fully absorbed into wildcards)\n",
		len(rules), len(compressed))

	fmt.Println("=== 5. Minimal decision tree ===")
	fmt.Print(dt.BuildTree(compressed, d))
}
