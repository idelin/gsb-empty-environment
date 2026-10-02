// Command demo shows hand-built protobuf encoding/parsing and the adaptive
// sparse-set optimizer in action.
package main

import (
	"fmt"
	"math/rand"

	"protokit/sparse"
	"protokit/wire"
)

func main() {
	fmt.Println("=== Protobuf wire-format: nested message + packed repeated ===")

	// Hand-build: User{1: id, 2: scores(packed varint), 3: Address{1: city, 2: zip}}
	var addr []byte
	addr = wire.AppendTag(addr, 1, wire.Bytes)
	addr = wire.AppendBytes(addr, []byte("Shanghai"))
	addr = wire.AppendTag(addr, 2, wire.Varint)
	addr = wire.AppendZigZag(addr, -200000)

	var msg []byte
	msg = wire.AppendTag(msg, 1, wire.Varint)
	msg = wire.AppendVarint(msg, 1024)
	var packed []byte
	for _, v := range []uint64{98, 99, 100, 65536} {
		packed = wire.AppendVarint(packed, v)
	}
	msg = wire.AppendTag(msg, 2, wire.Bytes)
	msg = wire.AppendBytes(msg, packed)
	msg = wire.AppendTag(msg, 3, wire.Bytes)
	msg = wire.AppendBytes(msg, addr)

	fmt.Printf("encoded %d bytes: %x\n", len(msg), msg)

	r := wire.NewReader(msg)
	for {
		f, ok, err := r.Next()
		if err != nil {
			fmt.Println("parse error:", err)
			break
		}
		if !ok {
			break
		}
		switch {
		case f.Number == 1:
			fmt.Printf("field 1 (id):            %d\n", f.Varint)
		case f.Number == 2:
			vals, _ := wire.PackedVarints(f.Data, nil)
			fmt.Printf("field 2 (packed scores): %v\n", vals)
		case f.Number == 3:
			fmt.Println("field 3 (nested Address):")
			ar := wire.NewReader(f.Data)
			for {
				af, ok, err := ar.Next()
				if err != nil || !ok {
					break
				}
				if af.Number == 1 {
					fmt.Printf("  field 1 (city):        %s\n", af.Data)
				} else {
					fmt.Printf("  field 2 (zip, zigzag): %d\n", af.ZigZag())
				}
			}
		}
	}

	// Truncation safety demo.
	fmt.Println("\n=== Truncation safety ===")
	truncated := msg[:len(msg)-3]
	tr := wire.NewReader(truncated)
	for {
		_, ok, err := tr.Next()
		if err != nil {
			fmt.Printf("truncated stream safely rejected: %v\n", err)
			break
		}
		if !ok {
			break
		}
	}

	fmt.Println("\n=== Sparse optimizer: adaptive backends ===")
	report := func(name string, fill func(s *sparse.Set)) {
		s := sparse.New()
		fill(s)
		backend, bytes, card := s.Stats()
		enc := s.Marshal()
		fmt.Printf("%-10s card=%-7d backend=%-6s mem=%-8d encoded=%dB (raw %dB)\n",
			name, card, backend, bytes, len(enc), card*4)
	}
	report("sparse", func(s *sparse.Set) {
		rng := rand.New(rand.NewSource(1))
		for i := 0; i < 5000; i++ {
			s.Add(uint32(rng.Intn(1 << 28)))
		}
	})
	report("clustered", func(s *sparse.Set) {
		for base := uint32(0); base < 100; base++ {
			for v := base * 50000; v < base*50000+200; v++ {
				s.Add(v)
			}
		}
	})
	report("dense", func(s *sparse.Set) {
		for v := uint32(0); v < 100000; v++ {
			s.Add(v)
		}
	})
}
