package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

// fakeCipher is the test-supplied cryptographic backend. Ciphertext is the
// plaintext followed by an HMAC tag over the authenticated header and
// plaintext. Distinct key names fail authentication, modeling a record opened
// under the wrong key generation.
type fakeCipher struct {
	name string

	mu        sync.Mutex
	sealCalls int
	openCalls int
}

func newFakeCipher(name string) *fakeCipher {
	return &fakeCipher{name: name}
}

func (c *fakeCipher) Overhead() int { return sha256.Size }

func (c *fakeCipher) mac(plaintext, header []byte) []byte {
	mac := hmac.New(sha256.New, []byte(c.name))
	mac.Write(header)
	mac.Write(plaintext)
	return mac.Sum(nil)
}

func (c *fakeCipher) Seal(plaintext, header []byte) ([]byte, error) {
	c.mu.Lock()
	c.sealCalls++
	c.mu.Unlock()

	out := make([]byte, 0, len(plaintext)+sha256.Size)
	out = append(out, plaintext...)
	out = append(out, c.mac(plaintext, header)...)
	return out, nil
}

func (c *fakeCipher) Open(ciphertext, header []byte) ([]byte, error) {
	c.mu.Lock()
	c.openCalls++
	c.mu.Unlock()

	if len(ciphertext) < sha256.Size {
		return nil, fmt.Errorf("%w: short ciphertext", ErrAuthenticationFailed)
	}
	plaintext := ciphertext[:len(ciphertext)-sha256.Size]
	tag := ciphertext[len(ciphertext)-sha256.Size:]
	if !hmac.Equal(tag, c.mac(plaintext, header)) {
		return nil, fmt.Errorf("%w: tag mismatch", ErrAuthenticationFailed)
	}
	return append([]byte(nil), plaintext...), nil
}

func (c *fakeCipher) seals() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sealCalls
}

// bufferPipe is an in-memory blocking transport built on a channel. Its
// buffered bytes can be drained and re-injected, which tests need to hold a
// late record back.
type bufferPipe struct {
	mu     sync.Mutex
	ch     chan byte
	done   chan struct{}
	closed bool
}

func newBufferPipe() *bufferPipe {
	return &bufferPipe{ch: make(chan byte, 1<<20), done: make(chan struct{})}
}

func (p *bufferPipe) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	// Block until at least one byte is available, then drain up to len(b)
	// bytes of contiguous data. Because every frame is written before it is
	// read and bytes pass through a single ordered channel, the returned
	// bytes are always a contiguous prefix of the stream.
	select {
	case v := <-p.ch:
		b[0] = v
	case <-p.done:
		select {
		case v := <-p.ch:
			b[0] = v
		default:
			return 0, io.EOF
		}
	}
	n := 1
	for n < len(b) {
		select {
		case v := <-p.ch:
			b[n] = v
			n++
		case <-p.done:
			return n, io.EOF
		default:
			return n, nil
		}
	}
	return n, nil
}

func (p *bufferPipe) Write(b []byte) (int, error) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	for i, v := range b {
		select {
		case p.ch <- v:
		case <-p.done:
			return i, io.ErrClosedPipe
		}
	}
	return len(b), nil
}

func (p *bufferPipe) len() int { return len(p.ch) }

func (p *bufferPipe) drain() []byte {
	out := make([]byte, 0, len(p.ch))
	for {
		select {
		case v := <-p.ch:
			out = append(out, v)
		default:
			return out
		}
	}
}

func (p *bufferPipe) inject(b []byte) {
	for _, v := range b {
		p.ch <- v
	}
}

func (p *bufferPipe) closePipe() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.done)
	}
}

// splitTransport uses one pipe per direction, like a full-duplex socket.
type splitTransport struct {
	w io.Writer
	r io.Reader
}

func (t *splitTransport) Read(p []byte) (int, error)  { return t.r.Read(p) }
func (t *splitTransport) Write(p []byte) (int, error) { return t.w.Write(p) }

// limitedWriter caps every Write at limit bytes to force partial control
// frame writes; the session must resume the identical sealed bytes.
type limitedWriter struct {
	inner *bufferPipe
	limit int
}

func (w *limitedWriter) Write(b []byte) (int, error) {
	if len(b) > w.limit {
		b = b[:w.limit]
	}
	return w.inner.Write(b)
}

