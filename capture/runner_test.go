package capture

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func timeoutAfter() <-chan time.Time {
	return time.After(10 * time.Second)
}

func TestRunGiantLineNoNewline(t *testing.T) {
	const payload = 200_000
	cmd := helperCommand(t, "giant", "200000")

	cfg := Config{MaxTotalBytes: 4096, MaxLineBytes: 256}
	result, err := Run(context.Background(), cmd, cfg)
	if err != nil {
		t.Fatalf("Run = %v", err)
	}
	if result.ExitErr != nil {
		t.Fatalf("ExitErr = %v, want clean exit", result.ExitErr)
	}

	snap := result.Stdout
	if len(snap.Lines) != 1 {
		t.Fatalf("lines = %d, want 1 (no newline in stream)", len(snap.Lines))
	}
	line := snap.Lines[0]
	if !line.Truncated || len(line.Data) != cfg.MaxLineBytes {
		t.Fatalf("line = len %d truncated=%v, want %d true", len(line.Data), line.Truncated, cfg.MaxLineBytes)
	}
	if snap.ReceivedBytes != payload {
		t.Fatalf("ReceivedBytes = %d, want %d (all bytes drained)", snap.ReceivedBytes, payload)
	}
	if snap.DroppedLineBytes != int64(payload-cfg.MaxLineBytes) {
		t.Fatalf("DroppedLineBytes = %d, want %d",
			snap.DroppedLineBytes, payload-cfg.MaxLineBytes)
	}
	if snap.TotalTruncated {
		t.Fatal("single in-flight line must not be evicted by total cap")
	}
}

func TestRunTailRetentionWithManyLines(t *testing.T) {
	cmd := helperCommand(t, "lines", "1000")
	cfg := Config{MaxTotalBytes: 40, MaxLineBytes: 1024}

	result, err := Run(context.Background(), cmd, cfg)
	if err != nil {
		t.Fatalf("Run = %v", err)
	}
	if result.ExitErr != nil {
		t.Fatalf("ExitErr = %v", result.ExitErr)
	}

	snap := result.Stdout
	if !snap.TotalTruncated {
		t.Fatal("TotalTruncated = false, want true")
	}
	if snap.RetainedBytes > cfg.MaxTotalBytes {
		t.Fatalf("RetainedBytes = %d > cap %d", snap.RetainedBytes, cfg.MaxTotalBytes)
	}
	last := snap.Lines[len(snap.Lines)-1]
	if string(last.Data) != "L000999" {
		t.Fatalf("last line = %q, want L000999 (tail retained)", last.Data)
	}
	if snap.ReceivedBytes != 1000*8 {
		t.Fatalf("ReceivedBytes = %d, want %d", snap.ReceivedBytes, 1000*8)
	}
}

func TestRunDualStreamsConcurrent(t *testing.T) {
	const perStream = 500
	cmd := helperCommand(t, "dual", "500")
	cfg := Config{MaxTotalBytes: 1 << 20, MaxLineBytes: 1024}

	result, err := Run(context.Background(), cmd, cfg)
	if err != nil {
		t.Fatalf("Run = %v", err)
	}
	if result.ExitErr != nil {
		t.Fatalf("ExitErr = %v", result.ExitErr)
	}

	if len(result.Stdout.Lines) != perStream {
		t.Fatalf("stdout lines = %d, want %d", len(result.Stdout.Lines), perStream)
	}
	if len(result.Stderr.Lines) != perStream {
		t.Fatalf("stderr lines = %d, want %d", len(result.Stderr.Lines), perStream)
	}
	for i, line := range result.Stdout.Lines {
		want := "OUT-" + pad6(i)
		if string(line.Data) != want {
			t.Fatalf("stdout[%d] = %q, want %q", i, line.Data, want)
		}
	}
	for i, line := range result.Stderr.Lines {
		want := "ERR-" + pad6(i)
		if string(line.Data) != want {
			t.Fatalf("stderr[%d] = %q, want %q", i, line.Data, want)
		}
	}
}

