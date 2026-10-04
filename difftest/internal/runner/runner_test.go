package runner

import (
	"context"
	"testing"
	"time"
)

func run(t *testing.T, e *CommandExecutor, input string) Outcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return e.Run(ctx, []byte(input))
}

func TestCommandValue(t *testing.T) {
	out := run(t, &CommandExecutor{Argv: []string{"cat"}, Timeout: time.Second}, "hello")
	if out.Kind != KindValue || out.Stdout != "hello" {
		t.Fatalf("got %+v", out)
	}
}

func TestCommandError(t *testing.T) {
	out := run(t, &CommandExecutor{Argv: []string{"sh", "-c", "exit 3"}, Timeout: time.Second}, "")
	if out.Kind != KindError || out.ExitCode != 3 {
		t.Fatalf("got %+v", out)
	}
}

func TestCommandSpawnFailure(t *testing.T) {
	out := run(t, &CommandExecutor{Argv: []string{"/nonexistent/binary"}, Timeout: time.Second}, "")
	if out.Kind != KindError || out.Err == "" {
		t.Fatalf("got %+v", out)
	}
}

func TestCommandTimeoutNotError(t *testing.T) {
	out := run(t, &CommandExecutor{Argv: []string{"sleep", "2"}, Timeout: 50 * time.Millisecond}, "")
	if out.Kind != KindTimeout {
		t.Fatalf("killed-by-deadline must be timeout, got %+v", out)
	}
}
