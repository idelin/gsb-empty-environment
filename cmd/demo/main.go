// Command demo exercises the wire codec, reflection schema engine and the
// crash-safe store under injected power loss, disk-full and packet loss.
package main

import (
	"fmt"
	"strings"

	"wirefmt/schema"
	"wirefmt/store"
)

type Point struct {
	X int32 `protobuf:"1,zigzag"`
	Y int32 `protobuf:"2,zigzag"`
}

type Track struct {
	ID      uint64    `protobuf:"1,varint"`
	Name    string    `protobuf:"2,string"`
	Points  []*Point  `protobuf:"3,message"`
	Samples []int64   `protobuf:"4,packed,zigzag"`
	Ratios  []float64 `protobuf:"5,packed,fixed64"`
}

func main() {
	fmt.Println("=== 1. Reflection schema validation ===")
	info, err := schema.Validate(Track{})
	must("Validate", err)
	for _, f := range info.Fields {
		fmt.Printf("  field #%d codec=%-7s packed=%v\n", f.Number, f.Codec, f.Packed)
	}

	fmt.Println("\n=== 2. Hand-built protobuf binary round trip ===")
	in := &Track{
		ID:   300,
		Name: "route-α",
		Points: []*Point{
			{X: -1, Y: 2},
			{X: 300, Y: -400},
		},
		Samples: []int64{-5, 0, 5, 1 << 40},
		Ratios:  []float64{0.5, 2.75},
	}
	data, err := schema.Marshal(in)
	must("Marshal", err)
	fmt.Printf("  encoded %d bytes:\n  % x\n", len(data), data)

	var out Track
	must("Unmarshal", schema.Unmarshal(data, &out))
	fmt.Printf("  decoded: ID=%d Name=%q Samples=%v Ratios=%v\n", out.ID, out.Name, out.Samples, out.Ratios)
	for i, p := range out.Points {
		fmt.Printf("  point[%d] = (%d, %d)\n", i, p.X, p.Y)
	}

	fmt.Println("\n=== 3. Truncation: safe bounds guard ===")
	cut := len(data) - 3
	var broken Track
	err = schema.Unmarshal(data[:cut], &broken)
	fmt.Printf("  stream cut at %d/%d bytes -> error: %v\n", cut, len(data), err)

	fmt.Println("\n=== 4. Crash recovery under power loss & disk full ===")
	dev := store.NewCrashStorage(store.NewMemStorage())
	wal, err := store.Open(dev)
	must("Open", err)
	for i := 0; i < 3; i++ {
		must("Append", wal.Append([]byte(fmt.Sprintf("committed-tx-%d", i))))
	}
	fmt.Printf("  committed %d transactions (%d durable bytes)\n", wal.Count(), wal.CommittedBytes())

	dev.ArmPowerLoss(4) // next write is torn after 4 bytes: sudden power cut
	err = wal.Append([]byte("tx-that-dies-mid-write"))
	fmt.Printf("  power loss during append -> %v\n", err)
	fmt.Printf("  state rolled back: count=%d durable=%d bytes\n", wal.Count(), wal.CommittedBytes())

	dev.SetDiskFull(true)
	err = wal.Append([]byte("tx-no-space"))
	fmt.Printf("  disk full during append -> %v\n", err)
	dev.SetDiskFull(false)
	must("Append after heal", wal.Append([]byte("tx-after-recovery")))
	fmt.Println("  disk healed, append succeeded again")

	reopened, err := store.Open(dev) // simulate restart after the storm
	must("reopen", err)
	fmt.Printf("  after restart, recovered records:\n")
	for i, r := range reopened.Records() {
		fmt.Printf("    [%d] %s\n", i, r)
	}

	fmt.Println("\n=== 5. Lossy network replication with retry ===")
	link := store.NewLossyTransport(0.4, 99) // 40% packet loss
	replica, err := store.Open(store.NewMemStorage())
	must("replica open", err)
	for _, r := range reopened.Records() {
		frame := r
		for {
			err := link.Send(frame, func(f []byte) error { return replica.Append(f) })
			if err == store.ErrPacketLoss {
				continue // dropped: retransmit
			}
			must("send", err)
			break
		}
	}
	sent, dropped := link.Stats()
	fmt.Printf("  %d frames delivered, %d dropped, replica holds %d records\n",
		sent, dropped, replica.Count())
	fmt.Println("\nAll scenarios passed: " + strings.Repeat("=", 20))
}

func must(what string, err error) {
	if err != nil {
		panic(fmt.Sprintf("%s: %v", what, err))
	}
}
