package netsec

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
)

var testLimits = Limits{
	MaxStatementTimeout: 30 * time.Second,
	MaxLockTimeout:      10 * time.Second,
}

// buildMsg assembles a raw startup message from key/value pairs.
func buildMsg(pairs ...string) []byte {
	if len(pairs)%2 != 0 {
		panic("buildMsg needs an even number of arguments")
	}
	var b bytes.Buffer
	b.Write([]byte{0, 0, 0, 0})
	var proto [4]byte
	binary.BigEndian.PutUint32(proto[:], ProtocolVersion30)
	b.Write(proto[:])
	for i := 0; i < len(pairs); i += 2 {
		b.WriteString(pairs[i])
		b.WriteByte(0)
		b.WriteString(pairs[i+1])
		b.WriteByte(0)
	}
	b.WriteByte(0)
	msg := b.Bytes()
	binary.BigEndian.PutUint32(msg[0:4], uint32(len(msg)))
	return msg
}

func TestValidStartupMessage(t *testing.T) {
	msg := buildMsg(
		"user", "alice",
		"database", "shop",
		"application_name", "psql",
		"client_encoding", "UTF8",
		"options", "-c statement_timeout=5s -c lock_timeout=2000",
	)
	p, err := ValidateStartupMessage(msg, testLimits)
	if err != nil {
		t.Fatalf("valid message rejected: %v", err)
	}
	if p.User != "alice" || p.Database != "shop" || p.ApplicationName != "psql" || p.ClientEncoding != "UTF8" {
		t.Errorf("unexpected params: %+v", p)
	}
	if p.StatementTimeout != 5*time.Second {
		t.Errorf("StatementTimeout = %v, want 5s", p.StatementTimeout)
	}
	if p.LockTimeout != 2*time.Second {
		t.Errorf("LockTimeout = %v, want 2s", p.LockTimeout)
	}
}

func TestValidMinimalMessage(t *testing.T) {
	p, err := ValidateStartupMessage(buildMsg("user", "bob"), testLimits)
	if err != nil {
		t.Fatalf("minimal message rejected: %v", err)
	}
	if p.User != "bob" {
		t.Errorf("User = %q, want bob", p.User)
	}
}

func TestReplicationParameterRejected(t *testing.T) {
	for _, value := range []string{"true", "database", "1", "on"} {
		msg := buildMsg("user", "alice", "replication", value)
		if _, err := ValidateStartupMessage(msg, testLimits); !errors.Is(err, ErrDisallowedParameter) {
			t.Errorf("replication=%s: err = %v, want ErrDisallowedParameter", value, err)
		}
	}
}

func TestSessionAuthorizationRejected(t *testing.T) {
	msg := buildMsg("user", "alice", "session_authorization", "postgres")
	if _, err := ValidateStartupMessage(msg, testLimits); !errors.Is(err, ErrDisallowedParameter) {
		t.Fatalf("err = %v, want ErrDisallowedParameter", err)
	}
}

func TestUnknownParameterRejected(t *testing.T) {
	for _, key := range []string{"search_path", "DateStyle", "geqo", "extra_float_digits", "foo"} {
		msg := buildMsg("user", "alice", key, "x")
		if _, err := ValidateStartupMessage(msg, testLimits); !errors.Is(err, ErrDisallowedParameter) {
			t.Errorf("key %q: err = %v, want ErrDisallowedParameter", key, err)
		}
	}
}

func TestDuplicateKeyRejected(t *testing.T) {
	msg := buildMsg("user", "alice", "database", "a", "database", "b")
	if _, err := ValidateStartupMessage(msg, testLimits); !errors.Is(err, ErrDuplicateParameter) {
		t.Fatalf("err = %v, want ErrDuplicateParameter", err)
	}

	msg = buildMsg("user", "alice", "user", "mallory")
	if _, err := ValidateStartupMessage(msg, testLimits); !errors.Is(err, ErrDuplicateParameter) {
		t.Fatalf("duplicate user: err = %v, want ErrDuplicateParameter", err)
	}
}

func TestUserRequired(t *testing.T) {
	if _, err := ValidateStartupMessage(buildMsg("database", "shop"), testLimits); !errors.Is(err, ErrMissingUser) {
		t.Fatalf("missing user: err = %v, want ErrMissingUser", err)
	}
	if _, err := ValidateStartupMessage(buildMsg("user", ""), testLimits); !errors.Is(err, ErrMissingUser) {
		t.Fatalf("empty user: err = %v, want ErrMissingUser", err)
	}
}