// collector runs a ReceiveMessage loop and preserves application-message
// order. Key-switch markers are consumed inside ReceiveMessage.
type collector struct {
	session  *Session
	closeFn  func()
	mu       sync.Mutex
	messages [][]byte
	err      error
	done     chan struct{}
}

func startCollector(s *Session, closeFn func()) *collector {
	c := &collector{session: s, closeFn: closeFn, done: make(chan struct{})}
	go c.loop()
	return c
}

func (c *collector) loop() {
	defer close(c.done)
	for {
		msg, err := c.session.ReceiveMessage()
		if err != nil {
			c.mu.Lock()
			c.err = err
			c.mu.Unlock()
			return
		}
		c.mu.Lock()
		c.messages = append(c.messages, msg)
		c.mu.Unlock()
	}
}

func (c *collector) snapshot() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.messages))
	copy(out, c.messages)
	return out
}

func (c *collector) waitCount(want int) [][]byte {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(c.snapshot()) >= want {
			return c.snapshot()
		}
		time.Sleep(time.Millisecond)
	}
	return c.snapshot()
}

func (c *collector) stop() error {
	_ = c.session.Close()
	if c.closeFn != nil {
		c.closeFn()
	}
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	if errors.Is(c.err, ErrClosed) || errors.Is(c.err, io.EOF) {
		return nil
	}
	return c.err
}

// testPair wires two sessions over two independent bufferPipes.
type testPair struct {
	a, b     *Session
	ab, ba   *bufferPipe
	collectB *collector
	collectA *collector
}

func newTestPair(t *testing.T, opts ...Option) *testPair {
	t.Helper()
	ab := newBufferPipe()
	ba := newBufferPipe()
	a := NewSession(&splitTransport{w: ab, r: ba},
		newFakeCipher("AB-gen0"), newFakeCipher("BA-gen0"), opts...)
	b := NewSession(&splitTransport{w: ba, r: ab},
		newFakeCipher("BA-gen0"), newFakeCipher("AB-gen0"), opts...)
	return &testPair{
		a: a, b: b, ab: ab, ba: ba,
		collectB: startCollector(b, ab.closePipe),
		collectA: startCollector(a, ba.closePipe),
	}
}

func (p *testPair) close(t *testing.T) {
	t.Helper()
	if err := p.collectB.stop(); err != nil {
		t.Fatalf("collector B: %v", err)
	}
	if err := p.collectA.stop(); err != nil {
		t.Fatalf("collector A: %v", err)
	}
}

// provisionRekey installs matching send/receive keys for one rotation.
func provisionRekey(t *testing.T, sender, receiver *Session, gen uint64, label string) {
	t.Helper()
	key := newFakeCipher(label)
	if err := sender.InstallSendRekey(gen, key); err != nil {
		t.Fatalf("InstallSendRekey: %v", err)
	}
	if err := receiver.InstallReceiveRekey(gen, newFakeCipher(label)); err != nil {
		t.Fatalf("InstallReceiveRekey: %v", err)
	}
}

func stringsOf(records [][]byte) []string {
	out := make([]string, len(records))
	for i, record := range records {
		out[i] = string(record)
	}
	return out
}

func TestBasicRoundTripAndBoundaries(t *testing.T) {
	// Smoke check: the in-memory transport preserves an ordered byte stream
	// across the blocking partial reads that io.ReadFull issues.
	pipe := newBufferPipe()
	payload := append([]byte{0x4e, 0x53, 0x45, 0x43, 2}, make([]byte, 52)...)
	if _, err := pipe.Write(payload); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, frameHeaderSize)
	if _, err := io.ReadFull(pipe, header); err != nil {
		t.Fatalf("header ReadFull: %v", err)
	}
	if !bytes.Equal(header, payload[:frameHeaderSize]) {
		t.Fatalf("header reassembly mismatch:\n got % x\nwant % x",
			header, payload[:frameHeaderSize])
	}

	pair := newTestPair(t)
	defer pair.close(t)

	messages := [][]byte{
		[]byte(""),
		[]byte("one"),
		bytes.Repeat([]byte("x"), 4096),
		[]byte("three"),
	}
	for _, msg := range messages {
		if err := pair.a.SendMessage(msg); err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	}
	got := pair.collectB.waitCount(len(messages))
	if len(got) != len(messages) {
		t.Fatalf("got %d records, want %d", len(got), len(messages))
	}
	for i, want := range messages {
		if !bytes.Equal(got[i], want) {
			t.Fatalf("record %d boundary broken: got %d bytes, want %d",
				i, len(got[i]), len(want))
		}
	}
}

