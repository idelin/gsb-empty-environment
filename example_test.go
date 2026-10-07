package merge_test

import (
	"fmt"
	"time"

	"merge"
)

func ExampleMerge() {
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	rec := merge.Record{
		"email": {
			{Source: "crm", Priority: 2, Seq: 10, Value: "a@x.com"},
			{Source: "web", Priority: 1, Seq: 99, Value: "b@y.com"},
			{Source: "legacy", Priority: 3, Seq: 1, Value: "c@z.com",
				ValidTo: now.Add(-time.Hour)}, // 已过期
		},
		"phone": {
			{Source: "crm", Priority: 1, Seq: 5, Null: true}, // 显式 null
		},
		"tags": {
			{Source: "crm", Priority: 1, Seq: 7, Value: "vip"},
			{Source: "web", Priority: 1, Seq: 7, Value: "trial"}, // 同优先同序异值
		},
	}
	res := merge.Merge(rec, now)
	for _, d := range res.Decisions {
		fmt.Printf("%s: status=%s value=%v null=%v\n", d.Field, d.Status, d.Value, d.Null)
		for _, ex := range d.Candidates {
			fmt.Printf("  [%s] effective=%v %s\n", ex.Candidate.Source, ex.Effective, ex.Reason)
		}
	}
	fmt.Println("conflicts:", res.Conflicts)
	// Output:
	// email: status=resolved value=a@x.com null=false
	//   [crm] effective=true winner
	//   [web] effective=true lower priority (1 < 2)
	//   [legacy] effective=false outside validity window at 2026-10-07T00:00:00Z
	// phone: status=resolved value=<nil> null=true
	//   [crm] effective=true winner
	// tags: status=conflict value=vip null=false
	//   [crm] effective=true winner (conflict: tie on priority/seq with different values, deterministic fallback by source)
	//   [web] effective=true conflict: tie on priority/seq with different values
	// conflicts: [tags]
}
