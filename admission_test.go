package netsec

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// fakeWriter records every packet payload the guard emits.
type fakeWriter struct {
	packets [][]byte
	err     error
}

func (f *fakeWriter) WritePacket(payload []byte) error {
	if f.err != nil {
		return f.err
	}
	f.packets = append(f.packets, append([]byte(nil), payload...))
	return nil
}

// infilePacket builds a 0xFB LOCAL INFILE request payload.
func infilePacket(name string) []byte {
	return append([]byte{0xFB}, append([]byte(name), 0)...)
}

// authSwitchPacket builds an AuthSwitchRequest payload for plugin.
func authSwitchPacket(plugin string) []byte {
	payload := []byte{0xFE}
	payload = append(payload, plugin...)
	payload = append(payload, 0)
	payload = append(payload, []byte("plugin-data")...)
	return payload
}

// newTestGuard installs an opener backed by an in-memory file map.
func newTestGuard(t *testing.T, allowCleartext, tls bool, files map[string]string) *Guard {
	t.Helper()
	g := NewGuard(allowCleartext, tls)
	g.openFile = func(name string) (io.ReadCloser, error) {
		content, ok := files[name]
		if !ok {
			return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist}
		}
		return io.NopCloser(bytes.NewReader([]byte(content))), nil
	}
	return g
}