func TestSimultaneousRekey(t *testing.T) {
	pair := newTestPair(t)
	defer pair.close(t)

	// Both directions negotiate at the same time; the two rotations are
	// independent and must not interfere.
	provisionRekey(t, pair.a, pair.b, 1, "AB-gen1")
	provisionRekey(t, pair.b, pair.a, 1, "BA-gen1")

	if pair.a.SendGeneration() != 0 || pair.b.ReceiveGeneration() != 0 {
		t.Fatal("provisioning a new key must not change the active generation")
	}
	if pair.b.ReceivePhase() != RekeyPending || pair.a.ReceivePhase() != RekeyPending {
		t.Fatal("receiver must wait for the authenticated marker")
	}

	// Messages enqueued before the marker stay on generation zero; messages
	// after the marker bind to generation one.
	if err := pair.a.SendMessage([]byte("a-before")); err != nil {
		t.Fatal(err)
	}
	if err := pair.b.SendMessage([]byte("b-before")); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() { defer wg.Done(); errs <- pair.a.SwitchSend() }()
	go func() { defer wg.Done(); errs <- pair.b.SwitchSend() }()
	wg.Wait()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("SwitchSend: %v", err)
		}
	}
	if pair.a.SendGeneration() != 1 || pair.b.SendGeneration() != 1 {
		t.Fatal("send directions did not commit after markers were written")
	}

	if err := pair.a.SendMessage([]byte("a-after")); err != nil {
		t.Fatal(err)
	}
	if err := pair.b.SendMessage([]byte("b-after")); err != nil {
		t.Fatal(err)
	}

	wantB := []string{"a-before", "a-after"}
	wantA := []string{"b-before", "b-after"}
	if got := stringsOf(pair.collectB.waitCount(2)); !equalStrings(got, wantB) {
		t.Fatalf("B application order = %v, want %v (marker stays out of app stream)",
			got, wantB)
	}
	if got := stringsOf(pair.collectA.waitCount(2)); !equalStrings(got, wantA) {
		t.Fatalf("A application order = %v, want %v", got, wantA)
	}

	waitReceiveGeneration(t, pair.a, 1)
	waitReceiveGeneration(t, pair.b, 1)
	if pair.a.ReceivePhase() != RekeyStable || pair.b.ReceivePhase() != RekeyStable {
		t.Fatal("receive direction must return to stable after authenticated commit")
	}

	// A second rotation proves the machine is reusable and keeps the previous
	// generation only as a deprecated, non-active key.
	provisionRekey(t, pair.a, pair.b, 2, "AB-gen2")
	if err := pair.a.SwitchSend(); err != nil {
		t.Fatal(err)
	}
	if err := pair.a.SendMessage([]byte("gen2-message")); err != nil {
		t.Fatal(err)
	}
	if got := pair.collectB.waitCount(3); string(got[2]) != "gen2-message" {
		t.Fatalf("gen2 message = %q", got[2])
	}
	waitReceiveGeneration(t, pair.b, 2)
	gen, ok := pair.b.DeprecatedReceiveGeneration()
	if !ok || gen != 1 {
		t.Fatalf("deprecated generation = %d/%v, want 1/true", gen, ok)
	}
	if pair.b.ReceiveGeneration() != 2 {
		t.Fatal("deprecated key must never become active again")
	}
}

