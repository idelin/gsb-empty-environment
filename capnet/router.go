package capnet

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// Router is the only object a plug-in receives. It exposes operations rather
// than addresses or dialers: every method starts by validating a host-minted
// Handle and then dispatches into the deterministic simulator.
type Router struct {
	host *Host

	mu      sync.Mutex
	readers map[uint64]*bufio.Reader
}

// NewRouter constructs the plug-in-facing router.
func NewRouter(h *Host) *Router {
	return &Router{host: h, readers: make(map[uint64]*bufio.Reader)}
}

// Dial "connects" to the simulated endpoint represented by h and returns a
// net.Conn whose addresses are synthetic and whose deadlines are fake. No
// real dialer is invoked at any point.
func (r *Router) Dial(h Handle) (net.Conn, error) {
	ep, err := r.host.resolve(h)
	if err != nil {
		return nil, err
	}
	ep.mu.Lock()
	defer ep.mu.Unlock()
	if ep.conn == nil {
		ep.conn = newSimConn(ep)
	}
	return ep.conn, nil
}

// Read reads up to len(p) bytes from the simulated endpoint.
func (r *Router) Read(h Handle, p []byte) (int, error) {
	if len(p) == 0 {
		if _, err := r.host.resolve(h); err != nil {
			return 0, err
		}
		return 0, nil
	}
	c, err := r.Dial(h)
	if err != nil {
		return 0, err
	}
	return c.Read(p)
}

// Write sends p to the simulated endpoint.
func (r *Router) Write(h Handle, p []byte) (int, error) {
	c, err := r.Dial(h)
	if err != nil {
		return 0, err
	}
	return c.Write(p)
}

// Close closes the simulated connection.
func (r *Router) Close(h Handle) error {
	c, err := r.Dial(h)
	if err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.readers, h.id)
	r.mu.Unlock()
	return c.Close()
}

// RemoteAddr returns a synthetic address that never contains the real target.
func (r *Router) RemoteAddr(h Handle) (net.Addr, error) {
	c, err := r.Dial(h)
	if err != nil {
		return nil, err
	}
	return c.RemoteAddr(), nil
}

// SetDeadline / SetReadDeadline / SetWriteDeadline bind deadlines to the
// trial's fake clock.
func (r *Router) SetDeadline(h Handle, t time.Time) error {
	c, err := r.Dial(h)
	if err != nil {
		return err
	}
	return c.SetDeadline(t)
}

func (r *Router) SetReadDeadline(h Handle, t time.Time) error {
	c, err := r.Dial(h)
	if err != nil {
		return err
	}
	return c.SetReadDeadline(t)
}

func (r *Router) SetWriteDeadline(h Handle, t time.Time) error {
	c, err := r.Dial(h)
	if err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (r *Router) buffered(h Handle) (*bufio.Reader, error) {
	c, err := r.Dial(h)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	br, ok := r.readers[h.id]
	if !ok {
		br = bufio.NewReader(c)
		r.readers[h.id] = br
	}
	return br, nil
}

// ReadLine reads and returns the next line without the trailing newline.
func (r *Router) ReadLine(h Handle) (string, error) {
	br, err := r.buffered(h)
	if err != nil {
		return "", err
	}
	line, err := br.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return trimNewline(line), err
}

// ReadFrame reads a length-prefixed binary frame: a four-byte big-endian
// length followed by that many payload bytes.
func (r *Router) ReadFrame(h Handle) ([]byte, error) {
	br, err := r.buffered(h)
	if err != nil {
		return nil, err
	}
	var header [4]byte
	if _, err := io.ReadFull(br, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	frame := make([]byte, n)
	if _, err := io.ReadFull(br, frame); err != nil {
		return nil, err
	}
	return frame, nil
}

// WriteFrame writes a length-prefixed binary frame.
func (r *Router) WriteFrame(h Handle, payload []byte) error {
	if len(payload) > 0xffffffff {
		return errors.New("capnet: frame payload too large")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := r.Write(h, header[:]); err != nil {
		return err
	}
	_, err := r.Write(h, payload)
	return err
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// Now returns the simulated time of the trial owning h.
func (r *Router) Now(h Handle) (time.Time, error) {
	ep, err := r.host.resolve(h)
	if err != nil {
		return time.Time{}, err
	}
	return ep.clock.nowTime(), nil
}

// Sleep blocks for simulated time d. It unblocks only when the host advances
// the trial clock; if the generation ends first it returns ErrStaleHandle.
func (r *Router) Sleep(h Handle, d time.Duration) error {
	ep, err := r.host.resolve(h)
	if err != nil {
		return err
	}
	tm := ep.clock.after(d, func() {})
	trialDone := r.host.trialDone(ep.gen)
	select {
	case <-tm.done:
	case <-trialDone:
	}
	if _, err := r.host.resolve(h); err != nil {
		return err
	}
	return nil
}

// Timer is a handle to a fake, simulated timer.
type Timer struct {
	t *fakeTimer
}

// AfterFunc schedules fn after simulated time d. The callback runs only when
// the host advances the clock and is dropped if the generation ends first.
func (r *Router) AfterFunc(h Handle, d time.Duration, fn func()) (*Timer, error) {
	ep, err := r.host.resolve(h)
	if err != nil {
		return nil, err
	}
	return &Timer{t: ep.clock.after(d, fn)}, nil
}

// Stop prevents the timer from firing. It reports whether the call stopped a
// pending timer.
func (tm *Timer) Stop() bool {
	return tm.t.stop()
}