func TestParseLocalInfileFile(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "basic single quotes",
			query: "LOAD DATA LOCAL INFILE '/etc/passwd'",
			want:  "/etc/passwd",
		},
		{
			name:  "lower case keywords and double quotes",
			query: `load data local infile "data.csv"`,
			want:  "data.csv",
		},
		{
			name:  "with low priority and surrounding comments",
			query: "/* c */ LOAD DATA LOW_PRIORITY LOCAL INFILE 'a.txt' -- tail\n",
			want:  "a.txt",
		},
		{
			name:  "concurrent variant",
			query: "LOAD DATA CONCURRENT LOCAL INFILE 'a.txt'",
			want:  "a.txt",
		},
		{
			name:  "backslash escaped quote decoded",
			query: `LOAD DATA LOCAL INFILE 'it\'s.csv'`,
			want:  "it's.csv",
		},
		{
			name:  "non local is not armed",
			query: "LOAD DATA INFILE 'a.txt'",
			want:  "",
		},
		{
			name:  "ordinary select",
			query: "SELECT 1",
			want:  "",
		},
		{
			name:  "multi statement rejected",
			query: "LOAD DATA LOCAL INFILE 'a.txt'; SELECT 1",
			want:  "",
		},
		{
			name:  "missing filename",
			query: "LOAD DATA LOCAL INFILE ",
			want:  "",
		},
		{
			name:  "unclosed string",
			query: "LOAD DATA LOCAL INFILE 'a.txt",
			want:  "",
		},
		{
			name:  "keyword prefix collision",
			query: "LOADER DATA LOCAL INFILE 'a.txt'",
			want:  "",
		},
		{
			name:  "leading junk",
			query: "X LOAD DATA LOCAL INFILE 'a.txt'",
			want:  "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseLocalInfileFile(tc.query); got != tc.want {
				t.Fatalf("parseLocalInfileFile(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}

// TestMidStream0xFBRejected is the first required check: a 0xFB that arrives
// when the client never armed a LOAD DATA LOCAL INFILE (or after an unrelated
// statement) must be refused and answered with one empty packet.
func TestMidStream0xFBRejected(t *testing.T) {
	g := newTestGuard(t, false, false, nil)
	w := &fakeWriter{}

	// A benign SELECT disarms anything; the server then lies and asks for a file.
	g.NoteQuery("SELECT * FROM users")
	file, err := g.AdmitLocalInfile(infilePacket("/etc/passwd"), w)
	if !errors.Is(err, ErrLocalInfileDenied) {
		t.Fatalf("expected ErrLocalInfileDenied, got %v", err)
	}
	if file != nil {
		t.Fatal("no file handle should be returned on denial")
	}
	if len(w.packets) != 1 || len(w.packets[0]) != 0 {
		t.Fatalf("expected exactly one empty packet, got %v", w.packets)
	}

	// The authorization was consumed, so an identical retry still fails.
	file, err = g.AdmitLocalInfile(infilePacket("/etc/passwd"), w)
	if !errors.Is(err, ErrLocalInfileDenied) {
		t.Fatalf("retry should stay denied, got %v", err)
	}
	if file != nil || len(w.packets) != 2 {
		t.Fatalf("retry must emit another empty packet, got %d", len(w.packets))
	}
}

// TestChangedPathRejected is the second required check: even after a real
// LOAD DATA LOCAL INFILE, a request naming a different path must be rejected.
func TestChangedPathRejected(t *testing.T) {
	files := map[string]string{
		"imports/data.csv": "id,name\n1,a\n",
		"/etc/passwd":      "root:x:0:0\n",
	}
	g := newTestGuard(t, false, false, files)
	w := &fakeWriter{}

	g.NoteQuery("LOAD DATA LOCAL INFILE 'imports/data.csv'")
	if g.PendingInfile() != "imports/data.csv" {
		t.Fatalf("unexpected armed file %q", g.PendingInfile())
	}

	// Server swaps the path for a sensitive file.
	file, err := g.AdmitLocalInfile(infilePacket("/etc/passwd"), w)
	if !errors.Is(err, ErrLocalInfileDenied) {
		t.Fatalf("path swap should be denied, got %v", err)
	}
	if file != nil {
		t.Fatal("file must not be opened for a swapped path")
	}
	if len(w.packets) != 1 || len(w.packets[0]) != 0 {
		t.Fatalf("expected empty terminating packet, got %v", w.packets)
	}

	// The original statement can no longer be replayed either.
	if _, err := g.AdmitLocalInfile(infilePacket("imports/data.csv"), w); !errors.Is(err, ErrLocalInfileDenied) {
		t.Fatalf("consumed statement must not remain armed, got %v", err)
	}
}

func TestExactMatchOpensFile(t *testing.T) {
	content := "id,name\n1,a\n"
	g := newTestGuard(t, false, false, map[string]string{"data.csv": content})
	w := &fakeWriter{}

	g.NoteQuery("LOAD DATA LOCAL INFILE 'data.csv'")
	file, err := g.AdmitLocalInfile(infilePacket("data.csv"), w)
	if err != nil {
		t.Fatalf("matching request should be admitted, got %v", err)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("file contents = %q, want %q", got, content)
	}
	if len(w.packets) != 0 {
		t.Fatalf("admission must not write packets, got %v", w.packets)
	}
	if g.PendingInfile() != "" {
		t.Fatal("pending state must be consumed after admission")
	}
}

func TestSubsequentStatementDisarms(t *testing.T) {
	g := newTestGuard(t, false, false, map[string]string{"data.csv": "x"})
	w := &fakeWriter{}

	g.NoteQuery("LOAD DATA LOCAL INFILE 'data.csv'")
	g.NoteQuery("SELECT 1")
	if g.PendingInfile() != "" {
		t.Fatal("unrelated statement must disarm pending infile")
	}
	if _, err := g.AdmitLocalInfile(infilePacket("data.csv"), w); !errors.Is(err, ErrLocalInfileDenied) {
		t.Fatalf("late 0xFB after another statement must be denied, got %v", err)
	}
}

func TestMalformedLocalInfilePayload(t *testing.T) {
	g := newTestGuard(t, false, false, nil)
	w := &fakeWriter{}

	for _, payload := range [][]byte{nil, {0x00}, {0xFB}, []byte("FB plain")} {
		if _, err := g.AdmitLocalInfile(payload, w); !errors.Is(err, ErrLocalInfileDenied) {
			t.Fatalf("payload %v must be fail-closed denied, got %v", payload, err)
		}
	}
	if len(w.packets) != 4 {
		t.Fatalf("each denial writes an empty packet, got %d", len(w.packets))
	}
}

func TestOpenFailureTerminatesTransfer(t *testing.T) {
	g := newTestGuard(t, false, false, nil) // no files -> open fails
	w := &fakeWriter{}
	g.NoteQuery("LOAD DATA LOCAL INFILE 'missing.csv'")

	file, err := g.AdmitLocalInfile(infilePacket("missing.csv"), w)
	if !errors.Is(err, ErrLocalInfileDenied) {
		t.Fatalf("open failure should be wrapped in denial, got %v", err)
	}
	if file != nil {
		t.Fatal("nil handle expected on open failure")
	}
	if len(w.packets) != 1 || len(w.packets[0]) != 0 {
		t.Fatalf("open failure must still empty-terminate the transfer, got %v", w.packets)
	}
}

func TestWriteFailureOnDenialIsWrapped(t *testing.T) {
	g := newTestGuard(t, false, false, nil)
	writeErr := errors.New("connection reset")
	w := &fakeWriter{err: writeErr}

	_, err := g.AdmitLocalInfile(infilePacket("/etc/passwd"), w)
	if !errors.Is(err, ErrLocalInfileDenied) || !errors.Is(err, writeErr) {
		t.Fatalf("denial and write errors must both be observable, got %v", err)
	}
}

func TestNewGuardWiresRealOpenerAndTLS(t *testing.T) {
	g := NewGuard(true, true)
	if !g.TLS() {
		t.Fatal("TLS flag must be exposed")
	}

	// The default opener must reach the real file system. Open a file created
	// under the test working directory, which is guaranteed writable.
	path := filepath.Join(t.TempDir(), "real.csv")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := g.openFile(path)
	if err != nil {
		t.Fatalf("default opener should use os.Open, got %v", err)
	}
	file.Close()

	if _, err := g.openFile(filepath.Join(t.TempDir(), "absent")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file must surface os.ErrNotExist, got %v", err)
	}
}

func TestDecodeMySQLEscape(t *testing.T) {
	cases := map[byte]byte{
		'0': 0, 'b': '\b', 'n': '\n', 'r': '\r', 't': '\t', 'Z': 0x1a,
		'\\': '\\', '\'': '\'', '"': '"',
	}
	for in, want := range cases {
		if got := decodeMySQLEscape(in); got != want {
			t.Fatalf("decodeMySQLEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCleartextAuthSwitch is the third required check: mysql_clear_password via
// AuthSwitchRequest needs both the caller opt-in and a TLS channel.
func TestCleartextAuthSwitch(t *testing.T) {
	payload := authSwitchPacket("mysql_clear_password")

	t.Run("denied over plaintext even when opted in", func(t *testing.T) {
		g := newTestGuard(t, true, false, nil)
		if err := g.AdmitAuthSwitch(payload); !errors.Is(err, ErrCleartextDenied) {
			t.Fatalf("cleartext on plaintext channel must be denied, got %v", err)
		}
	})

	t.Run("denied over TLS without opt in", func(t *testing.T) {
		g := newTestGuard(t, false, true, nil)
		if err := g.AdmitAuthSwitch(payload); !errors.Is(err, ErrCleartextDenied) {
			t.Fatalf("cleartext without opt-in must be denied, got %v", err)
		}
	})

	t.Run("denied with neither", func(t *testing.T) {
		g := newTestGuard(t, false, false, nil)
		if err := g.AdmitAuthSwitch(payload); !errors.Is(err, ErrCleartextDenied) {
			t.Fatalf("cleartext default must be denied, got %v", err)
		}
	})

	t.Run("allowed over TLS with opt in", func(t *testing.T) {
		g := newTestGuard(t, true, true, nil)
		if err := g.AdmitAuthSwitch(payload); err != nil {
			t.Fatalf("TLS + opt-in must be allowed, got %v", err)
		}
	})
}

func TestAuthSwitchOtherPluginsPassThrough(t *testing.T) {
	for _, plugin := range []string{"mysql_native_password", "caching_sha2_password"} {
		g := newTestGuard(t, false, false, nil)
		if err := g.AdmitAuthSwitch(authSwitchPacket(plugin)); err != nil {
			t.Fatalf("plugin %q must pass through regardless of TLS/opt-in, got %v", plugin, err)
		}
	}
}

func TestAuthSwitchMalformed(t *testing.T) {
	g := newTestGuard(t, false, false, nil)
	for _, payload := range [][]byte{nil, {0xFE}, []byte{0xFE, 'x'}, {0x00, 0x00}} {
		if err := g.AdmitAuthSwitch(payload); !errors.Is(err, ErrCleartextDenied) {
			t.Fatalf("malformed payload %v must fail closed, got %v", payload, err)
		}
	}
}

// fakeConn drives a scripted malicious server through the guard, simulating the
// driver's result-reading loop. Each script entry is one server packet.
type fakeConn struct {
	script [][]byte
	idx    int
	out    bytes.Buffer // bytes the client streams back to the server
	writer *fakeWriter
}

func (c *fakeConn) readResultPacket() ([]byte, bool) {
	if c.idx >= len(c.script) {
		return nil, false
	}
	p := c.script[c.idx]
	c.idx++
	return p, true
}

// driveReadLoop is the driver-side loop under test: every special control byte
// is referred to the guard, ordinary packets are treated as result rows.
func driveReadLoop(t *testing.T, g *Guard, conn *fakeConn) {
	t.Helper()
	for {
		payload, ok := conn.readResultPacket()
		if !ok {
			return
		}
		switch payload[0] {
		case 0xFB:
			file, err := g.AdmitLocalInfile(payload, conn.writer)
			if err != nil {
				if !errors.Is(err, ErrLocalInfileDenied) {
					t.Fatalf("unexpected guard error: %v", err)
				}
				continue // empty terminating packet already sent
			}
			stream, _ := io.ReadAll(file)
			file.Close()
			conn.out.Write(stream)
			conn.out.WriteByte(0) // normal transfer also ends with an empty packet
		case 0xFE:
			if err := g.AdmitAuthSwitch(payload); err != nil {
				if !errors.Is(err, ErrCleartextDenied) {
					t.Fatalf("unexpected auth switch error: %v", err)
				}
				return // driver aborts; no password is ever written
			}
		default:
			// ordinary result-set row: nothing goes back
		}
	}
}

// TestReadLoopIntegration replays all three attack vectors through one loop.
func TestReadLoopIntegration(t *testing.T) {
	files := map[string]string{"imports/data.csv": "row1\nrow2\n"}

	t.Run("mid stream 0xFB leaks no bytes", func(t *testing.T) {
		g := newTestGuard(t, false, false, files)
		w := &fakeWriter{}
		conn := &fakeConn{writer: w, script: [][]byte{
			{0x01},                      // ordinary row of a SELECT
			infilePacket("/etc/passwd"), // injected mid-stream
			{0x02},                      // loop must keep processing rows
		}}
		g.NoteQuery("SELECT 1")
		driveReadLoop(t, g, conn)

		if conn.out.Len() != 0 {
			t.Fatalf("no file content may be uploaded, got %q", conn.out.String())
		}
		if len(w.packets) != 1 || len(w.packets[0]) != 0 {
			t.Fatalf("exactly one empty refusal packet expected, got %v", w.packets)
		}
	})

	t.Run("changed path leaks no bytes", func(t *testing.T) {
		g := newTestGuard(t, false, false, files)
		w := &fakeWriter{}
		conn := &fakeConn{writer: w, script: [][]byte{
			infilePacket("/etc/passwd"), // statement names imports/data.csv
		}}
		g.NoteQuery("LOAD DATA LOCAL INFILE 'imports/data.csv'")
		driveReadLoop(t, g, conn)

		if conn.out.Len() != 0 {
			t.Fatalf("path swap must upload nothing, got %q", conn.out.String())
		}
		if len(w.packets) != 1 || len(w.packets[0]) != 0 {
			t.Fatalf("one empty refusal packet expected, got %v", w.packets)
		}
	})

	t.Run("matching infile streams exactly the named file", func(t *testing.T) {
		g := newTestGuard(t, false, false, files)
		w := &fakeWriter{}
		conn := &fakeConn{writer: w, script: [][]byte{
			infilePacket("imports/data.csv"),
		}}
		g.NoteQuery("LOAD DATA LOCAL INFILE 'imports/data.csv'")
		driveReadLoop(t, g, conn)

		want := files["imports/data.csv"] + "\x00"
		if conn.out.String() != want {
			t.Fatalf("uploaded stream = %q, want %q", conn.out.String(), want)
		}
		if len(w.packets) != 0 {
			t.Fatalf("admitted transfer must not use refusal packets, got %v", w.packets)
		}
	})

	t.Run("cleartext auth switch over plaintext aborts without a password", func(t *testing.T) {
		g := newTestGuard(t, true, false, nil) // opted in, but no TLS
		w := &fakeWriter{}
		conn := &fakeConn{writer: w, script: [][]byte{
			authSwitchPacket("mysql_clear_password"),
			infilePacket("/etc/passwd"), // must never be reached after abort
		}}
		driveReadLoop(t, g, conn)

		if conn.out.Len() != 0 {
			t.Fatalf("no password or file bytes may leave the client, got %q", conn.out.String())
		}
		if len(w.packets) != 0 {
			t.Fatalf("abort must not write packets, got %v", w.packets)
		}
	})

	t.Run("cleartext auth switch over TLS proceeds", func(t *testing.T) {
		g := newTestGuard(t, true, true, nil)
		w := &fakeWriter{}
		conn := &fakeConn{writer: w, script: [][]byte{
			authSwitchPacket("mysql_clear_password"),
			{0x01},
		}}
		driveReadLoop(t, g, conn) // must not abort and must read the final row
		if conn.idx != len(conn.script) {
			t.Fatal("loop should continue after an admitted auth switch")
		}
	})
}
