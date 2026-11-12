package netsec

import (
	"strings"
	"sync"
	"testing"
)

func cmd(parts ...string) [][]byte {
	args := make([][]byte, len(parts))
	for i, part := range parts {
		args[i] = []byte(part)
	}
	return args
}

func newTestGate() *Gate {
	return NewGate(RestrictedConfig())
}

func TestSelectEnforced(t *testing.T) {
	gate := newTestGate()
	conn := gate.Open()

	if err := gate.Check(conn, cmd("SELECT", "0")); err != nil {
		t.Fatalf("SELECT 0 should be allowed, got %v", err)
	}
	if gate.DB(conn) != 0 {
		t.Fatalf("expected DB 0, got %d", gate.DB(conn))
	}

	err := gate.Check(conn, cmd("SELECT", "1"))
	if err == nil || err.Kind != KindDeniedDB {
		t.Fatalf("SELECT 1 must be denied, got %v", err)
	}
	if gate.DB(conn) != 0 {
		t.Fatalf("denied SELECT must not change DB, got %d", gate.DB(conn))
	}

	err = gate.Check(conn, cmd("SELECT", "nope"))
	if err == nil || err.Kind != KindInvalidArg {
		t.Fatalf("invalid SELECT argument must be an argument error, got %v", err)
	}

	err = gate.Check(conn, cmd("SELECT"))
	if err == nil || err.Kind != KindInvalidArg {
		t.Fatalf("arity error expected, got %v", err)
	}
}

func TestMoveSwapCopy(t *testing.T) {
	gate := newTestGate()
	conn := gate.Open()

	if err := gate.Check(conn, cmd("MOVE", "key", "0")); err != nil {
		t.Fatalf("MOVE to DB 0 should be allowed, got %v", err)
	}
	err := gate.Check(conn, cmd("MOVE", "key", "2"))
	if err == nil || err.Kind != KindDeniedDB {
		t.Fatalf("MOVE to DB 2 must be denied, got %v", err)
	}

	if err := gate.Check(conn, cmd("SWAPDB", "0", "0")); err != nil {
		t.Fatalf("SWAPDB 0 0 should be allowed, got %v", err)
	}
	err = gate.Check(conn, cmd("SWAPDB", "0", "1"))
	if err == nil || err.Kind != KindDeniedDB {
		t.Fatalf("SWAPDB touching DB 1 must be denied, got %v", err)
	}
	err = gate.Check(conn, cmd("SWAPDB", "3", "0"))
	if err == nil || err.Kind != KindDeniedDB {
		t.Fatalf("SWAPDB touching DB 3 must be denied, got %v", err)
	}

	if err := gate.Check(conn, cmd("COPY", "src", "dst")); err != nil {
		t.Fatalf("plain COPY should be allowed, got %v", err)
	}
	if err := gate.Check(conn, cmd("COPY", "src", "dst", "REPLACE")); err != nil {
		t.Fatalf("COPY REPLACE should be allowed, got %v", err)
	}
	if err := gate.Check(conn, cmd("COPY", "src", "dst", "DB", "0", "REPLACE")); err != nil {
		t.Fatalf("COPY into DB 0 should be allowed, got %v", err)
	}
	err = gate.Check(conn, cmd("COPY", "src", "dst", "DB", "1"))
	if err == nil || err.Kind != KindDeniedDB {
		t.Fatalf("COPY into DB 1 must be denied, got %v", err)
	}
	err = gate.Check(conn, cmd("COPY", "src", "dst", "db", "7", "replace"))
	if err == nil || err.Kind != KindDeniedDB {
		t.Fatalf("lowercase COPY option targeting DB 7 must be denied, got %v", err)
	}

	err = gate.Check(conn, cmd("COPY", "src", "dst", "DB"))
	if err == nil || err.Kind != KindInvalidArg {
		t.Fatalf("COPY with missing DB argument must be an argument error, got %v", err)
	}
	err = gate.Check(conn, cmd("COPY", "src", "dst", "BOGUS"))
	if err == nil || err.Kind != KindInvalidArg {
		t.Fatalf("COPY with unknown option must be an argument error, got %v", err)
	}
	err = gate.Check(conn, cmd("MOVE", "key", "-1"))
	if err == nil || err.Kind != KindInvalidArg {
		t.Fatalf("negative DB index must be an argument error, got %v", err)
	}
}

func TestScriptingDenied(t *testing.T) {
	gate := newTestGate()
	conn := gate.Open()

	for _, c := range [][]string{
		{"EVAL", "return 1", "0"},
		{"EVALSHA", "abc", "0"},
		{"EVAL_RO", "return 1", "0"},
		{"EVALSHA_RO", "abc", "0"},
		{"FUNCTION", "LIST"},
		{"FUNCTION", "DUMP"},
		{"FCALL", "fn", "0"},
		{"FCALL_RO", "fn", "0"},
	} {
		err := gate.Check(conn, cmd(c...))
		if err == nil || err.Kind != KindDeniedCommand {
			t.Fatalf("%s must be denied for restricted users, got %v", c[0], err)
		}
	}

	openGate := NewGate(Config{AllowedDBs: []int{0}, BlockScripting: false})
	openConn := openGate.Open()
	if err := openGate.Check(openConn, cmd("EVAL", "return 1", "0")); err != nil {
		t.Fatalf("EVAL should be allowed when scripting is enabled, got %v", err)
	}
	if err := openGate.Check(openConn, cmd("FUNCTION", "LIST")); err != nil {
		t.Fatalf("FUNCTION should be allowed when scripting is enabled, got %v", err)
	}
}

