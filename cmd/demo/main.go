// demo 演示 DDL/DML 时间线协调器的验收场景：
//
//	DML_Old(3列) -> DDL(AlterTableAddColumn col4) -> DML_New(4列)
//
// 运行：go run ./cmd/demo
package main

import (
	"fmt"

	coordinator "cdc-coordinator"
)

func main() {
	c := coordinator.New(&coordinator.Schema{
		Version: 1,
		Columns: []string{"id", "name", "age"},
	})

	events := []coordinator.Event{
		coordinator.DMLEvent{SeqNo: 1, Raw: []string{"1", "alice", "30"}},
		coordinator.DMLEvent{SeqNo: 2, Raw: []string{"2", "bob", "25"}},
		coordinator.DDLEvent{SeqNo: 3, Kind: coordinator.AlterTableAddColumn, Column: "email"},
		coordinator.DMLEvent{SeqNo: 4, Raw: []string{"3", "carol", "40", "carol@example.com"}},
		coordinator.DMLEvent{SeqNo: 5, Raw: []string{"4", "dave", "35", "dave@example.com"}},
	}

	fmt.Println("=== CDC DDL/DML timeline coordinator demo ===")
	fmt.Printf("initial schema: %+v\n\n", c.CurrentSchema())

	for _, ev := range events {
		outs, err := c.Process(ev)
		if err != nil {
			fmt.Printf("seq=%d ERROR: %v\n", ev.Seq(), err)
			continue
		}
		for _, o := range outs {
			switch {
			case o.Barrier != nil:
				b := o.Barrier
				fmt.Printf("seq=%d  === GLOBAL BARRIER === schema v%d -> v%d (drain complete, metadata cache switched, resume)\n",
					b.SeqNo, b.FromVersion, b.ToVersion)
			case o.Row != nil:
				r := o.Row
				fmt.Printf("seq=%d  ROW decoded with schema v%d: %v\n", r.SeqNo, r.SchemaVersion, r.Values)
			}
		}
	}

	drained, barriers := c.Stats()
	fmt.Printf("\nfinal schema: %+v\n", c.CurrentSchema())
	fmt.Printf("stats: %d row(s) drained at barrier, %d barrier(s)\n", drained, barriers)
}
