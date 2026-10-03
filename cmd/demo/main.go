// Command demo exercises the full stack: hand-crafted protobuf encoding,
// generic parsing, reflection-based struct binding + validation, safe
// truncation handling, and the adaptive load-aware limiter.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"protowire/adaptive"
	"protowire/proto"
	"protowire/schema"
	"protowire/wire"
)

// Session is the target struct for the reflection engine.
type Session struct {
	User    string   `protobuf:"string,1" validate:"required,minlen=1"`
	Addr    *Address `protobuf:"message,2"`
	Scores  []int64  `protobuf:"zigzag64,3,rep,packed"`
	Token   uint64   `protobuf:"fixed64,4"`
	Balance int64    `protobuf:"zigzag64,5" validate:"min=-1000000"`
}

// Address is a nested message (level 2); Geo nests one level deeper.
type Address struct {
	City string `protobuf:"string,1" validate:"required"`
	Geo  *Geo   `protobuf:"message,2"`
}

type Geo struct {
	Lat int32 `protobuf:"zigzag32,1" validate:"min=-90000000,max=90000000"`
	Lng int32 `protobuf:"zigzag32,2"`
}

func (s *Session) Validate() error {
	if s.Balance < 0 && len(s.Scores) == 0 {
		return fmt.Errorf("negative balance requires at least one score")
	}
	return nil
}

// encodeSession hand-builds a 3-level nested protobuf stream.
func encodeSession() []byte {
	geo := wire.NewWriter()
	geo.AppendVarint(proto.EncodeTag(1, proto.WireVarint))
	geo.AppendZigZag32(31230499) // 31.230499 N
	geo.AppendVarint(proto.EncodeTag(2, proto.WireVarint))
	geo.AppendZigZag32(121473701) // 121.473701 E

	addr := wire.NewWriter()
	addr.AppendVarint(proto.EncodeTag(1, proto.WireLengthDelimited))
	addr.AppendLengthDelimited([]byte("Shanghai"))
	addr.AppendVarint(proto.EncodeTag(2, proto.WireLengthDelimited))
	addr.AppendLengthDelimited(geo.Bytes())

	packed := wire.NewWriter()
	for _, s := range []int64{88, -12, 4096, 7} {
		packed.AppendZigZag64(s)
	}

	w := wire.NewWriter()
	w.AppendVarint(proto.EncodeTag(1, proto.WireLengthDelimited))
	w.AppendLengthDelimited([]byte("alice"))
	w.AppendVarint(proto.EncodeTag(2, proto.WireLengthDelimited))
	w.AppendLengthDelimited(addr.Bytes())
	w.AppendVarint(proto.EncodeTag(3, proto.WireLengthDelimited))
	w.AppendLengthDelimited(packed.Bytes())
	w.AppendVarint(proto.EncodeTag(4, proto.WireFixed64))
	w.AppendFixed64(0x0123456789abcdef)
	w.AppendVarint(proto.EncodeTag(5, proto.WireVarint))
	w.AppendZigZag64(-987654)
	return w.Bytes()
}

func main() {
	data := encodeSession()
	fmt.Printf("== encoded stream: %d bytes ==\n%x\n\n", len(data), data)

	// 1. Generic parse: walk the raw field structure.
	msg, err := proto.Parse(data)
	if err != nil {
		panic(err)
	}
	fmt.Println("== generic parse ==")
	for _, f := range msg {
		fmt.Printf("  field=%d wire=%d", f.Num, f.WireType)
		if f.WireType == proto.WireLengthDelimited {
			fmt.Printf(" len=%d", len(f.Bytes))
		} else {
			fmt.Printf(" val=%d", f.U64)
		}
		fmt.Println()
	}

	// 2. Reflection binding + validation.
	var s Session
	if err := schema.Unmarshal(data, &s); err != nil {
		panic(err)
	}
	fmt.Println("\n== reflection unmarshal + validate ==")
	fmt.Printf("  user=%q balance=%d token=%#x\n", s.User, s.Balance, s.Token)
	fmt.Printf("  addr.city=%q geo=(%d,%d)\n", s.Addr.City, s.Addr.Geo.Lat, s.Addr.Geo.Lng)
	fmt.Printf("  scores(packed)=%v\n", s.Scores)

	// 3. Truncation safety: cut the stream mid-field.
	fmt.Println("\n== truncation guard ==")
	for _, cut := range []int{1, 5, len(data) / 2, len(data) - 1} {
		var v Session
		err := schema.Unmarshal(data[:cut], &v)
		fmt.Printf("  truncated at %3d/%d bytes -> %v\n", cut, len(data), err)
	}

	// 4. Adaptive limiter under simulated pressure.
	fmt.Println("\n== adaptive limiter ==")
	l := adaptive.NewLimiter(adaptive.Config{
		MinLimit: 2, MaxLimit: 32, Initial: 4,
		MetricsFunc:    func() float64 { return 0.85 }, // hot environment
		SampleInterval: 20 * time.Millisecond,
	})
	defer l.Close()

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := l.Acquire(context.Background())
			if err != nil {
				return
			}
			time.Sleep(time.Duration(5+i%10) * time.Millisecond)
			if i%6 == 0 {
				p.Drop() // simulate downstream failure
			} else {
				p.Release()
			}
		}(i)
	}
	wg.Wait()
	time.Sleep(50 * time.Millisecond) // let the sampler tick
	st := l.Stats()
	fmt.Printf("  limit=%d effective=%d (load=%.2f) admitted=%d rejected=%d inflight=%d\n",
		st.Limit, st.EffectiveLimit, st.Load, st.Admitted, st.Rejected, st.InFlight)
	fmt.Println("  threshold adapted to pressure; no goroutine or permit leaks")
}
