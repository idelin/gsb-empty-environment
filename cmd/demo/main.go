// Command demo is an end-to-end sample: it hand-encodes a nested
// protobuf message with a packed array, decodes it through the
// reflection validation engine, demonstrates truncation guards, then
// runs a deterministic simulation, injects a fault, and reproduces it.
package main

import (
	"errors"
	"fmt"
	"math"
	"time"

	"pbufsim/sim"
	"pbufsim/wire"
)

type Address struct {
	Zip  uint32 `wire:"1,fixed32"`
	Code int64  `wire:"2,sint"`
}

type Person struct {
	ID     uint64   `wire:"1,varint"`
	Name   string   `wire:"2,string"`
	Scores []int64  `wire:"3,packed,sint"`
	Addr   *Address `wire:"4,message"`
}

func encodePerson() []byte {
	var addr []byte
	addr = wire.AppendTag(addr, 1, wire.Fixed32)
	addr = wire.AppendFixed32(addr, 100084)
	addr = wire.AppendTag(addr, 2, wire.Varint)
	addr = wire.AppendVarint(addr, wire.ZigZagEncode(-7))

	var packed []byte
	for _, s := range []int64{3, -1, 42, math.MinInt64} {
		packed = wire.AppendVarint(packed, wire.ZigZagEncode(s))
	}

	var buf []byte
	buf = wire.AppendTag(buf, 1, wire.Varint)
	buf = wire.AppendVarint(buf, 12345)
	buf = wire.AppendTag(buf, 2, wire.LengthDelimited)
	buf = wire.AppendBytes(buf, []byte("Ada"))
	buf = wire.AppendTag(buf, 3, wire.LengthDelimited)
	buf = wire.AppendBytes(buf, packed)
	buf = wire.AppendTag(buf, 4, wire.LengthDelimited)
	buf = wire.AppendBytes(buf, addr)
	return buf
}

func codecDemo() {
	fmt.Println("== protobuf wire codec ==")
	buf := encodePerson()
	fmt.Printf("encoded %d bytes: %x\n", len(buf), buf)

	var p Person
	if err := wire.Unmarshal(buf, &p); err != nil {
		panic(err)
	}
	fmt.Printf("decoded: id=%d name=%q scores=%v addr={zip:%d code:%d}\n",
		p.ID, p.Name, p.Scores, p.Addr.Zip, p.Addr.Code)

	truncated := buf[:len(buf)-3]
	if _, err := wire.Parse(truncated); err != nil {
		fmt.Printf("truncation guard: %v\n", err)
	}
	if err := wire.Validate(truncated, &Person{}); errors.Is(err, wire.ErrTruncated) {
		fmt.Println("validator agrees: truncated input rejected safely")
	}
}

func simDemo() {
	fmt.Println("== deterministic simulation ==")
	workload := func(bugAt uint64) sim.Workload {
		return func(e *sim.Engine, emit sim.Emit) {
			var state, step uint64
			var tick sim.EventFunc
			tick = func(e *sim.Engine) {
				step++
				state = state*31 + uint64(e.Intn(97))
				if bugAt > 0 && step == bugAt {
					state = 0xDEADBEEF // injected fault
				}
				emit("tick", state)
				e.Schedule(uint64(e.Intn(3)+1), 0, "tick", tick)
			}
			e.Schedule(0, 0, "tick", tick)
		}
	}
	invariant := func(step uint64, tag string, state uint64) error {
		if state == 0xDEADBEEF {
			return fmt.Errorf("invariant violated: poison state at step %d", step)
		}
		return nil
	}

	const steps = 1_000_000
	start := time.Now()
	clean := sim.RunWorkloadNoTrace(20261002, steps, workload(0), invariant)
	fmt.Printf("1M steps in %v, trace hash %016x, err=%v\n",
		time.Since(start).Round(time.Millisecond), clean.Hash, clean.Err)

	const bugAt = 500_000
	bad := sim.RunWorkloadNoTrace(20261002, steps, workload(bugAt), invariant)
	ok, _ := sim.Reproduce(bad, steps, workload(bugAt), invariant)
	fmt.Println("bug hunt:", sim.ReproReport(bad, ok))
}

func main() {
	codecDemo()
	simDemo()
}
