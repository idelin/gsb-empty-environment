// Demo of the acceptance scenario: a producer parks on the 3rd send into a
// capacity-2 channel, a consumer's read wakes it precisely, and Close
// broadcasts EOF to all waiting readers.
package main

import (
	"fmt"
	"sync/atomic"
	"time"

	"corochan"
)

var start = time.Now()

func logf(format string, args ...any) {
	fmt.Printf("[%6.1fms] %s\n", float64(time.Since(start).Microseconds())/1000, fmt.Sprintf(format, args...))
}

func main() {
	ch := corochan.NewChannel[int](2)

	// Producer P: writes 3 values; the 3rd send must park.
	var pParked atomic.Bool
	go func() {
		for i := 1; i <= 3; i++ {
			if i == 3 {
				pParked.Store(true) // about to block
			}
			ch.Send(i)
			logf("P: Send(%d) returned", i)
		}
	}()

	time.Sleep(100 * time.Millisecond)
	logf("main: buffer holds %d values; P parked on 3rd send = %v", ch.Len(), pParked.Load())

	// Consumer C: one read frees a slot and wakes P immediately.
	v, _ := ch.Recv()
	logf("C: Recv() = %d (frees a slot, waking P)", v)
	time.Sleep(50 * time.Millisecond)

	// Two more readers park on the drained channel; Close broadcasts EOF.
	done := make(chan struct{})
	for _, name := range []string{"R1", "R2"} {
		go func(name string) {
			for {
				v, ok := ch.Recv()
				if !ok {
					logf("%s: Recv() => EOF (channel closed)", name)
					return
				}
				logf("%s: Recv() = %d", name, v)
			}
		}(name)
	}
	time.Sleep(50 * time.Millisecond)
	logf("main: closing channel")
	ch.Close()
	time.Sleep(50 * time.Millisecond)
	close(done)
	logf("main: done")
}
