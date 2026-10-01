// Demo of the DDL/DML timeline coordinator.
//
// Stream: DML_Old(3 cols) -> DDL(add col4) -> DML_New(4 cols).
// The coordinator inserts a global barrier at the DDL, drains the old
// DML, applies the new schema, then resumes the flow.
package main

import (
	"context"
	"fmt"
	"sort"

	"cdcbarrier/coordinator"
)

func main() {
	reg := coordinator.NewRegistry()
	reg.Register("users",
		coordinator.Column{Name: "id", Type: "INT"},
		coordinator.Column{Name: "name", Type: "STRING"},
		coordinator.Column{Name: "age", Type: "INT"},
	)

	c := coordinator.NewCoordinator(reg, 4)

	in := make(chan coordinator.Event)
	out := c.Run(context.Background(), in)

	go func() {
		defer close(in)
		in <- coordinator.DMLEvent{Table: "users", Raw: []string{"1", "alice", "30"}}
		in <- coordinator.DMLEvent{Table: "users", Raw: []string{"2", "bob", "41"}}
		fmt.Println("--- DDL arrives: ALTER TABLE users ADD COLUMN email STRING ---")
		in <- coordinator.DDLEvent{
			Table:  "users",
			Kind:   coordinator.AddColumn,
			Column: coordinator.Column{Name: "email", Type: "STRING"},
		}
		in <- coordinator.DMLEvent{Table: "users", Raw: []string{"3", "carol", "27", "carol@example.com"}}
	}()

	for o := range out {
		switch v := o.(type) {
		case coordinator.RowOutput:
			fmt.Printf("ROW     table=%s schema=v%d fields=%s\n",
				v.Row.Table, v.Row.SchemaVersion, formatFields(v.Row.Fields))
		case coordinator.BarrierOutput:
			fmt.Printf("BARRIER table=%s schema v%d -> v%d (old DML drained, new schema applied)\n",
				v.Table, v.FromVersion, v.ToVersion)
		case coordinator.ErrorOutput:
			fmt.Printf("ERROR   table=%s err=%v\n", v.Table, v.Err)
		}
	}
}

func formatFields(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := "{"
	for i, k := range keys {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%s:%q", k, m[k])
	}
	return s + "}"
}
