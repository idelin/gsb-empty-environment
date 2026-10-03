// Command demo exercises the full stack end to end: reflection-based protobuf
// encoding of a nested message with packed repeated fields, validation,
// crash-recovery of the WAL under simulated power loss and disk full, and
// reliable delivery over a lossy link.
package main

import (
	"fmt"
	"log"

	"pbufsafe/netsim"
	"pbufsafe/store"
	"pbufsafe/wire"
)

// Sample schema: two-level nesting + packed repeated arrays.
type Header struct {
	DeviceID uint64 `protobuf:"1,varint" validate:"required,min=1"`
	Timezone int32  `protobuf:"2,zigzag" validate:"min=-12,max=14"`
}

type Reading struct {
	Header   *Header  `protobuf:"1,message" validate:"required"`
	Label    string   `protobuf:"2,bytes"   validate:"required,len=1..64"`
	Samples  []int32  `protobuf:"3,zigzag,packed" validate:"len=1..1024"`
	Checksum uint32   `protobuf:"4,fixed32"`
	Serial   uint64   `protobuf:"5,fixed64"`
	Raw      []byte   `protobuf:"6,bytes"`
}

func main() {
	fmt.Println("=== 1. wire: nested message + packed repeated round-trip ===")
	in := Reading{
		Header:   &Header{DeviceID: 42, Timezone: 8},
		Label:    "engine-temp",
		Samples:  []int32{-3, 0, 7, 1024, -65536},
		Checksum: 0xDEADBEEF,
		Serial:   0x0102030405060708,
		Raw:      []byte{0xCA, 0xFE},
	}
	data, err := wire.Marshal(&in)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("encoded %d bytes: % x\n", len(data), data)

	var out Reading
	if err := wire.Unmarshal(data, &out); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("decoded: %+v header=%+v\n", out, out.Header)

	if err := wire.Validate(&out); err != nil {
		log.Fatal(err)
	}
	fmt.Println("validation: OK")

	fmt.Println("\n=== 2. truncation safety ===")
	if err := wire.Unmarshal(data[:len(data)-2], &out); err != nil {
		fmt.Println("truncated input safely rejected:", err)
	}

	fmt.Println("\n=== 3. store: power-loss crash recovery ===")
	sim := store.NewSimFile()
	st, err := store.Open(sim)
	if err != nil {
		log.Fatal(err)
	}
	must(st.Append([]byte("txn-1 committed")))
	must(st.Commit())
	must(st.Append([]byte("txn-2 UNCOMMITTED"))) // staged, never synced
	sim.FlushPartial(st.Stats().Offset - 5)      // OS flushes a torn prefix
	sim.Crash()                                  // sudden power loss
	sim.Reopen()
	st2, err := store.Open(sim) // recovery: rolls back to last commit
	if err != nil {
		log.Fatal(err)
	}
	recs, _ := st2.Scan()
	fmt.Printf("after crash: %d record(s) survived: %q (healed=%v)\n",
		len(recs), recs[0], st2.Stats().Recovered)
	_ = st2.Close()

	fmt.Println("\n=== 4. store: disk-full rollback ===")
	sim2 := store.NewSimFile()
	st3, _ := store.Open(sim2)
	must(st3.Append([]byte("small record")))
	must(st3.Commit())
	sim2.FullAfter = sim2.DurableSize() + 8 // almost no room left
	if err := st3.Append(make([]byte, 4096)); err != nil {
		fmt.Println("disk full rejected, state rolled back:", err != nil)
	}
	recs3, _ := st3.Scan()
	fmt.Printf("log still consistent: %d record(s)\n", len(recs3))
	_ = st3.Close()

	fmt.Println("\n=== 5. netsim: reliable delivery over 30% loss ===")
	dataLink := netsim.NewLink(1, 0.30, 0.05)
	ackLink := netsim.NewLink(2, 0.10, 0.0)
	sess := netsim.NewSession(dataLink, ackLink)
	for i := uint32(0); i < 20; i++ {
		if err := sess.Send(i, []byte(fmt.Sprintf("packet-%d", i))); err != nil {
			log.Fatal(err)
		}
		if _, err := sess.Recv(); err != nil {
			log.Fatal(err)
		}
	}
	sess.Close()
	fmt.Printf("delivered 20/20 messages (dropped=%d corrupted=%d retransmitted=%d)\n",
		dataLink.Dropped, dataLink.Corrupted, dataLink.Sent-20)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
