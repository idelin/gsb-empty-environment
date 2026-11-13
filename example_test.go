package netsec

import "fmt"

// 端到端示例: 攻击者域 1 投毒, 受害者域 2 在 0x0200 执行间接分支。
func ExampleCompareModels() {
	in := SearchInput{
		VictimPC:       0x0200,
		VictimDomain:   2,
		AttackerDomain: 1,
		ArchTarget:     0x1000,
		TrainPCs:       []uint16{0x0200, 0x0201, 0x0204},
		TrainTargets:   []uint32{0x2000, 0x3000},
		Whitelist:      []uint32{0x1000},
		Dangerous:      []uint32{0x2000, 0x3000},
		DangerPrograms: map[uint32][]SpecOp{
			0x2000: {Write(), Probe(0xAAAA), Barrier(), Probe(0xBBBB)},
			0x3000: {Probe(0xCCCC)},
		},
	}
	a, b := CompareModels(in)
	if a.Witness != nil {
		fmt.Printf("model A: witness length %d, probes=%#x\n",
			len(a.Witness.Events), a.Witness.Run.ProbeTrace)
		for i, ev := range a.Witness.Events {
			fmt.Printf("  train%d pc=%#04x target=%#x idx=%d tag=%d conf=%d->%d\n",
				i, ev.PC, ev.Target, ev.Index, ev.Tag, ev.ConfBefore, ev.ConfAfter)
		}
	} else {
		fmt.Println("model A:", a.Note)
	}
	if b.Witness != nil {
		fmt.Printf("model B: witness length %d\n", len(b.Witness.Events))
	} else {
		fmt.Println("model B:", b.Note)
	}
	// Output:
	// model A: witness length 2, probes=[0xaaaa]
	//   train0 pc=0x0200 target=0x2000 idx=0 tag=8 conf=0->1
	//   train1 pc=0x0200 target=0x2000 idx=0 tag=8 conf=1->2
	// model B: no witness within given PC/target sets and max length
}