func TestMessageTooLarge(t *testing.T) {
	// Declared length above the cap.
	msg := buildMsg("user", "alice")
	binary.BigEndian.PutUint32(msg[0:4], MaxStartupMessageLength+1)
	if _, err := ValidateStartupMessage(msg, testLimits); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("declared oversize: err = %v, want ErrMessageTooLarge", err)
	}

	// Real oversize message padded with a long application_name.
	msg = buildMsg("user", "alice", "application_name", strings.Repeat("a", MaxStartupMessageLength))
	if _, err := ValidateStartupMessage(msg, testLimits); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("actual oversize: err = %v, want ErrMessageTooLarge", err)
	}
}

func TestNotNULTerminated(t *testing.T) {
	msg := buildMsg("user", "alice")
	truncated := msg[:len(msg)-1] // drop the terminating empty key
	binary.BigEndian.PutUint32(truncated[0:4], uint32(len(truncated)))
	if _, err := ValidateStartupMessage(truncated, testLimits); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("err = %v, want ErrMalformedMessage", err)
	}
}

func TestTrailingDataAfterTerminator(t *testing.T) {
	msg := buildMsg("user", "alice")
	msg = append(msg, 0x41) // junk after the terminating NUL
	binary.BigEndian.PutUint32(msg[0:4], uint32(len(msg)))
	if _, err := ValidateStartupMessage(msg, testLimits); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("err = %v, want ErrMalformedMessage", err)
	}
}

func TestNonUTF8Rejected(t *testing.T) {
	msg := buildMsg("user", "alice", "application_name", "bad\xffname")
	if _, err := ValidateStartupMessage(msg, testLimits); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("non-UTF-8 value: err = %v, want ErrMalformedMessage", err)
	}
}

func TestLengthMismatch(t *testing.T) {
	msg := buildMsg("user", "alice")
	binary.BigEndian.PutUint32(msg[0:4], uint32(len(msg)+4))
	if _, err := ValidateStartupMessage(msg, testLimits); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("err = %v, want ErrMalformedMessage", err)
	}
}

func TestUnsupportedProtocol(t *testing.T) {
	msg := buildMsg("user", "alice")
	binary.BigEndian.PutUint32(msg[4:8], 196609) // 3.1
	if _, err := ValidateStartupMessage(msg, testLimits); !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("err = %v, want ErrUnsupportedProtocol", err)
	}
}

