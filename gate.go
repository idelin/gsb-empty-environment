// Package netsec provides an importable database-binding gate for a Go Redis
// proxy.
//
// A restricted connection is pinned to a fixed set of logical databases (in
// the common case only DB 0). The gate inspects every command on a
// connection, tracks the database currently selected by the connection
// (including SELECT issued inside a pipeline or a MULTI/EXEC transaction) and
// rejects, with a Redis error reply, every command that would touch a
// database outside the allowed set. Scripting (EVAL family and FUNCTION) is
// denied wholesale for restricted users.
package netsec

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Error kinds reported by the gate. They allow callers to distinguish a
// permission denial from a malformed argument without parsing the message.
const (
	// KindDeniedDB means the command would touch a database the connection
	// is not allowed to use.
	KindDeniedDB = "denied_db"
	// KindDeniedCommand means the command itself is forbidden for the user
	// (for example EVAL or FUNCTION).
	KindDeniedCommand = "denied_command"
	// KindInvalidArg means the command carried an invalid argument (bad
	// arity or a database index that is not a non-negative integer).
	KindInvalidArg = "invalid_arg"
)

// Error is the error reply produced by the gate. Error returns the exact
// bytes that should be written on the wire as a RESP error reply, including
// the leading error prefix and the trailing CRLF.
type Error struct {
	Kind    string
	Prefix  string
	Message string
}

func (e *Error) Error() string { return e.Prefix + " " + e.Message }

// Reply returns the RESP error reply ("-PREFIX MESSAGE\r\n").
func (e *Error) Reply() string { return "-" + e.Error() + "\r\n" }

func deniedDB(format string, args ...any) *Error {
	return &Error{Kind: KindDeniedDB, Prefix: "NOPERM", Message: fmt.Sprintf(format, args...)}
}

func deniedCommand(format string, args ...any) *Error {
	return &Error{Kind: KindDeniedCommand, Prefix: "NOPERM", Message: fmt.Sprintf(format, args...)}
}

func invalidArg(format string, args ...any) *Error {
	return &Error{Kind: KindInvalidArg, Prefix: "ERR", Message: fmt.Sprintf(format, args...)}
}

// Config describes the policy applied by a Gate.
type Config struct {
	// AllowedDBs is the set of logical databases a connection may select
	// or otherwise touch. A nil/empty slice pins every connection to DB 0.
	AllowedDBs []int
	// BlockScripting denies the EVAL/EVALSHA/EVAL_RO/EVALSHA_RO family and
	// every FUNCTION subcommand when true.
	BlockScripting bool
}

// RestrictedConfig returns a config for the canonical restricted user: only
// DB 0 is reachable and scripting is forbidden.
func RestrictedConfig() Config {
	return Config{AllowedDBs: []int{0}, BlockScripting: true}
}

// Policy is a compiled, immutable Config. It is safe for concurrent use.
type Policy struct {
	allowed        map[int]struct{}
	blockScripting bool
}

// NewPolicy compiles a Config into a reusable Policy. An empty AllowedDBs
// list is treated as "DB 0 only".
func NewPolicy(cfg Config) *Policy {
	allowed := make(map[int]struct{}, len(cfg.AllowedDBs))
	if len(cfg.AllowedDBs) == 0 {
		allowed[0] = struct{}{}
	} else {
		for _, db := range cfg.AllowedDBs {
			allowed[db] = struct{}{}
		}
	}
	return &Policy{allowed: allowed, blockScripting: cfg.BlockScripting}
}

// Allows reports whether db is reachable under the policy.
func (p *Policy) Allows(db int) bool {
	_, ok := p.allowed[db]
	return ok
}

// ConnState is the per-connection state maintained by the gate. The zero
// value is a fresh connection sitting on DB 0 outside of a transaction.
type ConnState struct {
	mu    sync.Mutex
	db    int
	multi bool
}

// DB returns the database currently selected by the connection.
func (s *ConnState) DB() int { return s.db }

// InMulti reports whether the connection is inside a MULTI/EXEC transaction.
func (s *ConnState) InMulti() bool { return s.multi }

