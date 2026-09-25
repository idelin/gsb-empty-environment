// Command demo exercises the rootstore protocol end to end: real-file
// commits, plus simulated torn writes, lost syncs, corruption and the
// process-crash vs power-loss boundary.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"hash/crc32"
	"os"

	"rootstore"
)

func buildTree(tag string) func(w *rootstore.PageWriter) (uint64, error) {
	return func(w *rootstore.PageWriter) (uint64, error) {
		l1, err := w.New(nil, []byte(tag+"-leaf-1"))
		if err != nil {
			return 0, err
		}
		l2, err := w.New(nil, []byte(tag+"-leaf-2"))
		if err != nil {
			return 0, err
		}
		return w.New([]uint64{l1, l2}, []byte(tag+"-root"))
	}
}

func commit(fs rootstore.FS, tag string) rootstore.Slot {
	st, err := rootstore.Open(fs)
	if err != nil {
		fmt.Println("  open:", err)
		os.Exit(1)
	}
	defer st.Close()
	if err := st.Commit(buildTree(tag)); err != nil {
		fmt.Println("  commit:", err)
		os.Exit(1)
	}
	slot, _ := st.Root()
	return slot
}

func reopen(fs rootstore.FS) {
	st, err := rootstore.Open(fs)
	if err != nil {
		fmt.Println("  reopen refused:", err)
		return
	}
	defer st.Close()
	slot, _ := st.Root()
	err = rootstore.VerifyClosure(st.Pages(), slot.RootID)
	fmt.Printf("  recovered root: %v, closure verify: %v\n", slot, err)
}

func main() {
	dir := flag.String("dir", "", "directory for the real-file demo (default: temp dir)")
	flag.Parse()
	if *dir == "" {
		d, err := os.MkdirTemp("", "rootstore-demo")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(d)
		*dir = d
	}

	fmt.Println("== 1. commits on real files in", *dir)
	real := rootstore.OSFS{Dir: *dir}
	fmt.Println("  commit A ->", commit(real, "A"))
	fmt.Println("  commit B ->", commit(real, "B"))
	reopen(real)

	fmt.Println("== 2. torn slot publish + power loss (simulator)")
	sim := rootstore.NewSimFS()
	fmt.Println("  commit A ->", commit(sim, "A"))
	sim.Hook = func(op *rootstore.Op) {
		if op.File == rootstore.MetaFileName && op.Kind == rootstore.OpWrite {
			op.Data = op.Data[:40] // only 40 of 128 bytes land
		}
	}
	fmt.Println("  commit B (slot write torn) ->", commit(sim, "B"))
	sim.Hook = nil
	sim.Crash(rootstore.PowerLoss)
	reopen(sim)

	fmt.Println("== 3. both slots corrupt")
	sim2 := rootstore.NewSimFS()
	fmt.Println("  commit A ->", commit(sim2, "A"))
	meta2, _ := sim2.Open(rootstore.MetaFileName)
	meta2.WriteAt(make([]byte, 2*rootstore.SlotSize), 0)
	meta2.Sync()
	reopen(sim2)

	fmt.Println("== 4. same sequence, different content")
	sim3 := rootstore.NewSimFS()
	fmt.Println("  commit A ->", commit(sim3, "A"))
	meta3, _ := sim3.Open(rootstore.MetaFileName)
	buf := make([]byte, rootstore.SlotSize)
	meta3.ReadAt(buf, 0)
	meta3.WriteAt(buf, rootstore.SlotSize) // duplicate slot 0 into slot 1
	meta3.Sync()
	fmt.Println("  identical duplicate publish:")
	reopen(sim3)
	// Forge slot 1: same seq, different Pages field, checksum fixed up, so
	// both slots are individually valid but disagree at the same seq.
	slot1 := make([]byte, rootstore.SlotSize)
	meta3.ReadAt(slot1, rootstore.SlotSize)
	slot1[32]++ // Pages field
	binary.LittleEndian.PutUint32(slot1[40:44],
		crc32.Checksum(slot1[:40], crc32.MakeTable(crc32.Castagnoli)))
	meta3.WriteAt(slot1, rootstore.SlotSize)
	meta3.Sync()
	fmt.Println("  forged same-seq slot:")
	reopen(sim3)

	fmt.Println("== 5. process crash vs power loss (meta sync lost)")
	for _, mode := range []rootstore.CrashMode{rootstore.ProcessCrash, rootstore.PowerLoss} {
		sim5 := rootstore.NewSimFS()
		commit(sim5, "A")
		sim5.Hook = func(op *rootstore.Op) {
			if op.File == rootstore.MetaFileName && op.Kind == rootstore.OpSync {
				op.Drop = true
			}
		}
		commit(sim5, "B")
		sim5.Hook = nil
		sim5.Crash(mode)
		if mode == rootstore.ProcessCrash {
			fmt.Println("  process crash (OS buffers survive):")
		} else {
			fmt.Println("  power loss (only synced bytes survive):")
		}
		reopen(sim5)
	}
}