func waitReceiveGeneration(t *testing.T, s *Session, want uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.ReceiveGeneration() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("receive generation never reached %d (current %d)",
		want, s.ReceiveGeneration())
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestPartialControlFrameWrite(t *testing.T) {
	// Replace the A->B pipe with a writer that accepts only a few bytes per
	// call, forcing the key-switch marker to be fragmented.
	ab := newBufferPipe()
	ba := newBufferPipe()
	fragmented := &limitedWriter{inner: ab, limit: 7}

	sendKey0 := newFakeCipher("A-send-gen0")
	a := NewSession(&splitTransport{w: fragmented, r: ba},
		sendKey0, newFakeCipher("BA-gen0"))
	recvKey0 := newFakeCipher("A-send-gen0")
	b := NewSession(&splitTransport{w: ba, r: ab},
		newFakeCipher("BA-gen0"), recvKey0)
	collectB := startCollector(b, ab.closePipe)
	defer func() {
		if err := collectB.stop(); err != nil {
			t.Fatalf("collector: %v", err)
		}
	}()

	sendKey1 := newFakeCipher("AB-gen1-send")
	recvKey1 := newFakeCipher("AB-gen1-send")
	if err := a.InstallSendRekey(1, sendKey1); err != nil {
		t.Fatal(err)
	}
	if err := b.InstallReceiveRekey(1, recvKey1); err != nil {
		t.Fatal(err)
	}

	before := sendKey1.seals()
	if err := a.SwitchSend(); err != nil {
		t.Fatalf("SwitchSend over short writes: %v", err)
	}
	if seals := sendKey1.seals() - before; seals != 1 {
		t.Fatalf("marker sealed %d times across partial writes, want exactly 1", seals)
	}
	if a.SendGeneration() != 1 || a.SendPhase() != RekeyStable {
		t.Fatal("send direction must commit only after the full marker is written")
	}

	waitReceiveGeneration(t, b, 1)

	payload := []byte("post-marker-payload")
	if err := a.SendMessage(payload); err != nil {
		t.Fatal(err)
	}
	got := collectB.waitCount(1)
	if len(got) != 1 || !bytes.Equal(got[0], payload) {
		t.Fatalf("payload after fragmented marker = %q", got)
	}
}

func TestLateOldGenerationMessage(t *testing.T) {
	abWire := newBufferPipe()
	ba := newBufferPipe()
	capture := newCaptureWriter()
	ab := &teePipe{wire: abWire, tee: capture}
	a := NewSession(&splitTransport{w: ab, r: ba},
		newFakeCipher("AB-gen0"), newFakeCipher("BA-gen0"),
		WithMaxLateOldRecords(2))
	b := NewSession(&splitTransport{w: ba, r: abWire},
		newFakeCipher("BA-gen0"), newFakeCipher("AB-gen0"),
		WithMaxLateOldRecords(2))

	// Seal three old-generation records before the rotation, then hold their
	// bytes outside the session while the rotation commits. They become
	// legitimately late records; the window of two accepts the first pair and
	// rejects the third without ever reactivating the deprecated key.
	var held [][]byte
	for _, name := range []string{"late-1", "late-2", "late-3"} {
		if err := a.SendMessage([]byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	captured := capture.bytes()
	for len(held) < 3 {
		frame, rest, err := splitFirstFrame(captured)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, frame)
		captured = rest
	}
	// Remove the three old records from the wire so B only sees the marker
	// and new-generation traffic until we re-inject the late frames.
	abWire.drain()

	provisionRekey(t, a, b, 1, "AB-late-gen1")
	if err := a.SwitchSend(); err != nil {
		t.Fatal(err)
	}

	// New-generation traffic is accepted first...
	if err := a.SendMessage([]byte("fresh-new")); err != nil {
		t.Fatal(err)
	}
	// ReceiveMessage consumes the key-switch marker internally and then
	// returns the first application record following it.
	got, err := b.ReceiveMessage()
	if err != nil {
		t.Fatalf("new-generation record: %v", err)
	}
	if string(got) != "fresh-new" {
		t.Fatalf("new-generation record = %q", got)
	}
	if b.ReceiveGeneration() != 1 {
		t.Fatal("receive direction did not commit after authenticated marker")
	}

	// ...and the delayed old-generation record still opens with the
	// deprecated key, consuming one late slot rather than reactivating it.
	abWire.inject(held[0])
	got, err = b.ReceiveMessage()
	if err != nil || string(got) != "late-1" {
		t.Fatalf("first late old record = %q, err %v", got, err)
	}
	if b.ReceiveGeneration() != 1 {
		t.Fatal("opening a late record must not roll the active key back")
	}

	// The second late record consumes the remaining window slot.
	abWire.inject(held[1])
	got, err = b.ReceiveMessage()
	if err != nil || string(got) != "late-2" {
		t.Fatalf("second late record = %q, err %v", got, err)
	}

	// The third late record exceeds the window and must fail.
	abWire.inject(held[2])
	if _, err := b.ReceiveMessage(); !errors.Is(err, ErrLateWindowExceeded) {
		t.Fatalf("third late record: %v", err)
	}
	if b.ReceiveGeneration() != 1 {
		t.Fatal("failed late record must not change the active generation")
	}
}

// captureWriter records every byte written through it, for tests that need to
// hold selected records back while the session keeps writing.
type captureWriter struct {
	mu  sync.Mutex
	buf []byte
}

func newCaptureWriter() *captureWriter { return &captureWriter{} }

func (c *captureWriter) Write(b []byte) (int, error) {
	c.mu.Lock()
	c.buf = append(c.buf, b...)
	c.mu.Unlock()
	return len(b), nil
}

func (c *captureWriter) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf...)
}

