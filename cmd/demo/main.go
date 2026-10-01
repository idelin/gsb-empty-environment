// Command demo runs the bounded receive queue against a locally
// synthesized workload and prints conservation statistics. It can also
// record the telemetry stream to a JSON-lines fixture for replay.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"recvq"
)

func main() {
	var (
		capacity = flag.Int("capacity", 64, "bounded queue capacity")
		workers  = flag.Int("workers", 4, "processing workers")
		records  = flag.Int("records", 1000, "synthesized records")
		tenants  = flag.Int("tenants", 4, "synthesized tenants")
		seed     = flag.Int64("seed", 42, "workload seed")
		policy   = flag.String("policy", "incoming", "drop policy: incoming|oldest")
		mode     = flag.String("mode", "drain", "shutdown mode: drain|stopnow")
		workUS   = flag.Int("work-us", 200, "simulated processing time per record (microseconds)")
		arrival  = flag.Int("arrival-us", 0, "pace between offers (microseconds); 0 = burst")
		fixture  = flag.String("fixture", "", "write telemetry events to this JSON-lines fixture")
	)
	flag.Parse()

	var dp recvq.DropPolicy
	switch *policy {
	case "incoming":
		dp = recvq.DropIncoming
	case "oldest":
		dp = recvq.DropOldest
	default:
		fmt.Fprintln(os.Stderr, "unknown -policy:", *policy)
		os.Exit(2)
	}

	sink, log := recvq.EventSink()
	r := recvq.NewReceiver(*capacity, dp, sink)

	tenantNames := make([]string, *tenants)
	for i := range tenantNames {
		tenantNames[i] = fmt.Sprintf("tenant-%c", 'a'+i)
	}
	workload := recvq.Synthesize(*seed, *records, tenantNames)

	done := make(chan struct{})
	go func() {
		r.Run(*workers, func(recvq.Record) {
			time.Sleep(time.Duration(*workUS) * time.Microsecond)
		})
		close(done)
	}()

	accepted := 0
	for _, rec := range workload {
		if r.Enqueue(rec) {
			accepted++
		}
		if *arrival > 0 {
			time.Sleep(time.Duration(*arrival) * time.Microsecond)
		}
	}

	switch *mode {
	case "drain":
		r.Shutdown(recvq.Drain)
	case "stopnow":
		r.Shutdown(recvq.StopNow)
	default:
		fmt.Fprintln(os.Stderr, "unknown -mode:", *mode)
		os.Exit(2)
	}
	<-done

	stats := r.Stats()
	fmt.Println("== receive queue demo ==")
	fmt.Printf("capacity=%d workers=%d policy=%s mode=%s\n", *capacity, *workers, *policy, *mode)
	fmt.Printf("offered=%d accepted=%d rejected=%d\n", len(workload), accepted, len(workload)-accepted)
	fmt.Println("stats:", stats)
	fmt.Printf("in_queue=%d conserved=%v (arrived == processed + dropped + in_queue)\n",
		r.InQueue(), stats.Conserved(r.InQueue()))

	// Per-tenant processed counts show the noisy high-priority tenant does
	// not starve the normal streams.
	processed := map[string]int{}
	processedPrio := map[string]int{}
	for _, ev := range *log {
		if ev.Type == recvq.EventProcess {
			processed[ev.Tenant]++
			processedPrio[ev.Priority]++
		}
	}
	fmt.Println("processed_by_tenant:", processed)
	fmt.Println("processed_by_priority:", processedPrio)

	if *fixture != "" {
		f, err := os.Create(*fixture)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := recvq.WriteEvents(f, *log); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		f.Close()
		fmt.Printf("wrote %d telemetry events to %s\n", len(*log), *fixture)
	}
}
