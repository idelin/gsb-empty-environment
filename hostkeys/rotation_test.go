package hostkeys_test

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"example.org/netsec/q0814/hostkeys"
)

type event struct {
	name string
	err  error
}

type harness struct {
	t        *testing.T
	srv      *testServer
	rotator  *hostkeys.Rotator
	client   *ssh.Client
	hostFile string
	events   chan event
}

func newHarness(t *testing.T, initialSigner ssh.Signer, behavior *serverBehavior) *harness {
	t.Helper()

	hostFile := filepath.Join(t.TempDir(), "known_hosts")
	srv := startServer(t, initialSigner, behavior)
	addr := srv.listener.Addr().String()

	seedLine := knownhosts.Line([]string{addr}, initialSigner.PublicKey())
	if err := os.WriteFile(hostFile, []byte(seedLine+"\n"), 0o600); err != nil {
		t.Fatalf("seed known_hosts: %v", err)
	}

	events := make(chan event, 16)
	rotator, err := hostkeys.New(hostkeys.Options{
		KnownHostsPath: hostFile,
		OnEvent: func(name string, err error) {
			events <- event{name: name, err: err}
		},
	})
	if err != nil {
		t.Fatalf("new rotator: %v", err)
	}

	callback, err := knownhosts.New(hostFile)
	if err != nil {
		t.Fatalf("knownhosts callback: %v", err)
	}
	cfg := &ssh.ClientConfig{
		HostKeyCallback: rotator.WrapCallback(callback),
		Timeout:         5 * time.Second,
	}

	client, err := rotator.Dial("tcp", srv.listener.Addr().String(), cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	return &harness{
		t:        t,
		srv:      srv,
		rotator:  rotator,
		client:   client,
		hostFile: hostFile,
		events:   events,
	}
}

// trigger releases the server, which then sends the hostkeys notification.
func (h *harness) trigger() {
	h.srv.ready <- struct{}{}
}

func (h *harness) waitEvent(want string) event {
	h.t.Helper()
	select {
	case ev := <-h.events:
		if ev.name != want {
			h.t.Fatalf("want event %q, got %q (err=%v)", want, ev.name, ev.err)
		}
		return ev
	case <-time.After(5 * time.Second):
		h.t.Fatalf("timed out waiting for event %q", want)
	}
	return event{}
}

func (h *harness) knownHostBlobs() map[string]bool {
	h.t.Helper()
	data, err := os.ReadFile(h.hostFile)
	if err != nil {
		h.t.Fatalf("read known_hosts: %v", err)
	}
	out := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "@") {
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			h.t.Fatalf("parse known_hosts line %q: %v", line, err)
		}
		out[string(key.Marshal())] = true
	}
	return out
}

func TestSuccessfulRotationAddsNewProvedKey(t *testing.T) {
	signerA, keyA := newTestSigner(t)
	signerB, keyB := newTestSigner(t)

	behavior := &serverBehavior{
		advertise: [][]byte{keyA.Marshal(), keyB.Marshal()},
		signers: map[string]ssh.Signer{
			string(keyA.Marshal()): signerA,
			string(keyB.Marshal()): signerB,
		},
		tamperIndex: -1,
	}

	h := newHarness(t, signerA, behavior)
	before := h.knownHostBlobs()
	h.trigger()

	h.waitEvent("notify")
	h.waitEvent("applied")

	after := h.knownHostBlobs()
	if !after[string(keyA.Marshal())] {
		t.Error("authenticated key A missing after rotation")
	}
	if !after[string(keyB.Marshal())] {
		t.Error("proved new key B missing after rotation")
	}
	if len(before) != 1 || len(after) != 2 {
		t.Fatalf("want 1 -> 2 entries, got %d -> %d", len(before), len(after))
	}
}

func TestStaleEntryRemovedOnlyAfterSuccessfulProof(t *testing.T) {
	signerA, keyA := newTestSigner(t)
	signerB, keyB := newTestSigner(t)

	// The batch lists only B; A may be removed only because every proof
	// succeeds.
	behavior := &serverBehavior{
		advertise:   [][]byte{keyB.Marshal()},
		signers:     map[string]ssh.Signer{string(keyB.Marshal()): signerB},
		tamperIndex: -1,
	}

	h := newHarness(t, signerA, behavior)
	h.trigger()
	h.waitEvent("notify")
	h.waitEvent("applied")

	blobs := h.knownHostBlobs()
	if blobs[string(keyA.Marshal())] {
		t.Error("stale key A should have been removed after a proved rotation")
	}
	if !blobs[string(keyB.Marshal())] {
		t.Error("proved replacement key B missing")
	}
}