// Apply runs the gate for one command against an explicit connection state.
// It is the stateless core used by Gate; proxies that already keep their own
// per-connection state can call it directly.
//
// args is the parsed command as received from the client: args[0] is the
// command name and the remaining elements are its arguments. Command names
// are matched case-insensitively. A non-nil error is the error reply that
// must be sent to the client instead of forwarding the command; in that case
// the connection state is left untouched.
func Apply(policy *Policy, state *ConnState, args [][]byte) *Error {
	if len(args) == 0 {
		return invalidArg("empty command")
	}
	cmd := strings.ToUpper(string(args[0]))

	if policy.blockScripting && isScriptingCommand(cmd) {
		return deniedCommand("%s is not allowed for this user", cmd)
	}

	switch cmd {
	case "SELECT":
		if len(args) != 2 {
			return invalidArg("wrong number of arguments for 'SELECT' command")
		}
		db, err := parseDB(args[1])
		if err != nil {
			return err
		}
		if !policy.Allows(db) {
			return deniedDB("SELECT to DB %d is not allowed; this connection is bound to the permitted database(s)", db)
		}
		// SELECT takes effect immediately, even when pipelined or queued
		// inside MULTI, so subsequent commands in the same buffer are
		// evaluated against the newly selected database.
		state.db = db
		return nil

	case "MOVE":
		if len(args) != 3 {
			return invalidArg("wrong number of arguments for 'MOVE' command")
		}
		db, err := parseDB(args[2])
		if err != nil {
			return err
		}
		if !policy.Allows(db) {
			return deniedDB("MOVE to DB %d is not allowed", db)
		}
		return nil

	case "SWAPDB":
		if len(args) != 3 {
			return invalidArg("wrong number of arguments for 'SWAPDB' command")
		}
		first, err := parseDB(args[1])
		if err != nil {
			return err
		}
		second, err := parseDB(args[2])
		if err != nil {
			return err
		}
		if !policy.Allows(first) || !policy.Allows(second) {
			return deniedDB("SWAPDB involving DB %d and DB %d is not allowed", first, second)
		}
		return nil

	case "COPY":
		return checkCopy(policy, args)

	case "MULTI":
		state.multi = true
		return nil

	case "EXEC", "DISCARD":
		state.multi = false
		return nil

	case "RESET":
		state.db = 0
		state.multi = false
		return nil

	default:
		return nil
	}
}

// checkCopy enforces the optional DB parameter of COPY. COPY is only
// rejected when it targets a forbidden database; ordinary same-DB copies are
// forwarded unchanged.
func checkCopy(policy *Policy, args [][]byte) *Error {
	targetDB := -1
	hasDB := false
	for i := 3; i < len(args); i++ {
		option := strings.ToUpper(string(args[i]))
		switch option {
		case "DB":
			if i+1 >= len(args) {
				return invalidArg("syntax error: DB requires an argument")
			}
			db, err := parseDB(args[i+1])
			if err != nil {
				return err
			}
			targetDB = db
			hasDB = true
			i++
		case "REPLACE":
			// boolean flag, no argument
		default:
			return invalidArg("syntax error: unknown COPY option '%s'", option)
		}
	}
	if hasDB && !policy.Allows(targetDB) {
		return deniedDB("COPY to DB %d is not allowed", targetDB)
	}
	return nil
}

func isScriptingCommand(cmd string) bool {
	switch cmd {
	case "EVAL", "EVALSHA", "EVAL_RO", "EVALSHA_RO",
		"FUNCTION", "FCALL", "FCALL_RO":
		return true
	default:
		return false
	}
}

// parseDB parses a non-negative database index, mirroring the integer syntax
// accepted by Redis ("0", "15", ...). Leading signs and non-numeric input are
// protocol/argument errors rather than permission denials.
func parseDB(token []byte) (int, *Error) {
	db, err := strconv.Atoi(string(token))
	if err != nil || db < 0 {
		return 0, invalidArg("invalid DB index '%s'", string(token))
	}
	return db, nil
}

// Gate keeps per-connection state for a proxy and applies one Policy to every
// connection. A Gate is safe for concurrent use; each connection carries its
// own lock so commands on different connections never block one another.
type Gate struct {
	policy *Policy

	mu     sync.Mutex
	conns  map[uint64]*ConnState
	nextID uint64
}

// NewGate builds a Gate enforcing cfg.
func NewGate(cfg Config) *Gate {
	return &Gate{
		policy: NewPolicy(cfg),
		conns:  make(map[uint64]*ConnState),
	}
}

// Policy returns the compiled policy enforced by the gate.
func (g *Gate) Policy() *Policy { return g.policy }

// Open registers a new connection and returns its identifier. The connection
// starts on DB 0.
func (g *Gate) Open() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nextID++
	id := g.nextID
	g.conns[id] = &ConnState{db: 0}
	return id
}

// Close drops the state of a connection. Closing an unknown identifier is a
// no-op.
func (g *Gate) Close(id uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.conns, id)
}

// Check inspects one command on the connection identified by id. An unknown
// id is treated as a fresh connection on DB 0 and never creates state.
// A non-nil *Error is the error reply to return to the client; the underlying
// connection state is unchanged in that case.
func (g *Gate) Check(id uint64, args [][]byte) *Error {
	g.mu.Lock()
	state := g.conns[id]
	if state == nil {
		state = &ConnState{db: 0}
	}
	g.mu.Unlock()

	state.mu.Lock()
	defer state.mu.Unlock()
	return Apply(g.policy, state, args)
}

// DB returns the database currently selected by the connection, or 0 for an
// unknown identifier.
func (g *Gate) DB(id uint64) int {
	g.mu.Lock()
	if state := g.conns[id]; state != nil {
		g.mu.Unlock()
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.db
	}
	g.mu.Unlock()
	return 0
}

// InMulti reports whether the connection is inside a MULTI/EXEC transaction.
func (g *Gate) InMulti(id uint64) bool {
	g.mu.Lock()
	if state := g.conns[id]; state != nil {
		g.mu.Unlock()
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.multi
	}
	g.mu.Unlock()
	return false
}
