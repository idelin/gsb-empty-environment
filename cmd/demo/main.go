// Command demo exercises the full pipeline: hand-rolled protobuf wire
// encoding, reflection-based schema validation, and incremental snapshots
// with checkpoint archiving.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"wiresnap/schema"
	"wiresnap/snapshot"
)

type Vec3 struct {
	X float32 `protobuf:"fixed32,1"`
	Y float32 `protobuf:"fixed32,2"`
	Z float32 `protobuf:"fixed32,3"`
}

type Player struct {
	ID      uint64   `protobuf:"varint,1"`
	Name    string   `protobuf:"bytes,2"`
	Balance int64    `protobuf:"zigzag,3"`
	Pos     Vec3     `protobuf:"message,4"`
	Home    *Vec3    `protobuf:"message,5"`
	Scores  []int32  `protobuf:"zigzag,6"`
	Tags    []uint32 `protobuf:"varint,7"`
}

func main() {
	// ---- 1. Reflection schema validation ----
	if err := schema.Validate(Player{}); err != nil {
		log.Fatalf("schema invalid: %v", err)
	}
	fmt.Println("[schema] Player schema validated OK")

	// ---- 2. Marshal / unmarshal a nested, packed message ----
	in := Player{
		ID:      9007199254740993,
		Name:    "爱丽丝",
		Balance: -9876543210,
		Pos:     Vec3{X: 1.5, Y: -2.25, Z: 3.75},
		Home:    &Vec3{X: 0, Y: 0, Z: 1},
		Scores:  []int32{-100, 0, 100, 2147483647, -2147483648},
		Tags:    []uint32{7, 77, 777, 4294967295},
	}
	data, err := schema.Marshal(in)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}
	fmt.Printf("[wire] encoded %d bytes: %x...\n", len(data), data[:16])

	var out Player
	if err := schema.Unmarshal(data, &out); err != nil {
		log.Fatalf("unmarshal: %v", err)
	}
	fmt.Printf("[wire] decoded: ID=%d Name=%s Balance=%d Pos=%+v Scores=%v Tags=%v\n",
		out.ID, out.Name, out.Balance, out.Pos, out.Scores, out.Tags)

	// ---- 3. Truncation safety demo ----
	truncated := data[:len(data)/2]
	if err := schema.Unmarshal(truncated, &out); err != nil {
		fmt.Printf("[wire] truncated input safely rejected: %v\n", err)
	}

	// ---- 4. Incremental snapshots + checkpoint archive ----
	store := snapshot.NewStore()
	sn := snapshot.NewSnapshotter(store)
	dir := filepath.Join(os.TempDir(), "wiresnap-demo-checkpoints")
	os.RemoveAll(dir)
	arc, err := snapshot.OpenArchive(dir)
	if err != nil {
		log.Fatal(err)
	}

	for i := 0; i < 100000; i++ {
		store.Set(fmt.Sprintf("acct:%06d", i), uint64(i)*13)
	}
	full := sn.Full()
	path, _ := arc.Append(full)
	fi, _ := os.Stat(path)
	fmt.Printf("[snap] full checkpoint: %d entries, %d bytes on disk (%s)\n",
		len(full.Entries), fi.Size(), filepath.Base(path))

	// Only 3 mutations out of 100k keys -> tiny incremental checkpoint.
	store.Set("acct:000001", 111)
	store.Set("acct:000002", 222)
	store.Delete("acct:000003")
	inc := sn.Incremental()
	path, _ = arc.Append(inc)
	fi, _ = os.Stat(path)
	fmt.Printf("[snap] incremental checkpoint: %d entries, %d bytes on disk (%s)\n",
		len(inc.Entries), fi.Size(), filepath.Base(path))

	// ---- 5. Restore from checkpoints ----
	restored := snapshot.NewStore()
	if err := arc.Restore(restored); err != nil {
		log.Fatal(err)
	}
	v1, _ := restored.Get("acct:000001")
	_, deleted := restored.Get("acct:000003")
	vLast, _ := restored.Get("acct:099999")
	fmt.Printf("[snap] restored: %d keys, acct:000001=%d, acct:000003 deleted=%v, acct:099999=%d, version=%d\n",
		restored.Len(), v1, !deleted, vLast, restored.Version())
	os.RemoveAll(dir)
}