func TestNoProofWritesNothing(t *testing.T) {
	signerA, keyA := newTestSigner(t)
	_, keyB := newTestSigner(t)

	behavior := &serverBehavior{
		advertise: [][]byte{keyA.Marshal(), keyB.Marshal()},
		signers: map[string]ssh.Signer{
			string(keyA.Marshal()): signerA,
		},
		noProve:     true,
		tamperIndex: -1,
	}

	h := newHarness(t, signerA, behavior)
	before := h.knownHostBlobs()

	h.trigger()
	h.waitEvent("notify")
	ev := h.waitEvent("abort")
	if !errors.Is(ev.err, hostkeys.ErrProveFailed) {
		t.Fatalf("want ErrProveFailed, got %v", ev.err)
	}

	after := h.knownHostBlobs()
	if len(after) != len(before) {
		t.Fatalf("known_hosts mutated without proof: before=%d after=%d", len(before), len(after))
	}
	for blob := range before {
		if !after[blob] {
			t.Fatalf("entry %x lost despite failed proof", blob)
		}
	}
}

func TestForeignSessionSignatureRejected(t *testing.T) {
	signerA, keyA := newTestSigner(t)
	signerB, keyB := newTestSigner(t)

	behavior := &serverBehavior{
		advertise: [][]byte{keyA.Marshal(), keyB.Marshal()},
		signers: map[string]ssh.Signer{
			string(keyA.Marshal()): signerA,
			string(keyB.Marshal()): signerB,
		},
		foreignSession: true,
		tamperIndex:    -1,
	}

	h := newHarness(t, signerA, behavior)
	before := h.knownHostBlobs()

	h.trigger()
	h.waitEvent("notify")
	ev := h.waitEvent("abort")
	if !errors.Is(ev.err, hostkeys.ErrProofRejected) {
		t.Fatalf("want ErrProofRejected, got %v", ev.err)
	}

	after := h.knownHostBlobs()
	if len(after) != len(before) {
		t.Fatalf("known_hosts mutated on foreign-session proof: before=%d after=%d", len(before), len(after))
	}
}

func TestPartialFailureAbortsWholeBatch(t *testing.T) {
	signerA, keyA := newTestSigner(t)
	signerB, keyB := newTestSigner(t)
	signerC, keyC := newTestSigner(t)

	behavior := &serverBehavior{
		advertise: [][]byte{keyA.Marshal(), keyB.Marshal(), keyC.Marshal()},
		signers: map[string]ssh.Signer{
			string(keyA.Marshal()): signerA,
			string(keyB.Marshal()): signerB,
			string(keyC.Marshal()): signerC,
		},
		// A and B verify, C carries garbage: nothing may be written.
		tamperIndex: 2,
	}

	h := newHarness(t, signerA, behavior)
	before := h.knownHostBlobs()

	h.trigger()
	h.waitEvent("notify")
	ev := h.waitEvent("abort")
	if !errors.Is(ev.err, hostkeys.ErrProofRejected) {
		t.Fatalf("want ErrProofRejected, got %v", ev.err)
	}

	after := h.knownHostBlobs()
	if len(after) != len(before) {
		t.Fatalf("partial failure changed known_hosts: before=%d after=%d", len(before), len(after))
	}
	if after[string(keyB.Marshal())] || after[string(keyC.Marshal())] {
		t.Fatal("keys were pinned despite a failed proof in the same batch")
	}
}

func TestNotificationIgnoredWithoutKnownHostEntry(t *testing.T) {
	signerA, keyA := newTestSigner(t)
	signerB, keyB := newTestSigner(t)

	behavior := &serverBehavior{
		advertise: [][]byte{keyA.Marshal(), keyB.Marshal()},
		signers: map[string]ssh.Signer{
			string(keyA.Marshal()): signerA,
			string(keyB.Marshal()): signerB,
		},
		tamperIndex: -1,
	}

	h := newHarness(t, signerA, behavior)

	// Remove the seeded entry so the authenticated host is unknown.
	if err := os.WriteFile(h.hostFile, []byte("# emptied\n"), 0o600); err != nil {
		t.Fatalf("empty known_hosts: %v", err)
	}

	h.trigger()
	h.waitEvent("notify")
	ev := h.waitEvent("ignored")
	if !errors.Is(ev.err, hostkeys.ErrNoKnownHostEntry) {
		t.Fatalf("want ErrNoKnownHostEntry, got %v", ev.err)
	}

	blobs := h.knownHostBlobs()
	if len(blobs) != 0 {
		t.Fatalf("unknown host must not learn keys, got %d entries", len(blobs))
	}
}

func TestMalformedNotificationAborts(t *testing.T) {
	signerA, keyA := newTestSigner(t)

	behavior := &serverBehavior{
		advertise:   [][]byte{{0x00, 0x01, 0x02}},
		signers:     map[string]ssh.Signer{},
		tamperIndex: -1,
	}

	h := newHarness(t, signerA, behavior)
	before := h.knownHostBlobs()

	h.trigger()
	h.waitEvent("notify")
	ev := h.waitEvent("abort")
	if !errors.Is(ev.err, hostkeys.ErrMalformedNotification) {
		t.Fatalf("want ErrMalformedNotification, got %v", ev.err)
	}

	after := h.knownHostBlobs()
	if len(after) != len(before) {
		t.Fatalf("malformed notification changed known_hosts: before=%d after=%d", len(before), len(after))
	}
}
