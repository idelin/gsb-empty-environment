// Command demo runs the ordered log pipeline twice: once fault-free
// and once with an injected persistence failure, printing durability
// and buffer-accounting statistics for each run.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"logpipe"
)

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		n++
	}
	return n
}

// produce starts n producers that concurrently append total records.
func produce(p *logpipe.Pipeline, producers, total int) []logpipe.Handle {
	handles := make([]logpipe.Handle, total)
	var ticket, hcount int64
	var wg sync.WaitGroup
	for w := 0; w < producers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				i := atomic.AddInt64(&ticket, 1)
				if i > int64(total) {
					return
				}
				h, err := p.Append([]byte(fmt.Sprintf("producer-%d-msg-%d", id, i)))
				if err != nil {
					return
				}
				handles[atomic.AddInt64(&hcount, 1)-1] = h
			}
		}(w)
	}
	wg.Wait()
	return handles[:atomic.LoadInt64(&hcount)]
}

func subscribe(p *logpipe.Pipeline) (published *int64, done *sync.WaitGroup) {
	var count int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range p.Subscribe() {
			atomic.AddInt64(&count, 1)
		}
	}()
	return &count, &wg
}

func report(title string, p *logpipe.Pipeline, handles []logpipe.Handle, published *int64, path string) {
	var acked, rolledBack int
	for _, h := range handles {
		if err := h.Wait(); err != nil {
			rolledBack++
		} else {
			acked++
		}
	}
	fmt.Printf("  %-22s %d\n", "appended:", len(handles))
	fmt.Printf("  %-22s %d\n", "acked durable:", acked)
	fmt.Printf("  %-22s %d\n", "rolled back:", rolledBack)
	fmt.Printf("  %-22s %d\n", "published:", atomic.LoadInt64(published))
	fmt.Printf("  %-22s %d\n", "durable LSN:", p.DurableLSN())
	fmt.Printf("  %-22s %d\n", "records in WAL:", countLines(path))
	fmt.Printf("  %-22s %d allocated / %d released / %d outstanding\n",
		"buffers:", p.Allocated(), p.Released(), p.Outstanding())
	_ = title
}

func main() {
	dir, err := os.MkdirTemp("", "logpipe-demo")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("WAL directory: %s\n\n", dir)

	// --- Run 1: fault-free, 4 producers x 50 records, barriers at 60 and 140.
	fmt.Println("== run 1: 4 producers x 50 records, barriers @60/@140, no fault ==")
	path1 := filepath.Join(dir, "normal.log")
	f1, err := os.Create(path1)
	if err != nil {
		log.Fatal(err)
	}
	p1 := logpipe.New(f1, logpipe.Options{BatchSize: 16})
	b1, _ := p1.InsertBarrierAt(60)
	b2, _ := p1.InsertBarrierAt(140)
	pub1, sub1 := subscribe(p1)
	h1 := produce(p1, 4, 200)
	if err := b1.Wait(); err != nil {
		log.Fatalf("barrier@60: %v", err)
	}
	if err := b2.Wait(); err != nil {
		log.Fatalf("barrier@140: %v", err)
	}
	if err := p1.Close(); err != nil {
		log.Fatalf("close: %v", err)
	}
	sub1.Wait()
	report("run 1", p1, h1, pub1, path1)
	f1.Close()

	// --- Run 2: fault injected at record 130, barrier at 120.
	fmt.Println("\n== run 2: 4 producers x 50 records, barrier @120, fault injected @130 ==")
	path2 := filepath.Join(dir, "fault.log")
	f2, err := os.Create(path2)
	if err != nil {
		log.Fatal(err)
	}
	injected := errors.New("simulated disk failure")
	p2 := logpipe.New(f2, logpipe.Options{BatchSize: 16, FaultLSN: 130, FaultErr: injected})
	barrier, _ := p2.InsertBarrierAt(120)
	pub2, sub2 := subscribe(p2)
	h2 := produce(p2, 4, 200)
	if err := barrier.Wait(); err != nil {
		log.Fatalf("barrier@120: %v", err)
	}
	fmt.Printf("  barrier@120 durable, fault: %v\n", p2.Close())
	sub2.Wait()
	report("run 2", p2, h2, pub2, path2)
	f2.Close()

	if p1.Outstanding() != 0 || p2.Outstanding() != 0 {
		log.Fatal("buffer accounting not conserved")
	}
	fmt.Println("\nbuffer accounting conserved in both runs (outstanding = 0)")
}