func pad6(n int) string {
	s := "000000" + itoa(n)
	return s[len(s)-6:]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func TestRunCancelStopsProcessAndPreservesEvidence(t *testing.T) {
	cmd := helperCommand(t, "slow")
	ctx, cancel := context.WithCancel(context.Background())

	cfg := Config{MaxTotalBytes: 1024, MaxLineBytes: 256}
	done := make(chan *Result, 1)
	errs := make(chan error, 1)
	go func() {
		r, err := Run(ctx, cmd, cfg)
		done <- r
		errs <- err
	}()

	// Let evidence accumulate before pulling the rug.
	time.Sleep(150 * time.Millisecond)
	cancel()

	var result *Result
	select {
	case result = <-done:
	case <-timeoutAfter():
		t.Fatal("Run did not return after cancellation")
	}
	if err := <-errs; err != nil {
		t.Fatalf("Run err = %v, want nil with evidence in result", err)
	}
	if !result.Canceled {
		t.Fatal("Canceled = false, want true")
	}
	if result.ExitErr == nil {
		t.Fatal("ExitErr = nil, want a non-nil killed-process error")
	}
	if result.Stderr.ReceivedBytes == 0 || len(result.Stderr.Lines) == 0 {
		t.Fatal("expected stderr evidence collected before cancellation")
	}
	// The trial process must really be gone: a non-nil ProcessState means
	// Wait reaped it. A killed process reports exit code -1 on Unix.
	if cmd.ProcessState == nil {
		t.Fatal("process not reaped: ProcessState is nil")
	}
	// A SIGKILL'd process is signal-terminated rather than exit()-based, so
	// reaping (non-nil ProcessState) is the portable liveness check here.
}

func TestRunAlreadyCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := Run(ctx, helperCommand(t, "slow"), Config{})
	if err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if result != nil {
		t.Fatalf("result = %v, want nil", result)
	}
}

func TestRunStartFailure(t *testing.T) {
	cmd := exec.Command("/nonexistent/binary-for-capture-test")
	result, err := Run(context.Background(), cmd, Config{})
	if err == nil {
		t.Fatalf("err = nil, result = %+v; want start error", result)
	}
}

func TestRunCloseRaceStress(t *testing.T) {
	// Repeated burst-then-exit runs race pipe EOF against process reaping.
	for i := 0; i < 12; i++ {
		cmd := helperCommand(t, "burst")
		result, err := Run(context.Background(), cmd, Config{
			MaxTotalBytes: 16 * 1024,
			MaxLineBytes:  4096,
		})
		if err != nil {
			t.Fatalf("iteration %d: Run = %v", i, err)
		}
		if result.ExitErr != nil {
			t.Fatalf("iteration %d: ExitErr = %v", i, result.ExitErr)
		}
		if result.Stdout.ReceivedBytes != 10*64*1024 {
			t.Fatalf("iteration %d: ReceivedBytes = %d, want %d",
				i, result.Stdout.ReceivedBytes, 10*64*1024)
		}
		if result.Stdout.DroppedLineBytes == 0 {
			t.Fatalf("iteration %d: expected per-line truncation", i)
		}
	}
}

func TestRunConcurrentSnapshotsDuringDrain(t *testing.T) {
	// Snapshot must be safe while drain goroutines are actively writing.
	cap1 := New(Config{MaxTotalBytes: 256, MaxLineBytes: 128})
	cap2 := New(Config{MaxTotalBytes: 256, MaxLineBytes: 128})
	r1, w1, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	var drainers sync.WaitGroup
	drainers.Add(2)
	go func() {
		defer drainers.Done()
		defer cap1.Close()
		io.Copy(cap1, r1)
	}()
	go func() {
		defer drainers.Done()
		defer cap2.Close()
		io.Copy(cap2, r2)
	}()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = cap1.Snapshot()
					_ = cap2.Snapshot()
				}
			}
		}()
	}

	for i := 0; i < 500; i++ {
		fmt.Fprintf(w1, "stream-one-line-%d\n", i)
		fmt.Fprintf(w2, "stream-two-line-%d\n", i)
	}
	w1.Close()
	w2.Close()
	drainers.Wait()
	close(stop)
	wg.Wait()

	if got := len(cap1.Snapshot().Lines); got == 0 {
		t.Fatalf("cap1 retained no lines")
	}
	if got := len(cap2.Snapshot().Lines); got == 0 {
		t.Fatalf("cap2 retained no lines")
	}
}