// teePipe writes to the real pipe while mirroring into a capture sink.
type teePipe struct {
	wire *bufferPipe
	tee  *captureWriter
}

func (p *teePipe) Write(b []byte) (int, error) {
	n, err := p.wire.Write(b)
	if n > 0 {
		_, _ = p.tee.Write(b[:n])
	}
	return n, err
}

func (p *teePipe) Read(b []byte) (int, error) { return p.wire.Read(b) }

// splitFirstFrame separates one length-prefixed frame from the remainder.
func splitFirstFrame(data []byte) (frame, rest []byte, err error) {
	if len(data) < frameHeaderSize {
		return nil, nil, io.EOF
	}
	parsed, err := decodeHeader(data[:frameHeaderSize])
	if err != nil {
		return nil, nil, err
	}
	total := frameHeaderSize + int(parsed.length)
	if len(data) < total {
		return nil, nil, io.EOF
	}
	return append([]byte(nil), data[:total]...), data[total:], nil
}

func TestMarkerAuthenticationFailureKeepsKeys(t *testing.T) {
	ab := newBufferPipe()
	ba := newBufferPipe()
	a := NewSession(&splitTransport{w: ab, r: ba},
		newFakeCipher("AB-gen0"), newFakeCipher("BA-gen0"))
	b := NewSession(&splitTransport{w: ba, r: ab},
		newFakeCipher("BA-gen0"), newFakeCipher("AB-gen0"))

	// Capture a valid old-generation record before the failed rotation.
	if err := a.SendMessage([]byte("still-old")); err != nil {
		t.Fatal(err)
	}
	oldFrame, _, err := splitFirstFrame(ab.drain())
	if err != nil {
		t.Fatal(err)
	}

	// Provision mismatched keys: the marker cannot authenticate, so neither
	// generation may change and the old key must stay usable.
	sendKey := newFakeCipher("AB-wrong-send")
	recvKey := newFakeCipher("AB-wrong-receive")
	if err := a.InstallSendRekey(1, sendKey); err != nil {
		t.Fatal(err)
	}
	if err := b.InstallReceiveRekey(1, recvKey); err != nil {
		t.Fatal(err)
	}
	if err := a.SwitchSend(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReceiveMessage(); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatal(err)
	}
	if b.ReceiveGeneration() != 0 || b.ReceivePhase() != RekeyPending {
		t.Fatal("failed marker must not commit the receive direction")
	}
	if a.SendGeneration() != 1 {
		t.Fatal("sender still commits its own marker: receiving failure must not roll it back")
	}

	// The old generation remains usable for a record still sealed under it,
	// proving the failed marker never deprecated or replaced the old key.
	ab.inject(oldFrame)
	got, err := b.ReceiveMessage()
	if err != nil {
		t.Fatalf("old key unusable after failed marker: %v", err)
	}
	if string(got) != "still-old" {
		t.Fatalf("record after failed marker = %q", got)
	}

	// The failed marker must not be applied as a committed rotation: an
	// unknown generation-one record is rejected rather than downgraded.
	if gen, ok := b.DeprecatedReceiveGeneration(); ok {
		t.Fatalf("unexpected deprecated generation %d after failed marker", gen)
	}

	// A record sealed by the new send key stays unreadable on B and must not
	// cause any rollback.
	if err := a.SendMessage([]byte("new-key-data")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReceiveMessage(); !errors.Is(err, ErrUnknownGeneration) {
		t.Fatalf("new-key record after failed marker: %v", err)
	}
	if b.ReceiveGeneration() != 0 {
		t.Fatal("rejecting an unknown generation must not change the active key")
	}
}

func TestReplayRejected(t *testing.T) {
	ab := newBufferPipe()
	ba := newBufferPipe()
	a := NewSession(&splitTransport{w: ab, r: ba},
		newFakeCipher("AB-gen0"), newFakeCipher("BA-gen0"))
	b := NewSession(&splitTransport{w: ba, r: ab},
		newFakeCipher("BA-gen0"), newFakeCipher("AB-gen0"))

	if err := a.SendMessage([]byte("once")); err != nil {
		t.Fatal(err)
	}
	frame := ab.drain()
	ab.inject(frame)
	got, err := b.ReceiveMessage()
	if err != nil || string(got) != "once" {
		t.Fatalf("first delivery = %q, err %v", got, err)
	}

	ab.inject(append([]byte(nil), frame...))
	if _, err := b.ReceiveMessage(); !errors.Is(err, ErrReplay) {
		t.Fatal(err)
	}
}

func TestDeterministicVersionPerRecord(t *testing.T) {
	abWire := newBufferPipe()
	ba := newBufferPipe()
	capture := newCaptureWriter()
	sendKey0 := newFakeCipher("AB-det-gen0")
	sendKey1 := newFakeCipher("AB-det-gen1")
	a := NewSession(&splitTransport{w: &teePipe{wire: abWire, tee: capture}, r: ba},
		sendKey0, newFakeCipher("BA-gen0"))
	b := NewSession(&splitTransport{w: ba, r: abWire},
		newFakeCipher("BA-gen0"), newFakeCipher("AB-det-gen0"))

	// Two records before the marker must each be sealed once under gen 0.
	if err := a.SendMessage([]byte("old-0")); err != nil {
		t.Fatal(err)
	}
	if err := a.SendMessage([]byte("old-1")); err != nil {
		t.Fatal(err)
	}
	if err := a.InstallSendRekey(1, sendKey1); err != nil {
		t.Fatal(err)
	}
	if err := b.InstallReceiveRekey(1, newFakeCipher("AB-det-gen1")); err != nil {
		t.Fatal(err)
	}
	sealsBeforeMarker := sendKey0.seals()
	if err := a.SwitchSend(); err != nil {
		t.Fatal(err)
	}
	// The marker must be sealed by the new key exactly once, never by the old.
	if seals := sendKey1.seals(); seals != 1 {
		t.Fatalf("new key sealed marker %d times, want 1", seals)
	}
	// Records after the marker are gen 1; the old key seals nothing further.
	if err := a.SendMessage([]byte("new-0")); err != nil {
		t.Fatal(err)
	}

	// Decode every captured frame and assert one immutable generation and
	// sequence per record, with the marker cleanly splitting the versions.
	data := capture.bytes()
	var seen []frameHeader
	for len(data) > 0 {
		frame, rest, err := splitFirstFrame(data)
		if err != nil {
			t.Fatal(err)
		}
		header, err := decodeHeader(frame[:frameHeaderSize])
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, header)
		data = rest
	}

	want := []struct {
		typ RecordType
		gen uint64
		seq uint64
	}{
		{RecordApplication, 0, 0},
		{RecordApplication, 0, 1},
		{RecordKeySwitch, 1, 0},
		{RecordApplication, 1, 1},
	}
	if len(seen) != len(want) {
		t.Fatalf("captured %d frames, want %d", len(seen), len(want))
	}
	for i, w := range want {
		if seen[i].typ != w.typ || seen[i].generation != w.gen || seen[i].sequence != w.seq {
			t.Fatalf("frame %d = type %d gen %d seq %d, want type %d gen %d seq %d",
				i, seen[i].typ, seen[i].generation, seen[i].sequence,
				w.typ, w.gen, w.seq)
		}
	}
	if got := sendKey0.seals(); got != sealsBeforeMarker {
		t.Fatalf("old key sealed %d times after marker, want frozen at %d",
			got, sealsBeforeMarker)
	}

	// The receiver must still deliver the three application records in order.
	for _, wantMsg := range []string{"old-0", "old-1", "new-0"} {
		msg, err := b.ReceiveMessage()
		if err != nil {
			t.Fatalf("receive %q: %v", wantMsg, err)
		}
		if string(msg) != wantMsg {
			t.Fatalf("got %q, want %q", msg, wantMsg)
		}
	}
}
