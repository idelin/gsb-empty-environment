package model_test

import (
	"fmt"

	"example.org/netsec/uid1243/hazptr/model"
)

// ExampleEnumerate shows how an instrumented host imports the package,
// declares explicit visibility events per thread, and reads back a
// violation witness instead of assuming sequential consistency.
func ExampleEnumerate() {
	const (
		writer = 0
		reader = 1
	)
	programs := model.Programs{
		writer: {
			{Kind: model.OpRetire, Thread: writer, Addr: "A"},
			{Kind: model.OpSetRoot, Thread: writer, Root: "head", Addr: ""},
			{Kind: model.OpFlushRoot, Thread: writer},
			{Kind: model.OpScan, Thread: writer},
		},
		reader: {
			{Kind: model.OpLoadRoot, Thread: reader, Root: "head", Reg: "p"},
			{Kind: model.OpProtect, Thread: reader, Slot: 0, Addr: "A"},
			{Kind: model.OpFlushHP, Thread: reader},
			{Kind: model.OpDeref, Thread: reader, Reg: "p"},
		},
	}

	cfg := model.DefaultConfig()
	cfg.RequireConsistentReread = false
	report, err := model.Enumerate(
		[]int{writer, reader},
		map[string]string{"head": "A"},
		cfg,
		programs,
	)
	if err != nil {
		panic(err)
	}

	fmt.Println("safe:", report.Safe())
	fmt.Println("uaf witnesses:", report.ViolationCounts[model.ErrUseAfterFree])

	// The Recorder API is the adapter used when a real instrumented
	// runtime drives the machine step by step.
	machine := model.NewMachine([]int{reader},
		map[string]string{"head": "A"}, model.DefaultConfig())
	rec := model.NewRecorder(machine, reader)
	if v := rec.LoadRoot("head", "p", ""); v.Kind != model.ViolationNone {
		panic(v.Error())
	}
	fmt.Println("loaded:", machine.Trace()[0].Op.Kind)

	// Output:
	// safe: false
	// uaf witnesses: 1
	// loaded: LoadRoot
}
