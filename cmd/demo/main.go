// Demo of the logpipe acceptance scenario: 10 concurrent streams write
// 2000 records, flush barriers are inserted at record 500 and 1200, and a
// write failure is injected at record 1250. The program then verifies
// that every record before LSN 1200 is durable and that the in-memory
// buffer accounting is conserved.
package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"logpipe"
)

func main() {
	dir, err := os.MkdirTemp("", "logpipe-demo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "wal.log")

	p, err := logpipe.OpenWithOptions(path, logpipe.Options{FailAtLSN: 1250})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("scenario: 10 streams x 200 records, barriers @500/@1200, injected failure @1250")
	fmt.Println()

	// Insert explicit durability barriers after records 500 and 1200.
	barrierErrs := make([]error, 2)
	var bwg sync.WaitGroup
	for i, at := range []uint64{500, 1200} {
		bwg.Add(1)
		go func(i int, at uint64) {
			defer bwg.Done()
			barrierErrs[i] = p.InsertBarrierAt(at)
		}(i, at)
	}

	// 10 producer streams, 200 records each.
	var appended, rejected atomic.Int64
	var wg sync.WaitGroup
	for id := 0; id < 10; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, err := p.Append([]byte(fmt.Sprintf("stream-%02d-record-%04d", id, i)))
				if err != nil {
					rejected.Add(1)
					continue
				}
				appended.Add(1)
			}
		}(id)
	}
	wg.Wait()
	bwg.Wait()
	<-p.Done()

	fmt.Printf("records accepted:            %d\n", appended.Load())
	fmt.Printf("appends rejected post-fail:  %d\n", rejected.Load())
	fmt.Printf("barrier @500:                %v\n", status(barrierErrs[0]))
	fmt.Printf("barrier @1200:               %v\n", status(barrierErrs[1]))
	fmt.Printf("pipeline error:              %v\n", p.Err())
	fmt.Printf("durable LSN:                 %d\n", p.DurableLSN())
	entries, bytes := p.MemStats()
	fmt.Printf("unconfirmed buffers:         entries=%d bytes=%d\n", entries, bytes)

	if err := p.Close(); err != nil {
		log.Fatal(err)
	}

	// Verify the WAL on disk.
	persisted := readLSNs(path)
	fmt.Printf("records persisted in WAL:    %d\n", len(persisted))

	ok := true
	for lsn := uint64(1); lsn <= 1200; lsn++ {
		if int(lsn) > len(persisted) || persisted[lsn-1] != lsn {
			fmt.Printf("FAIL: pre-barrier record LSN %d missing from WAL\n", lsn)
			ok = false
			break
		}
	}
	for _, lsn := range persisted {
		if lsn == 1250 {
			fmt.Println("FAIL: failed record LSN 1250 was persisted")
			ok = false
		}
	}
	if entries != 0 || bytes != 0 {
		fmt.Println("FAIL: unconfirmed buffers not released after rollback")
		ok = false
	}
	if !ok {
		os.Exit(1)
	}
	fmt.Println()
	fmt.Println("verification: all records with LSN <= 1200 durable, LSN 1250 absent, memory conserved")
}

func status(err error) string {
	if err == nil {
		return "durable"
	}
	return err.Error()
}

func readLSNs(path string) []uint64 {
	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	var lsns []uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.SplitN(sc.Text(), " ", 3)
		lsn, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			log.Fatal(err)
		}
		lsns = append(lsns, lsn)
	}
	if err := sc.Err(); err != nil {
		log.Fatal(err)
	}
	return lsns
}