func TestCommandNameCaseInsensitive(t *testing.T) {
	gate := newTestGate()
	conn := gate.Open()

	cases := [][]string{
		{"select", "1"},
		{"SeLeCt", "1"},
		{"move", "k", "1"},
		{"SwapDB", "0", "1"},
		{"copy", "a", "b", "DB", "1"},
		{"eval", "return 1", "0"},
		{"function", "list"},
	}
	for _, c := range cases {
		if err := gate.Check(conn, cmd(c...)); err == nil {
			t.Fatalf("mixed-case %s must still be gated", strings.ToUpper(c[0]))
		}
	}
	if gate.DB(conn) != 0 {
		t.Fatalf("case-varied denials must leave DB at 0, got %d", gate.DB(conn))
	}

	if err := gate.Check(conn, cmd("select", "0")); err != nil {
		t.Fatalf("lowercase allowed SELECT must pass, got %v", err)
	}
}

func TestPipelineSelectApplies(t *testing.T) {
	gate := newTestGate()
	conn := gate.Open()

	pipeline := [][]string{
		{"SET", "a", "1"},
		{"SELECT", "1"},
		{"GET", "a"},
		{"SELECT", "0"},
		{"GET", "a"},
	}
	var deniedAt []int
	for i, command := range pipeline {
		if err := gate.Check(conn, cmd(command...)); err != nil {
			deniedAt = append(deniedAt, i)
		}
	}
	if len(deniedAt) != 1 || deniedAt[0] != 1 {
		t.Fatalf("only the SELECT 1 command must be denied, got denials at %v", deniedAt)
	}
	if gate.DB(conn) != 0 {
		t.Fatalf("connection must remain bound to DB 0, got %d", gate.DB(conn))
	}

	// An allowed SELECT inside a pipeline still updates the tracked state.
	allowed := [][]string{{"SELECT", "0"}, {"GET", "k"}}
	for _, command := range allowed {
		if err := gate.Check(conn, cmd(command...)); err != nil {
			t.Fatalf("pipelined allowed command failed: %v", err)
		}
	}
	if gate.DB(conn) != 0 {
		t.Fatalf("expected DB 0 after pipelined SELECT 0, got %d", gate.DB(conn))
	}
}

func TestMultiExecSelect(t *testing.T) {
	gate := newTestGate()
	conn := gate.Open()

	if err := gate.Check(conn, cmd("MULTI")); err != nil {
		t.Fatalf("MULTI failed: %v", err)
	}
	if !gate.InMulti(conn) {
		t.Fatal("connection should be inside MULTI")
	}

	if err := gate.Check(conn, cmd("SET", "a", "1")); err != nil {
		t.Fatalf("queued SET failed: %v", err)
	}
	err := gate.Check(conn, cmd("SELECT", "1"))
	if err == nil || err.Kind != KindDeniedDB {
		t.Fatalf("SELECT inside MULTI must be gated, got %v", err)
	}
	if !gate.InMulti(conn) {
		t.Fatal("denied SELECT must not abort the transaction")
	}
	if gate.DB(conn) != 0 {
		t.Fatalf("denied SELECT inside MULTI must not change DB, got %d", gate.DB(conn))
	}

	if err := gate.Check(conn, cmd("SELECT", "0")); err != nil {
		t.Fatalf("SELECT 0 inside MULTI should pass, got %v", err)
	}
	if gate.DB(conn) != 0 {
		t.Fatalf("SELECT 0 inside MULTI must update tracked DB, got %d", gate.DB(conn))
	}

	if err := gate.Check(conn, cmd("EXEC")); err != nil {
		t.Fatalf("EXEC failed: %v", err)
	}
	if gate.InMulti(conn) {
		t.Fatal("EXEC must clear the transaction state")
	}
}

func TestMultiDiscard(t *testing.T) {
	gate := newTestGate()
	conn := gate.Open()

	if err := gate.Check(conn, cmd("MULTI")); err != nil {
		t.Fatalf("MULTI failed: %v", err)
	}
	if err := gate.Check(conn, cmd("MOVE", "k", "1")); err == nil {
		t.Fatal("MOVE inside MULTI to DB 1 must be denied")
	}
	if err := gate.Check(conn, cmd("DISCARD")); err != nil {
		t.Fatalf("DISCARD failed: %v", err)
	}
	if gate.InMulti(conn) {
		t.Fatal("DISCARD must clear the transaction state")
	}
	if gate.DB(conn) != 0 {
		t.Fatalf("DB must remain 0, got %d", gate.DB(conn))
	}
}

