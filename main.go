// Demo: hand-rolled protobuf wire codec + reflection validation engine +
// incremental snapshot / checkpoint archiving. Zero external dependencies.
package main

import (
	"fmt"
	"io/ioutil"
	"os"

	"compact/proto"
	"compact/snapshot"
)

type Account struct {
	ID       uint64   `wire:"1,varint" check:"required,min=1"`
	Balance  int64    `wire:"2,zigzag"`
	Name     string   `wire:"3,bytes"  check:"required,minlen=1,maxlen=32"`
	Limits   []uint64 `wire:"4,varint,packed"`
	History  []int64  `wire:"5,zigzag,packed"`
	Metadata *Meta    `wire:"6,bytes"`
}

type Meta struct {
	Region string  `wire:"1,bytes" check:"required"`
	Score  float64 `wire:"2,fixed64" check:"min=0,max=100"`
}

func main() {
	fmt.Println("=== 1. Protobuf wire codec: nested + packed round trip ===")
	src := &Account{
		ID:      9001,
		Balance: -123456,
		Name:    "alice",
		Limits:  []uint64{100, 200, 300, 1 << 40},
		History: []int64{-5, 10, -20, 40},
		Metadata: &Meta{
			Region: "cn-shanghai",
			Score:  98.5,
		},
	}
	data, err := proto.Encode(src)
	must(err)
	fmt.Printf("encoded %d bytes: %x\n", len(data), data)

	var dst Account
	must(proto.Decode(data, &dst))
	fmt.Printf("decoded: ID=%d Balance=%d Name=%q Limits=%v History=%v Meta=%+v\n",
		dst.ID, dst.Balance, dst.Name, dst.Limits, dst.History, dst.Metadata)

	fmt.Println("\n=== 2. Reflection validation engine ===")
	must(proto.Validate(src))
	fmt.Println("valid: OK")
	bad := &Account{ID: 0, Name: "", Metadata: &Meta{Region: "", Score: 150}}
	fmt.Println("invalid:", proto.Validate(bad))

	fmt.Println("\n=== 3. Truncation safety ===")
	var trunc Account
	if err := proto.Decode(data[:len(data)-3], &trunc); err != nil {
		fmt.Println("truncated stream rejected:", err)
	}

	fmt.Println("\n=== 4. Incremental snapshot & checkpoint archive ===")
	dir, err := ioutil.TempDir("", "compact-demo-")
	must(err)
	defer os.RemoveAll(dir)
	arch, err := snapshot.NewArchiver(dir)
	must(err)

	store := snapshot.NewStore()
	for i := 0; i < 10000; i++ {
		store.Put(fmt.Sprintf("acct-%05d", i), data)
	}
	cv, err := arch.Checkpoint(store)
	must(err)
	fmt.Printf("checkpoint v%d archived (%d keys)\n", cv, store.Len())

	// incremental mutations after the checkpoint
	store.Put("acct-00001", []byte("updated"))
	store.Del("acct-00002")
	dv, err := arch.ArchiveDelta(store, cv)
	must(err)
	fmt.Printf("delta v%d -> v%d archived\n", cv, dv)

	restored, rv, err := arch.Load()
	must(err)
	fmt.Printf("restored at v%d: %d keys, acct-00001=%q, acct-00002 present=%v\n",
		rv, restored.Len(), mustGet(restored, "acct-00001"), has(restored, "acct-00002"))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func mustGet(s *snapshot.Store, k string) string {
	v, _ := s.Get(k)
	if len(v) > 12 {
		return string(v[:12]) + "..."
	}
	return string(v)
}

func has(s *snapshot.Store, k string) bool {
	_, ok := s.Get(k)
	return ok
}
