package streamfilter

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

// Direction identifies one half of the proxied session.
type Direction int

const (
	// DirInput is local input flowing towards the remote side.
	DirInput Direction = iota
	// DirOutput is remote output flowing towards the local side.
	DirOutput
)

func (d Direction) String() string {
	if d == DirInput {
		return "input"
	}
	return "output"
}

// Recorder is the persistence layer for the filtered session. Record
// must not retain p. Proxy serializes Record calls, and per-direction
// ordering of the recorded bytes always matches the wire. A failing
// Record aborts the whole session (fail closed).
type Recorder interface {
	Record(dir Direction, p []byte) error
}

// RecorderFunc adapts a function to Recorder.
type RecorderFunc func(dir Direction, p []byte) error

// Record implements Recorder.
func (f RecorderFunc) Record(dir Direction, p []byte) error { return f(dir, p) }

// Endpoint is one side of the proxied session. If Out implements
// CloseWrite() error it is half-closed once the opposite direction
// reaches EOF. If any stream implements io.Closer it is closed when
// the session aborts or the context is cancelled.
type Endpoint struct {
	In  io.Reader
	Out io.Writer
}

type closeWriter interface{ CloseWrite() error }

// Proxy pumps a bidirectional session between a local and a remote
// endpoint, recording only what its Filter lets through. Forwarding
// never depends on the recorder: masked bytes still reach the remote
// side, and a slow recorder only applies backpressure to the pumps.
type Proxy struct {
	Filter   *Filter
	Recorder Recorder
	BufSize  int
}

// Run pumps both directions until both reach EOF, an unrecoverable
// error occurs, or ctx is cancelled. Half-close is propagated: when
// one direction ends, the other keeps flowing.
func (p *Proxy) Run(ctx context.Context, local, remote Endpoint) error {
	f := p.Filter
	if f == nil {
		f = NewFilter(0)
	}
	bufSize := p.BufSize
	if bufSize <= 0 {
		bufSize = 32 * 1024
	}

	var recMu sync.Mutex
	record := func(dir Direction, b []byte) error {
		if len(b) == 0 || p.Recorder == nil {
			return nil
		}
		recMu.Lock()
		defer recMu.Unlock()
		return p.Recorder.Record(dir, b)
	}

	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			closeIfPossible(local.In)
			closeIfPossible(local.Out)
			closeIfPossible(remote.In)
			closeIfPossible(remote.Out)
		})
	}

	var cancelled atomic.Bool
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			cancelled.Store(true)
			closeAll()
		case <-done:
		}
	}()

	errCh := make(chan error, 2)
	go func() {
		err := pump(local.In, remote.Out, bufSize,
			f.FilterInput,
			func(masked []byte) error { return record(DirInput, masked) })
		if err != nil {
			closeAll()
		}
		errCh <- err
	}()
	go func() {
		err := pump(remote.In, local.Out, bufSize,
			f.FilterOutput,
			func(masked []byte) error { return record(DirOutput, masked) })
		if err != nil {
			closeAll()
		}
		errCh <- err
	}()

	var firstErr error
	for i := 0; i < 2; i++ {
		err := <-errCh
		if err == nil {
			continue
		}
		if firstErr == nil || (isCloseErr(firstErr) && !isCloseErr(err)) {
			firstErr = err
		}
	}
	if cancelled.Load() {
		return ctx.Err()
	}
	return firstErr
}

// pump copies src to dst. The filter runs before each chunk is
// forwarded so its state is always ahead of anything the peer may
// answer; only the filtered bytes are offered to record. On src EOF
// it half-closes dst when supported.
func pump(src io.Reader, dst io.Writer, bufSize int, filter func([]byte) []byte, record func([]byte) error) error {
	buf := make([]byte, bufSize)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			masked := filter(chunk)
			if _, err := dst.Write(chunk); err != nil {
				return err
			}
			if err := record(masked); err != nil {
				return err
			}
		}
		if rerr != nil {
			if cw, ok := dst.(closeWriter); ok {
				cw.CloseWrite()
			}
			if rerr == io.EOF {
				return nil
			}
			return rerr
		}
	}
}

func closeIfPossible(v any) {
	if c, ok := v.(io.Closer); ok {
		c.Close()
	}
}

func isCloseErr(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed)
}
