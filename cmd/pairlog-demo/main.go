// Command pairlog-demo demonstrates the pairlog package: appending records,
// per-consumer reads and acknowledgements, a slow consumer holding back
// reclamation, and the watermark advancing once it unregisters.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"pairlog"
)

func main() {
	dir := filepath.Join(os.TempDir(), "pairlog-demo")
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) > 0 {
		log.Fatalf("demo log directory %s is not empty; remove it first", dir)
	}

	l, err := pairlog.Open(dir, pairlog.Options{MaxSegmentBytes: 64})
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	fmt.Printf("log directory: %s\n", dir)

	for _, name := range []string{"alice", "bob"} {
		if err := l.RegisterConsumer(name); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Println("registered consumers: alice, bob")

	for i := 0; i < 4; i++ {
		payload := fmt.Sprintf("msg-%02d-%013d", i, 0) // 20 bytes -> 36 byte frame
		off, err := l.Append([]byte(payload))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("appended %-22q at offset %d\n", payload, off)
	}
	fmt.Println("segments:", l.Segments())

	// alice is fast: reads and acknowledges everything.
	for {
		rec, err := l.Read("alice")
		if err != nil {
			break
		}
		fmt.Printf("alice read  %-22q next=%d\n", string(rec.Payload), rec.Next)
	}
	if err := l.Ack(map[string]uint64{"alice": l.End()}); err != nil {
		log.Fatal(err)
	}

	// bob is slow: reads and acknowledges only the first record.
	rec, err := l.Read("bob")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("bob read    %-22q next=%d\n", string(rec.Payload), rec.Next)
	if err := l.Ack(map[string]uint64{"bob": rec.Next}); err != nil {
		log.Fatal(err)
	}

	res, err := l.Reclaim()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("reclaim with slow bob: watermark=%d removed=%v\n", res.Watermark, res.RemovedBases)

	// bob goes away; the watermark can advance.
	if err := l.UnregisterConsumer("bob"); err != nil {
		log.Fatal(err)
	}
	res, err = l.Reclaim()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("reclaim after bob left: watermark=%d removed=%v\n", res.Watermark, res.RemovedBases)
	fmt.Println("segments:", l.Segments())
}
