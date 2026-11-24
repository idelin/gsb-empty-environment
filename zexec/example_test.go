package zexec_test

import (
	"fmt"
	"os"

	"prescreen.local/ps-7e522fc03b/zdevice"
	"prescreen.local/ps-7e522fc03b/zexec"
	"prescreen.local/ps-7e522fc03b/zplanner"
)

func Example() {
	dir, err := os.MkdirTemp("", "zexec-example-*")
	must(err)
	dev := zdevice.NewDevice(dir)
	must(dev.Format())

	ops := []zplanner.Op{
		{ID: 0, Kind: zplanner.OpWrite, Writes: map[string]zplanner.CID{"a": 1}},
		{ID: 1, Kind: zplanner.OpSnapshot, Name: "snap"},
		{ID: 2, Kind: zplanner.OpWrite, Writes: map[string]zplanner.CID{"a": 2}},
	}

	ex := zexec.NewExecutor(dev, ops)
	must(ex.Resume(0))
	applied, err := ex.Run()
	must(err)

	tbl := ex.Table()
	fmt.Printf("applied=%d rootA=%d snapshotA=%d\n",
		applied, tbl.Roots["a"], tbl.Snaps["snap"]["a"])

	// Output:
	// applied=3 rootA=2 snapshotA=1
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
