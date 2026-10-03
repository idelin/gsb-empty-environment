// Demo: hand-encode a nested protobuf stream, decode it via reflection,
// validate it, then run a multi-node contention race on the arbiter.
package main

import (
	"fmt"
	"sync"

	"pbwire/arbiter"
	"pbwire/wire"
)

type Addr struct {
	City string `protobuf:"bytes,1" validate:"required"`
	Zip  uint32 `protobuf:"varint,2"`
}

type User struct {
	ID      uint64  `protobuf:"varint,1" validate:"min=1"`
	Name    string  `protobuf:"bytes,2" validate:"required,minlen=1,maxlen=32"`
	Balance int64   `protobuf:"zigzag,3"`
	Addr    *Addr   `protobuf:"bytes,4"`
	Lucky   []int32 `protobuf:"zigzag,5,packed"`
	Score   float32 `protobuf:"fixed32,6"`
}

func encodeUser() []byte {
	addr := wire.NewBuffer()
	addr.AppendTag(1, wire.LengthDelim)
	addr.AppendBytes([]byte("Shanghai"))
	addr.AppendTag(2, wire.Varint)
	addr.AppendVarint(200000)

	lucky := wire.NewBuffer()
	for _, v := range []int64{7, -13, 42} {
		lucky.AppendZigZag(v)
	}

	b := wire.NewBuffer()
	b.AppendTag(1, wire.Varint)
	b.AppendVarint(1001)
	b.AppendTag(2, wire.LengthDelim)
	b.AppendBytes([]byte("alice"))
	b.AppendTag(3, wire.Varint)
	b.AppendZigZag(-98765)
	b.AppendTag(4, wire.LengthDelim)
	b.AppendBytes(addr.Bytes())
	b.AppendTag(5, wire.LengthDelim)
	b.AppendBytes(lucky.Bytes())
	b.AppendTag(6, wire.Fixed32)
	b.AppendFloat32(98.6)
	return b.Bytes()
}

func main() {
	fmt.Println("=== protobuf wire decode ===")
	data := encodeUser()
	fmt.Printf("encoded %d bytes: %x\n", len(data), data)

	var u User
	if err := wire.Unmarshal(data, &u); err != nil {
		panic(err)
	}
	fmt.Printf("decoded: ID=%d Name=%q Balance=%d Score=%.1f\n", u.ID, u.Name, u.Balance, u.Score)
	fmt.Printf("nested:  City=%q Zip=%d Lucky=%v\n", u.Addr.City, u.Addr.Zip, u.Lucky)
	if err := wire.Validate(&u); err != nil {
		panic(err)
	}
	fmt.Println("validation: OK")

	fmt.Println("\n=== truncation guard ===")
	cut := len(data) - 3
	if err := wire.Unmarshal(data[:cut], &u); err != nil {
		fmt.Printf("cut at %d/%d -> %v\n", cut, len(data), err)
	}

	fmt.Println("\n=== multi-node arbitration ===")
	a := arbiter.New()
	const nodes = 8
	const rounds = 2000
	var wg sync.WaitGroup
	for id := 0; id < nodes; id++ {
		wg.Add(1)
		go func(nodeID int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				ver, _, _ := a.Read("leader")
				a.Propose("leader", nodeID, ver, nodeID)
			}
		}(id)
	}
	wg.Wait()
	ver, winner, _ := a.Read("leader")
	acc, rej := a.Stats()
	fmt.Printf("final version=%d last-writer=node-%v accepted=%d rejected=%d\n", ver, winner, acc, rej)
	fmt.Printf("linearizability: accepted(%d) == final version(%d) -> %v\n", acc, ver, acc == ver)
}
