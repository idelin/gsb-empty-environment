// Demo: hand-rolled protobuf wire codec + multi-tenant DRR fair scheduler.
package main

import (
	"fmt"
	"log"

	"example.com/protodrr/scheduler"
	"example.com/protodrr/wire"
)

type Header struct {
	ID      uint64 `protobuf:"varint,1" validate:"nonzero,min=1"`
	Version int32  `protobuf:"zigzag,2"`
}

type Envelope struct {
	Header  Header  `protobuf:"bytes,1"`
	Samples []int32 `protobuf:"zigzag,2,packed"`
	Note    string  `protobuf:"bytes,3"`
}

func codecDemo() {
	fmt.Println("== protobuf wire codec ==")
	in := &Envelope{
		Header:  Header{ID: 42, Version: -3},
		Samples: []int32{-150, 0, 1, 300},
		Note:    "hello, wire",
	}
	data, err := wire.Marshal(in)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("encoded %d bytes: %x\n", len(data), data)

	var out Envelope
	if err := wire.Unmarshal(data, &out); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("decoded: %+v\n", out)
	if err := wire.Validate(&out); err != nil {
		log.Fatal(err)
	}
	fmt.Println("validate: ok")

	truncated := data[:len(data)-2]
	var bad Envelope
	fmt.Printf("truncated decode: %v\n", wire.Unmarshal(truncated, &bad))
}

func schedulerDemo() {
	fmt.Println("\n== multi-tenant DRR scheduler ==")
	s := scheduler.New(10)
	for _, cfg := range []struct {
		id     string
		weight uint64
	}{
		{"tenant-a", 1},
		{"tenant-b", 2},
		{"tenant-c", 3},
	} {
		if err := s.AddTenant(cfg.id, cfg.weight, 100, 4); err != nil {
			log.Fatal(err)
		}
	}
	// Backlog every tenant with equal demand; weights decide service share.
	for i := 0; i < 60; i++ {
		for _, id := range []string{"tenant-a", "tenant-b", "tenant-c"} {
			job := &scheduler.Job{ID: fmt.Sprintf("%s#%d", id, i), Cost: 10, Payload: id}
			if err := s.Submit(id, job); err != nil {
				log.Fatal(err)
			}
		}
	}
	served := map[string]uint64{}
	fmt.Print("dispatch order (first 18, weights 1:2:3): ")
	for i := 0; ; i++ {
		job, err := s.Next()
		if err != nil {
			log.Fatal(err)
		}
		if job == nil {
			break
		}
		id := job.Payload.(string)
		if i < 18 {
			fmt.Printf("%s ", id[len("tenant-"):])
		}
		served[id] += job.Cost
		if err := s.Complete(id, job); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Println("\nfinal totals after draining equal demand (per-tenant accounting):")
	for _, id := range []string{"tenant-a", "tenant-b", "tenant-c"} {
		st, _ := s.Stats(id)
		fmt.Printf("  %s: served=%d queued=%d inflight=%d\n", id, st.Served, st.Queued, st.Inflight)
	}
	if err := s.Close(); err != nil {
		log.Fatalf("leak detected: %v", err)
	}
	fmt.Println("close: no leaks")
}

func main() {
	codecDemo()
	schedulerDemo()
}
