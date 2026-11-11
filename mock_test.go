package imapsync

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
)

// scriptStep is one step of a scripted mock IMAP server conversation.
type scriptStep struct {
	expect string   // regexp matched against the client command (tag stripped); "" = unsolicited send
	send   []string // raw response bytes, sent verbatim; "{TAG}" expands to the command tag
	chunk  int      // if > 0, split sends into writes of at most this many bytes
	drop   bool     // close the connection after this step
}

func line(s string) string { return s + "\r\n" }

// mockDialer hands out scripted connections, one script per Dial call.
type mockDialer struct {
	scripts [][]scriptStep
	calls   int
	done    []chan error
}

func (d *mockDialer) Dial() (io.ReadWriteCloser, error) {
	i := d.calls
	d.calls++
	if i >= len(d.scripts) {
		return nil, fmt.Errorf("unexpected connection #%d", i+1)
	}
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	done := make(chan error, 1)
	d.done = append(d.done, done)
	go serveScript(d.scripts[i], serverR, serverW, done)
	return &pipeConn{r: clientR, w: clientW, closeAll: func() {
		clientR.Close()
		clientW.Close()
		serverR.Close()
		serverW.Close()
	}}, nil
}

// wait fails the test if any served script reported an error.
func (d *mockDialer) wait(t *testing.T) {
	t.Helper()
	for i, ch := range d.done {
		if err := <-ch; err != nil {
			t.Fatalf("connection %d script: %v", i+1, err)
		}
	}
}

type pipeConn struct {
	r        *io.PipeReader
	w        *io.PipeWriter
	closeAll func()
}

func (c *pipeConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *pipeConn) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *pipeConn) Close() error                { c.closeAll(); return nil }

var tagRe = regexp.MustCompile(`^(\S+)\s+(.*)$`)

func serveScript(steps []scriptStep, r io.Reader, w io.Writer, done chan<- error) {
	br := bufio.NewReader(r)
	for i, st := range steps {
		tag := ""
		if st.expect != "" {
			raw, err := br.ReadString('\n')
			if err != nil {
				done <- fmt.Errorf("step %d: reading command: %w", i, err)
				return
			}
			m := tagRe.FindStringSubmatch(strings.TrimRight(raw, "\r\n"))
			if m == nil {
				done <- fmt.Errorf("step %d: malformed command %q", i, raw)
				return
			}
			tag = m[1]
			if cmd := m[2]; !regexp.MustCompile(st.expect).MatchString(cmd) {
				done <- fmt.Errorf("step %d: command %q does not match %q", i, cmd, st.expect)
				return
			}
		}
		for _, s := range st.send {
			data := strings.ReplaceAll(s, "{TAG}", tag)
			if err := writeChunked(w, []byte(data), st.chunk); err != nil {
				done <- fmt.Errorf("step %d: writing response: %w", i, err)
				return
			}
		}
		if st.drop {
			// Simulate a dropped TCP connection: both directions die,
			// so pending client reads and writes fail instead of blocking.
			if c, ok := w.(io.Closer); ok {
				c.Close()
			}
			if c, ok := r.(io.Closer); ok {
				c.Close()
			}
			done <- nil
			return
		}
	}
	done <- nil
}

func writeChunked(w io.Writer, data []byte, chunk int) error {
	for len(data) > 0 {
		n := len(data)
		if chunk > 0 && n > chunk {
			n = chunk
		}
		if _, err := w.Write(data[:n]); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}
