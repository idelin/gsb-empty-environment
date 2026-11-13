package netsec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestInvokeHookPreservesIdentityAndEnvironment(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "should-not-exist")
	identities := []string{
		"peer$(touch " + marker + ").example",
		"peer`touch " + marker + "`.example",
		`peer'$(touch ` + marker + `).example`,
		"CN=peer$(touch " + marker + ")+O=ACME\\, Inc.",
	}

	for i, identity := range identities {
		t.Run(fmt.Sprintf("identity_%d", i), func(t *testing.T) {
			hookPath, outputPath := recordingHook(t)
			selectors := []string{"10.0.0.0/24", "2001:db8::/32"}

			t.Setenv(EnvPeerIdentity, "stale value that must be replaced")
			err := InvokeEstablished(context.Background(), hookPath, identity, "10.9.8.7", selectors)
			if err != nil {
				t.Fatalf("InvokeEstablished: %v", err)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("identity was interpreted by a shell; marker exists at %s", marker)
			}

			record := readRecording(t, outputPath)
			if got, want := len(record.args), 3+len(selectors); got != want {
				t.Fatalf("argc = %d, want %d", got, want)
			}
			if record.args[0] != string(PhaseEstablished) {
				t.Errorf("phase arg = %q", record.args[0])
			}
			if record.args[1] != identity {
				t.Errorf("identity arg = %q, want %q", record.args[1], identity)
			}
			if record.args[2] != "10.9.8.7" {
				t.Errorf("virtual IP arg = %q", record.args[2])
			}
			for i, selector := range selectors {
				if record.args[3+i] != selector {
					t.Errorf("selector %d = %q, want %q", i, record.args[3+i], selector)
				}
			}

			if got := countExact(record.args, identity); got != 1 {
				t.Errorf("hook received %d identity argv values, want exactly 1", got)
			}
			if record.env[EnvEvent] != string(PhaseEstablished) {
				t.Errorf("%s = %q", EnvEvent, record.env[EnvEvent])
			}
			if record.env[EnvPeerIdentity] != identity {
				t.Errorf("%s = %q, want %q", EnvPeerIdentity, record.env[EnvPeerIdentity], identity)
			}
			if record.env[EnvVirtualIP] != "10.9.8.7" {
				t.Errorf("%s = %q", EnvVirtualIP, record.env[EnvVirtualIP])
			}
			if record.env[EnvTrafficSelectors] != `["10.0.0.0/24","2001:db8::/32"]` {
				t.Errorf("%s = %q", EnvTrafficSelectors, record.env[EnvTrafficSelectors])
			}
		})
	}
}

func TestInvokeHookRejectsControlCharactersBeforeExec(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "should-not-exist")
	tests := []string{
		"peer\n$(touch " + marker + ").example",
		"peer\x00$(touch " + marker + ").example",
		"peer\t.example",
		"peer\x7f.example",
		"peer\u0085.example",
	}

	for i, identity := range tests {
		t.Run(fmt.Sprintf("identity_%d", i), func(t *testing.T) {
			hookPath, outputPath := recordingHook(t)
			err := InvokeEstablished(context.Background(), hookPath, identity, "10.0.0.1", nil)
			if !errors.Is(err, ErrPeerIdentityControlCharacter) {
				t.Fatalf("error = %v, want ErrPeerIdentityControlCharacter", err)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("hook side effect occurred for rejected identity")
			}
			if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("hook was invoked for rejected identity")
			}
		})
	}
}

func TestInvokeHookIdentityLengthBoundary(t *testing.T) {
	identity256 := strings.Repeat("a", MaxPeerIdentityBytes)
	identity257 := identity256 + "a"

	hookPath, outputPath := recordingHook(t)
	if err := InvokeEstablished(context.Background(), hookPath, identity256, "10.0.0.1", nil); err != nil {
		t.Fatalf("256-byte identity rejected: %v", err)
	}
	record := readRecording(t, outputPath)
	if record.args[1] != identity256 {
		t.Errorf("256-byte identity was changed")
	}

	os.Remove(outputPath)
	if err := InvokeEstablished(context.Background(), hookPath, identity257, "10.0.0.1", nil); !errors.Is(err, ErrPeerIdentityTooLong) {
		t.Fatalf("257-byte error = %v, want ErrPeerIdentityTooLong", err)
	}
	if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("hook was invoked for 257-byte identity")
	}
}

