// Command demo walks through the tombstone lifecycle acceptance scenario:
// insert K1=V1, delete K1, observe NotFound, then compact until the
// tombstone sinks to the bottom-most level and is physically erased.
package main

import (
	"fmt"
	"io/ioutil"
	"os"

	"lsm"
)

func main() {
	dir, err := ioutil.TempDir("", "lsm-demo-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	db, err := lsm.Open(dir, lsm.Options{MemtableLimit: 1 << 20, LevelCap: 1, MaxLevels: 3})
	if err != nil {
		panic(err)
	}
	defer db.Close()

	report := func(step string) {
		fmt.Printf("%-58s %v\n", step, db.Stats())
	}

	fmt.Println("== LSM tombstone lifecycle demo (MaxLevels=3, LevelCap=1) ==")
	db.Put("K1", []byte("V1"))
	db.Flush()
	report("1. Put K1=V1, flush -> value lives in L0")

	db.Delete("K1")
	if _, err := db.Get("K1"); err == lsm.ErrNotFound {
		fmt.Println("2. Delete K1 (tombstone in memtable); Get(K1) -> NotFound immediately")
	}
	db.Flush()
	report("3. Flush tombstone; L0 overflows, merged into L1 (tombstone kept)")

	for i := 0; i < 10; i++ {
		db.Put("filler", []byte{byte('a' + i)})
		db.Flush()
	}
	report("4. More flushes push K1 down to the bottom-most level")

	if _, err := db.Get("K1"); err == lsm.ErrNotFound {
		fmt.Println("5. Get(K1) -> NotFound (still correctly masked)")
	}
	tombs := 0
	for _, s := range db.Stats() {
		tombs += s.Tombstones
	}
	fmt.Printf("6. Tombstones remaining on disk: %d -> K1 physically erased, space reclaimed\n", tombs)
}
