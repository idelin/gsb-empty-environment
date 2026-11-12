package capture

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"
)

// helperCommand builds a Cmd that re-executes the test binary in helper mode.
func helperCommand(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=TestHelperProcess", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	return cmd
}

// TestHelperProcess is not a real test: it is the entry point of the
// re-executed test binary used as a controllable output process.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	args := os.Args
	// Skip past the test-binary flags up to the "--" separator.
	var rest []string
	for i, a := range args {
		if a == "--" {
			rest = args[i+1:]
			break
		}
	}
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "helper: missing mode")
		os.Exit(2)
	}

	mode := rest[0]
	switch mode {
	case "giant":
		// One giant line without any newline, written in small chunks so the
		// capture's Write sees many fragmented calls.
		n := atoi(rest, 1)
		chunk := make([]byte, 4096)
		written := 0
		for written < n {
			take := n - written
			if take > len(chunk) {
				take = len(chunk)
			}
			for i := range chunk[:take] {
				chunk[i] = byte('A' + (written+i)%26)
			}
			if _, err := os.Stdout.Write(chunk[:take]); err != nil {
				os.Exit(1)
			}
			written += take
		}

	case "lines":
		// Numbered newline-terminated lines for tail-retention checks.
		n := atoi(rest, 1)
		for i := 0; i < n; i++ {
			fmt.Fprintf(os.Stdout, "L%06d\n", i)
		}

	case "dual":
		// Two streams written concurrently from separate goroutines.
		n := atoi(rest, 1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				fmt.Fprintf(os.Stdout, "OUT-%06d\n", i)
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				fmt.Fprintf(os.Stderr, "ERR-%06d\n", i)
			}
		}()
		wg.Wait()

	case "slow":
		// Emits forever until killed; used for cancellation tests.
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := os.Stderr.WriteString("slow-line\n"); err != nil {
				os.Exit(0)
			}
		}

	case "burst":
		// Large bursts followed by immediate exit, stressing the EOF race.
		buf := make([]byte, 64*1024)
		for i := range buf {
			buf[i] = byte('0' + i%10)
		}
		for b := 0; b < 10; b++ {
			if _, err := os.Stdout.Write(buf); err != nil {
				os.Exit(1)
			}
		}

	default:
		fmt.Fprintf(os.Stderr, "helper: unknown mode %q\n", mode)
		os.Exit(2)
	}
	os.Exit(0)
}

func atoi(args []string, idx int) int {
	if idx >= len(args) {
		fmt.Fprintf(os.Stderr, "helper: missing integer argument\n")
		os.Exit(2)
	}
	n, err := strconv.Atoi(args[idx])
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper: bad integer %q: %v\n", args[idx], err)
		os.Exit(2)
	}
	return n
}