func TestStateAfterDenialAndReset(t *testing.T) {
	gate := newTestGate()
	conn := gate.Open()

	denials := [][]string{
		{"SELECT", "5"},
		{"MOVE", "k", "5"},
		{"SWAPDB", "0", "5"},
		{"COPY", "a", "b", "DB", "5"},
		{"EVAL", "return 1", "0"},
	}
	for _, command := range denials {
		if err := gate.Check(conn, cmd(command...)); err == nil {
			t.Fatalf("%s should have been denied", command[0])
		}
	}
	if gate.DB(conn) != 0 || gate.InMulti(conn) {
		t.Fatalf("state corrupted after denials: db=%d multi=%v", gate.DB(conn), gate.InMulti(conn))
	}

	if err := gate.Check(conn, cmd("RESET")); err != nil {
		t.Fatalf("RESET failed: %v", err)
	}
	if gate.DB(conn) != 0 || gate.InMulti(conn) {
		t.Fatalf("RESET must restore default state, got db=%d multi=%v", gate.DB(conn), gate.InMulti(conn))
	}

	gate.Close(conn)
	if gate.DB(conn) != 0 {
		t.Fatal("closed connection must report the default DB")
	}
}

func TestConnectionsIsolated(t *testing.T) {
	policy := NewPolicy(Config{AllowedDBs: []int{0, 1}, BlockScripting: true})
	gate := NewGate(Config{AllowedDBs: []int{0, 1}, BlockScripting: true})

	first := gate.Open()
	second := gate.Open()

	if err := gate.Check(first, cmd("SELECT", "1")); err != nil {
		t.Fatalf("SELECT 1 should be allowed by the two-DB policy, got %v", err)
	}
	if gate.DB(first) != 1 {
		t.Fatalf("first connection should be on DB 1, got %d", gate.DB(first))
	}
	if gate.DB(second) != 0 {
		t.Fatalf("second connection must stay on DB 0, got %d", gate.DB(second))
	}
	if err := gate.Check(second, cmd("SELECT", "2")); err == nil {
		t.Fatal("SELECT 2 must be denied under the two-DB policy")
	}
	if !policy.Allows(1) || policy.Allows(2) {
		t.Fatal("compiled policy membership is wrong")
	}
}

func TestConcurrentConnections(t *testing.T) {
	gate := newTestGate()
	const workers = 16
	const iterations = 200

	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn := gate.Open()
			defer gate.Close(conn)
			for i := 0; i < iterations; i++ {
				if i%3 == 0 {
					if err := gate.Check(conn, cmd("SELECT", "1")); err == nil {
						t.Error("SELECT 1 must be denied")
						return
					}
				} else {
					if err := gate.Check(conn, cmd("GET", "k")); err != nil {
						t.Errorf("GET failed: %v", err)
						return
					}
				}
				if gate.DB(conn) != 0 {
					t.Errorf("connection drifted to DB %d", gate.DB(conn))
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestConcurrentCommandsOnSameConnection hammers one connection from several
// goroutines. The per-connection lock must keep its state race-free while
// every forbidden command is still rejected.
func TestConcurrentCommandsOnSameConnection(t *testing.T) {
	gate := newTestGate()
	conn := gate.Open()
	const workers = 8
	const iterations = 300

	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				switch (worker + i) % 4 {
				case 0:
					if err := gate.Check(conn, cmd("MULTI")); err != nil {
						t.Errorf("MULTI failed: %v", err)
						return
					}
				case 1:
					if err := gate.Check(conn, cmd("EXEC")); err != nil {
						t.Errorf("EXEC failed: %v", err)
						return
					}
				case 2:
					if err := gate.Check(conn, cmd("SELECT", "1")); err == nil {
						t.Error("SELECT 1 must be denied")
						return
					}
				default:
					if err := gate.Check(conn, cmd("GET", "k")); err != nil {
						t.Errorf("GET failed: %v", err)
						return
					}
				}
				if gate.DB(conn) != 0 {
					t.Errorf("connection drifted to DB %d", gate.DB(conn))
					return
				}
			}
		}(worker)
	}
	wg.Wait()
}

func TestErrorReplyFormat(t *testing.T) {
	gate := newTestGate()
	conn := gate.Open()
	err := gate.Check(conn, cmd("SELECT", "1"))
	if err == nil {
		t.Fatal("expected denial")
	}
	reply := err.Reply()
	if !strings.HasPrefix(reply, "-NOPERM ") || !strings.HasSuffix(reply, "\r\n") {
		t.Fatalf("malformed RESP error reply: %q", reply)
	}

	argErr := gate.Check(conn, cmd("SELECT", "x"))
	if argErr == nil || !strings.HasPrefix(argErr.Reply(), "-ERR ") {
		t.Fatalf("expected ERR reply, got %v", argErr)
	}
}