func TestOptionsBypassAttempts(t *testing.T) {
	cases := []struct {
		name    string
		options string
		want    error
	}{
		{"-c search_path", "-c search_path=public", ErrInvalidOptions},
		{"-csearch_path compact", "-csearch_path=public", ErrInvalidOptions},
		{"--search_path", "--search_path=public", ErrInvalidOptions},
		{"--search_path separate", "--search_path public", ErrInvalidOptions},
		{"uppercase -c SEARCH_PATH", "-c SEARCH_PATH=public", ErrInvalidOptions},
		{"mixed case --Search_Path", "--Search_Path=public", ErrInvalidOptions},
		{"escaped dashes", `\-\-search_path=public`, ErrInvalidOptions},
		{"escaped -c flag", `-\c search_path=public`, ErrInvalidOptions},
		{"escaped name", `-c sea\rch_path=public`, ErrInvalidOptions},
		{"-c session_authorization", "-c session_authorization=postgres", ErrInvalidOptions},
		{"-c work_mem", "-c work_mem=4MB", ErrInvalidOptions},
		{"bare word", "search_path=public", ErrInvalidOptions},
		{"dangling -c", "-c", ErrInvalidOptions},
		{"missing equals", "-c statement_timeout", ErrInvalidOptions},
		{"empty value", "-c statement_timeout=", ErrInvalidOptions},
		{"statement_timeout over limit", "-c statement_timeout=31s", ErrTimeoutExceedsLimit},
		{"statement_timeout over limit in ms", "-c statement_timeout=30001", ErrTimeoutExceedsLimit},
		{"lock_timeout over limit", "-c lock_timeout=11s", ErrTimeoutExceedsLimit},
		{"huge overflow value", "-c statement_timeout=99999999999999999999999", ErrInvalidOptions},
		{"bad unit", "-c statement_timeout=5x", ErrInvalidOptions},
		{"negative", "-c statement_timeout=-1", ErrInvalidOptions},
		{"trailing backslash", `-c statement_timeout=5\`, ErrInvalidOptions},
		{"escaped space inside -c token", `-c\ statement_timeout=5s`, ErrInvalidOptions},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := buildMsg("user", "alice", "options", tc.options)
			if _, err := ValidateStartupMessage(msg, testLimits); !errors.Is(err, tc.want) {
				t.Fatalf("options %q: err = %v, want %v", tc.options, err, tc.want)
			}
		})
	}
}

func TestOptionsAllowedForms(t *testing.T) {
	cases := []struct {
		name     string
		options  string
		stmtWant time.Duration
		lockWant time.Duration
	}{
		{"plain ms", "-c statement_timeout=5000", 5 * time.Second, 0},
		{"compact -c", "-cstatement_timeout=5000", 5 * time.Second, 0},
		{"seconds unit", "-c statement_timeout=5s", 5 * time.Second, 0},
		{"fraction-free ms unit", "-c statement_timeout=250ms", 250 * time.Millisecond, 0},
		{"zero allowed", "-c statement_timeout=0", 0, 0},
		{"at the limit", "-c statement_timeout=30s -c lock_timeout=10s", 30 * time.Second, 10 * time.Second},
		{"tab separated", "-c\tstatement_timeout=5s", 5 * time.Second, 0},
		{"case-insensitive name", "-c Statement_Timeout=5s", 5 * time.Second, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := buildMsg("user", "alice", "options", tc.options)
			p, err := ValidateStartupMessage(msg, testLimits)
			if err != nil {
				t.Fatalf("options %q rejected: %v", tc.options, err)
			}
			if p.StatementTimeout != tc.stmtWant {
				t.Errorf("StatementTimeout = %v, want %v", p.StatementTimeout, tc.stmtWant)
			}
			if p.LockTimeout != tc.lockWant {
				t.Errorf("LockTimeout = %v, want %v", p.LockTimeout, tc.lockWant)
			}
		})
	}
}

func sslRequestPacket() []byte {
	pkt := make([]byte, 8)
	binary.BigEndian.PutUint32(pkt[0:4], 8)
	binary.BigEndian.PutUint32(pkt[4:8], SSLRequestCode)
	return pkt
}

func gssencRequestPacket() []byte {
	pkt := make([]byte, 8)
	binary.BigEndian.PutUint32(pkt[0:4], 8)
	binary.BigEndian.PutUint32(pkt[4:8], GSSENCRequestCode)
	return pkt
}

func TestSSLRequestClean(t *testing.T) {
	r := bufio.NewReader(bytes.NewReader(sslRequestPacket()))
	code, msg, err := ReadStartupPacket(r)
	if err != nil {
		t.Fatalf("clean SSLRequest rejected: %v", err)
	}
	if code != SSLRequestCode || msg != nil {
		t.Errorf("code = %d, msg = %v", code, msg)
	}
}

func TestGSSENCRequestClean(t *testing.T) {
	r := bufio.NewReader(bytes.NewReader(gssencRequestPacket()))
	code, _, err := ReadStartupPacket(r)
	if err != nil {
		t.Fatalf("clean GSSENCRequest rejected: %v", err)
	}
	if code != GSSENCRequestCode {
		t.Errorf("code = %d, want %d", code, GSSENCRequestCode)
	}
}

func TestSSLRequestWithPipelinedBytes(t *testing.T) {
	// Attacker pipelines a startup message right behind the
	// SSLRequest in the same read buffer.
	payload := append(sslRequestPacket(), buildMsg("user", "mallory")...)
	r := bufio.NewReader(bytes.NewReader(payload))
	if _, _, err := ReadStartupPacket(r); !errors.Is(err, ErrPipelinedBytes) {
		t.Fatalf("err = %v, want ErrPipelinedBytes", err)
	}
}

func TestGSSENCRequestWithPipelinedBytes(t *testing.T) {
	payload := append(gssencRequestPacket(), []byte("N")...)
	r := bufio.NewReader(bytes.NewReader(payload))
	if _, _, err := ReadStartupPacket(r); !errors.Is(err, ErrPipelinedBytes) {
		t.Fatalf("err = %v, want ErrPipelinedBytes", err)
	}
}

func TestReadStartupPacketValidStartup(t *testing.T) {
	raw := buildMsg("user", "alice", "options", "-c lock_timeout=1s")
	r := bufio.NewReader(bytes.NewReader(raw))
	code, msg, err := ReadStartupPacket(r)
	if err != nil {
		t.Fatalf("ReadStartupPacket: %v", err)
	}
	if code != ProtocolVersion30 {
		t.Errorf("code = %d, want %d", code, ProtocolVersion30)
	}
	p, err := ValidateStartupMessage(msg, testLimits)
	if err != nil {
		t.Fatalf("ValidateStartupMessage: %v", err)
	}
	if p.User != "alice" || p.LockTimeout != time.Second {
		t.Errorf("unexpected params: %+v", p)
	}
}

func TestReadStartupPacketOversize(t *testing.T) {
	pkt := make([]byte, 8)
	binary.BigEndian.PutUint32(pkt[0:4], MaxStartupMessageLength+1)
	binary.BigEndian.PutUint32(pkt[4:8], ProtocolVersion30)
	r := bufio.NewReader(bytes.NewReader(pkt))
	if _, _, err := ReadStartupPacket(r); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("err = %v, want ErrMessageTooLarge", err)
	}
}
