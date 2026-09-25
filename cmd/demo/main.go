// Command demo exercises the pagestore transaction lifecycle, injecting
// crashes at each interesting point and reopening the directory to show
// what recovery guarantees. Usage: go run ./cmd/demo [dir]
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"pagestore"
)

var errCrash = errors.New("simulated crash")

func crash() func() error { return func() error { return errCrash } }

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func reopen(dir string) *pagestore.DB {
	db, err := pagestore.Open(dir) // runs crash recovery
	must(err)
	return db
}

func show(db *pagestore.DB, id uint64) {
	data, ver, err := db.ReadPage(id)
	must(err)
	fmt.Printf("    page %d: version=%d data=%q\n", id, ver, string(trimZeros(data)))
}

func trimZeros(b []byte) []byte {
	end := len(b)
	for end > 0 && b[end-1] == 0 {
		end--
	}
	return b[:end]
}

func main() {
	dir := filepath.Join(os.TempDir(), "pagestore-demo")
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	os.RemoveAll(dir)
	fmt.Println("demo dir:", dir)

	fmt.Println("1) txn1 commits pages 0,1 and checkpoints")
	db := reopen(dir)
	tx := db.Begin()
	must(tx.Set(0, []byte("hello")))
	must(tx.Set(1, []byte("world")))
	must(tx.Commit())
	must(db.Checkpoint())
	show(db, 0)
	show(db, 1)

	fmt.Println("2) txn2 crashes BEFORE the commit fsync -> discarded, no partial writes")
	db.SetFailpoint(pagestore.FailCommitBeforeSync, crash())
	tx = db.Begin()
	must(tx.Set(0, []byte("HALF")))
	must(tx.Set(1, []byte("HALF")))
	fmt.Println("   commit err:", tx.Commit())
	db = reopen(dir)
	show(db, 0)
	show(db, 1)

	fmt.Println("3) txn3 crashes AFTER the commit fsync -> replayed by recovery")
	db.SetFailpoint(pagestore.FailCommitAfterSync, crash())
	tx = db.Begin()
	must(tx.Set(0, []byte("durable")))
	fmt.Println("   commit err:", tx.Commit())
	db = reopen(dir)
	show(db, 0)
	must(db.Checkpoint())

	fmt.Println("4) power loss halfway through a page write -> checksum catches it, WAL repairs it")
	tx = db.Begin()
	must(tx.Set(2, []byte("torn-target")))
	must(tx.Commit())
	db.SetFailpoint(pagestore.FailTornPageWrite, func() error { return nil })
	fmt.Println("   checkpoint err:", db.Checkpoint())
	db = reopen(dir)
	show(db, 2)

	fmt.Println("5) crash while appending to the WAL -> torn tail detected and truncated")
	walPath := filepath.Join(dir, "wal.log")
	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0)
	must(err)
	_, err = f.Write([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01})
	must(err)
	must(f.Close())
	db = reopen(dir)
	fmt.Println("   reopened OK; committed data intact:")
	show(db, 0)
	must(db.Close())
	fmt.Println("done.")
}
