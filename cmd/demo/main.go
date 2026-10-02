
// Command demo runs the acceptance scenario end to end:
//  1. load data into the LSM engine and export a hard-link snapshot
//  2. keep writing heavily, export a second (incremental) snapshot
//  3. compact, which deletes the original SSTables from the live dir
//  4. verify both snapshots are untouched and fully readable
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"example.com/lsmbackup/backup"
	"example.com/lsmbackup/lsm"
)

func main() {
	base, err := ioutil.TempDir("", "lsm-backup-demo-")
	must(err)
	defer os.RemoveAll(base)

	e, err := lsm.Open(filepath.Join(base, "db"))
	must(err)
	x, err := backup.NewExporter(e.Dir(), filepath.Join(base, "backups"))
	must(err)

	fmt.Println("== phase 1: load 3 generations x 2000 keys ==")
	for gen := 0; gen < 3; gen++ {
		load(e, gen, 2000)
	}
	fmt.Printf("live sstables: %v\n", e.SSTables())

	fmt.Println("\n== phase 2: export snapshot #1 (hard links, zero copy) ==")
	start := time.Now()
	snap1, err := x.Export()
	must(err)
	fmt.Printf("exported %d sstables to %s in %v\n",
		len(snap1.Manifest.Files), filepath.Base(snap1.Dir), time.Since(start))
	for _, fe := range snap1.Manifest.Files {
		fmt.Printf("  %-18s %8d bytes  crc32=%08x\n", fe.Name, fe.Size, fe.CRC32)
	}

	fmt.Println("\n== phase 3: heavy foreground writes (5 more generations) ==")
	for gen := 3; gen < 8; gen++ {
		load(e, gen, 2000)
	}
	fmt.Printf("live sstables: %v\n", e.SSTables())

	fmt.Println("\n== phase 4: export snapshot #2 (incremental) ==")
	snap2, err := x.Export()
	must(err)
	fmt.Printf("snapshot #2 full manifest: %d sstables\n", len(snap2.Manifest.Files))
	fmt.Printf("snapshot #2 incremental list (new since #1): %v\n", snap2.Added)

	fmt.Println("\n== phase 5: compaction deletes the original files ==")
	must(e.Compact())
	fmt.Printf("live sstables after compaction: %v\n", e.SSTables())
	for _, fe := range snap1.Manifest.Files {
		_, err := os.Stat(filepath.Join(e.Dir(), fe.Name))
		fmt.Printf("  original %-18s deleted from live dir: %v\n", fe.Name, os.IsNotExist(err))
	}

	fmt.Println("\n== phase 6: verify both snapshots ==")
	must(backup.Verify(snap1.Dir, snap1.Manifest))
	fmt.Println("snapshot #1 CRC verification: OK (data intact after compaction)")
	must(backup.Verify(snap2.Dir, snap2.Manifest))
	fmt.Println("snapshot #2 CRC verification: OK")

	fmt.Println("\nALL CHECKS PASSED")
}

func load(e *lsm.Engine, gen, n int) {
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("user-%06d", i)
		val := fmt.Sprintf("gen%d-%0100d", gen, i)
		if err := e.Put(key, val); err != nil {
			panic(err)
		}
	}
	if _, err := e.Flush(); err != nil {
		panic(err)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