func TestEventEnvironmentAndSelectorsJSON(t *testing.T) {
	event := Event{
		Phase:            PhaseTerminated,
		PeerIdentity:     "peer.example",
		VirtualIP:        "192.0.2.1",
		TrafficSelectors: nil,
	}
	env, err := event.Env()
	if err != nil {
		t.Fatal(err)
	}
	envMap := environmentMap(env)
	if envMap[EnvTrafficSelectors] != "[]" {
		t.Errorf("nil selectors encoded as %q, want []", envMap[EnvTrafficSelectors])
	}
	if envMap[EnvEvent] != string(PhaseTerminated) {
		t.Errorf("event env = %q", envMap[EnvEvent])
	}
}

func TestInvokeHookReportsHookFailure(t *testing.T) {
	dir := t.TempDir()
	hookPath := filepath.Join(dir, "failing-hook")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho hook failed >&2\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	err := InvokeEstablished(context.Background(), hookPath, "peer.example", "10.0.0.1", nil)
	if err == nil {
		t.Fatal("expected hook failure")
	}
	var hookErr *HookError
	if !errors.As(err, &hookErr) {
		t.Fatalf("error type = %T, want *HookError", err)
	}
	if hookErr.ExitCode != 7 {
		t.Errorf("exit code = %d, want 7", hookErr.ExitCode)
	}
	if !strings.Contains(hookErr.Output, "hook failed") {
		t.Errorf("hook output = %q", hookErr.Output)
	}
}

type hookRecording struct {
	args []string
	env  map[string]string
}

func recordingHook(t *testing.T) (hookPath, outputPath string) {
	t.Helper()
	dir := t.TempDir()
	hookPath = filepath.Join(dir, "record-hook")
	outputPath = filepath.Join(dir, "hook-output")
	t.Setenv("HOOK_OUTPUT", outputPath)

	const script = `#!/bin/sh
set -eu
{
	printf 'argc=%s\n' "$#"
	index=0
	for argument in "$@"; do
		printf 'arg%d=<%s>\n' "$index" "$argument"
		index=$((index + 1))
	done
	printf 'env_%s=<%s>\n' 'IPSEC_EVENT' "${IPSEC_EVENT-}"
	printf 'env_%s=<%s>\n' 'IPSEC_PEER_IDENTITY' "${IPSEC_PEER_IDENTITY-}"
	printf 'env_%s=<%s>\n' 'IPSEC_VIRTUAL_IP' "${IPSEC_VIRTUAL_IP-}"
	printf 'env_%s=<%s>\n' 'IPSEC_TRAFFIC_SELECTORS' "${IPSEC_TRAFFIC_SELECTORS-}"
} > "$HOOK_OUTPUT"
`
	if err := os.WriteFile(hookPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return hookPath, outputPath
}

func readRecording(t *testing.T, path string) hookRecording {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	record := hookRecording{env: make(map[string]string)}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "argc="):
			count, err := strconv.Atoi(strings.TrimPrefix(line, "argc="))
			if err != nil {
				t.Fatal(err)
			}
			record.args = make([]string, 0, count)
		case strings.HasPrefix(line, "arg"):
			indexEnd := strings.IndexByte(line, '=')
			if indexEnd < 3 {
				t.Fatalf("invalid arg record %q", line)
			}
			index, err := strconv.Atoi(line[3:indexEnd])
			if err != nil {
				t.Fatal(err)
			}
			value := strings.TrimSuffix(strings.TrimPrefix(line[indexEnd+1:], "<"), ">")
			for len(record.args) <= index {
				record.args = append(record.args, "")
			}
			record.args[index] = value
		case strings.HasPrefix(line, "env_"):
			nameEnd := strings.IndexByte(line, '=')
			name := strings.TrimPrefix(line[4:nameEnd], "env_")
			value := strings.TrimSuffix(strings.TrimPrefix(line[nameEnd+1:], "<"), ">")
			record.env[name] = value
		default:
			t.Fatalf("invalid hook record %q", line)
		}
	}
	return record
}

func environmentMap(entries []string) map[string]string {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, _ := strings.Cut(entry, "=")
		out[key] = value
	}
	return out
}

func countExact(values []string, target string) int {
	count := 0
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count
}
