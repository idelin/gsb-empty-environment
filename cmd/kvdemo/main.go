// Command kvdemo demonstrates the kvdb store: append-only writes with
// tombstones, segment rotation, compaction, and crash recovery.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	kvdb "example.com/kvdb"
)

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func listFiles(dir string) string {
	entries, err := ioutil.ReadDir(dir)
	check(err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func main() {
	dir := flag.String("dir", "", "data directory (default: a fresh temp dir)")
	flag.Parse()
	d := *dir
	if d == "" {
		tmp, err := ioutil.TempDir("", "kvdb-demo")
		check(err)
		defer os.RemoveAll(tmp)
		d = tmp
	} else {
		check(os.RemoveAll(d))
	}
	fmt.Println("data dir:", d)

	opts := &kvdb.Options{MaxSegmentBytes: 256, SyncWrites: true}
	db, err := kvdb.Open(d, opts)
	check(err)

	fmt.Println("\n-- write/delete/write the same key --")
	check(db.Put([]byte("alpha"), []byte("v1")))
	check(db.Delete([]byte("alpha")))
	check(db.Put([]byte("alpha"), []byte("v2")))
	v, err := db.Get([]byte("alpha"))
	fmt.Printf("Get(alpha) = %q, err = %v  (only the last write is visible)\n", v, err)

	fmt.Println("\n-- fill several segments to force rotation --")
	for i := 0; i < 40; i++ {
		key := fmt.Sprintf("key-%02d", i)
		check(db.Put([]byte(key), []byte(fmt.Sprintf("value-%02d", i))))
		if i%10 == 9 {
			check(db.Delete([]byte(fmt.Sprintf("key-%02d", i-5))))
		}
	}
	fmt.Println("files:", listFiles(d))

	fmt.Println("\n-- compact: keep only visible records --")
	check(db.Compact())
	fmt.Println("files:", listFiles(d))
	v, _ = db.Get([]byte("alpha"))
	fmt.Printf("Get(alpha) = %q after compaction\n", v)
	if _, err := db.Get([]byte("key-04")); err == kvdb.ErrNotFound {
		fmt.Println("Get(key-04) = ErrNotFound (tombstone survived compaction)")
	}

	fmt.Println("\n-- simulate a crash: reopen without Close --")
	check(db.Put([]byte("durable"), []byte("survives-crash")))
	db2, err := kvdb.Open(d, opts)
	check(err)
	v, err = db2.Get([]byte("durable"))
	fmt.Printf("Get(durable) = %q, err = %v after crash recovery\n", v, err)
	v, err = db2.Get([]byte("alpha"))
	fmt.Printf("Get(alpha)   = %q, err = %v after crash recovery\n", v, err)
	check(db2.Close())
	fmt.Println("\ndone.")
}
