// Command hazptr-demo exhaustively enumerates two-thread hazard-pointer
// interleavings and prints violation witnesses for the instrumented host.
package main

import (
	"fmt"

	"example.org/netsec/uid1243/hazptr/model"
)

const (
	reclaimer = 0
	reader    = 1
)

func main() {
	fmt.Println("== hazard-pointer event model: two-thread exhaustive run ==")

	runLatePublication()
	fmt.Println()
	runAddressReuse()
	fmt.Println()
	runPartialScan()
	fmt.Println()
	runCompliantBaseline()
}

func runLatePublication() {
	fmt.Println("-- scenario 1: late publication (hazard fence may land after scan) --")
	progs := model.Programs{
		reclaimer: {
			{Kind: model.OpRetire, Thread: reclaimer, Addr: "A", Label: "retire A"},
			{Kind: model.OpSetRoot, Thread: reclaimer, Root: "head", Addr: "", Label: "unlink A"},
			{Kind: model.OpFlushRoot, Thread: reclaimer, Label: "publish unlink"},
			{Kind: model.OpScan, Thread: reclaimer, Label: "reclaim"},
		},
		reader: {
			{Kind: model.OpLoadRoot, Thread: reader, Root: "head", Reg: "p", Label: "read root"},
			{Kind: model.OpProtect, Thread: reader, Slot: 0, Addr: "A", Label: "stage hazard"},
			{Kind: model.OpFlushHP, Thread: reader, Label: "publish hazard"},
			{Kind: model.OpDeref, Thread: reader, Reg: "p", Label: "deref A"},
		},
	}
	cfg := model.DefaultConfig()
	cfg.RequireConsistentReread = false
	rep, err := model.Enumerate([]int{reclaimer, reader},
		map[string]string{"head": "A"}, cfg, progs)
	if err != nil {
		panic(err)
	}
	fmt.Print(model.RenderReport(rep))
	if w, ok := rep.Witnesses[model.ErrUseAfterFree]; ok {
		fmt.Println()
		fmt.Print(model.RenderWitness(w))
	}
}

func runAddressReuse() {
	fmt.Println("-- scenario 2: address reuse (ABA across generations) --")
	progs := model.Programs{
		reclaimer: {
			{Kind: model.OpRetire, Thread: reclaimer, Addr: "A", Label: "retire A"},
			{Kind: model.OpSetRoot, Thread: reclaimer, Root: "head", Addr: "", Label: "unlink A"},
			{Kind: model.OpFlushRoot, Thread: reclaimer, Label: "publish unlink"},
			{Kind: model.OpScan, Thread: reclaimer, Label: "reclaim A"},
			{Kind: model.OpAlloc, Thread: reclaimer, Reg: "b", ReuseAddr: "A", Label: "reuse A as B"},
		},
		reader: {
			{Kind: model.OpLoadRoot, Thread: reader, Root: "head", Reg: "p", Label: "read root A"},
			{Kind: model.OpProtect, Thread: reader, Slot: 0, Addr: "A", Label: "hazard A"},
			{Kind: model.OpFlushHP, Thread: reader, Label: "publish hazard"},
			{Kind: model.OpClearHP, Thread: reader, Slot: 0, Label: "release hazard"},
			{Kind: model.OpFlushHP, Thread: reader, Label: "publish clear"},
			{Kind: model.OpDeref, Thread: reader, Reg: "p", Label: "stale deref"},
		},
	}
	cfg := model.DefaultConfig()
	cfg.RequireConsistentReread = false
	rep, err := model.Enumerate([]int{reclaimer, reader},
		map[string]string{"head": "A"}, cfg, progs)
	if err != nil {
		panic(err)
	}
	fmt.Print(model.RenderReport(rep))
	if w, ok := rep.Witnesses[model.ErrAddressReuse]; ok {
		fmt.Println()
		fmt.Print(model.RenderWitness(w))
	}
}

func runPartialScan() {
	fmt.Println("-- scenario 3: reclaimer scans only its own thread --")
	progs := model.Programs{
		reader: {
			{Kind: model.OpProtect, Thread: reader, Slot: 0, Addr: "A", Label: "hazard A"},
			{Kind: model.OpFlushHP, Thread: reader, Label: "publish hazard"},
		},
		reclaimer: {
			{Kind: model.OpRetire, Thread: reclaimer, Addr: "A", Label: "retire A"},
			{Kind: model.OpScan, Thread: reclaimer, IncludeThreads: []int{reclaimer}, Label: "partial scan"},
		},
	}
	rep, err := model.Enumerate([]int{reclaimer, reader},
		map[string]string{"head": "A"}, model.DefaultConfig(), progs)
	if err != nil {
		panic(err)
	}
	fmt.Print(model.RenderReport(rep))
	if w, ok := rep.Witnesses[model.ErrPartialScan]; ok {
		fmt.Println()
		fmt.Print(model.RenderWitness(w))
	}
}

func runCompliantBaseline() {
	fmt.Println("-- baseline: full protocol (publish before scan, re-read before deref) --")
	progs := model.Programs{
		reclaimer: {
			{Kind: model.OpRetire, Thread: reclaimer, Addr: "A", Label: "retire A"},
			{Kind: model.OpSetRoot, Thread: reclaimer, Root: "head", Addr: "B", Label: "install B"},
			{Kind: model.OpFlushRoot, Thread: reclaimer, Label: "publish B"},
			{Kind: model.OpScan, Thread: reclaimer, Label: "reclaim"},
		},
		reader: {
			{Kind: model.OpLoadRoot, Thread: reader, Root: "head", Reg: "p", Label: "read root"},
			{Kind: model.OpProtect, Thread: reader, Slot: 0, Addr: "A", Label: "hazard A"},
			{Kind: model.OpFlushHP, Thread: reader, Label: "publish hazard"},
			{Kind: model.OpVerifyRoot, Thread: reader, Root: "head", Reg: "p", Label: "re-read"},
			{Kind: model.OpDeref, Thread: reader, Reg: "p", Label: "deref"},
		},
	}
	rep, err := model.Enumerate([]int{reclaimer, reader},
		map[string]string{"head": "A", "next": "B"}, model.DefaultConfig(), progs)
	if err != nil {
		panic(err)
	}
	fmt.Print(model.RenderReport(rep))
}
