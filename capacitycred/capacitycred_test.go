package capacitycred_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"testing"

	"prescreen.local/ps-47c6a59d98/capacitycred"
)

const (
	tenantA = capacitycred.TenantID("tenant-a")
	tenantB = capacitycred.TenantID("tenant-b")
	nodeA   = capacitycred.NodeID("node-a")
	nodeB   = capacitycred.NodeID("node-b")
	root    = capacitycred.CredID("root")
	childL  = capacitycred.CredID("child-left")
	childR  = capacitycred.CredID("child-right")
	evil    = capacitycred.CredID("evil-child")
)

type host struct {
	keys map[capacitycred.TenantID][]byte
}

func newHost() *host {
	return &host{keys: map[capacitycred.TenantID][]byte{
		tenantA: []byte("key-a"),
		tenantB: []byte("key-b"),
	}}
}

func (h *host) callbacks() capacitycred.Callbacks {
	return capacitycred.Callbacks{
		Hash: func(_ context.Context, data []byte) ([]byte, error) {
			sum := sha256.Sum256(data)
			return sum[:], nil
		},
		Verify: func(tenant capacitycred.TenantID, body, sig []byte) error {
			key, ok := h.keys[tenant]
			if !ok {
				return capacitycred.ErrBadSignature
			}
			mac := hmac.New(sha256.New, key)
			mac.Write(body)
			if !hmac.Equal(mac.Sum(nil), sig) {
				return capacitycred.ErrBadSignature
			}
			return nil
		},
	}
}

func (h *host) sign(tenant capacitycred.TenantID, body []byte) []byte {
	mac := hmac.New(sha256.New, h.keys[tenant])
	mac.Write(body)
	return mac.Sum(nil)
}

type eventBuilder struct {
	t     *testing.T
	h     *host
	cb    capacitycred.Callbacks
	nonce uint64
}

func newBuilder(t *testing.T, h *host) *eventBuilder {
	t.Helper()
	return &eventBuilder{t: t, h: h, cb: h.callbacks()}
}

func (b *eventBuilder) WithNonce(nonce uint64) *eventBuilder {
	b.nonce = nonce
	return b
}

func (b *eventBuilder) finish(e capacitycred.Event) capacitycred.Event {
	b.t.Helper()
	e.Nonce = b.nonce
	b.nonce = 0
	id, err := capacitycred.EventDigest(b.cb, e)
	if err != nil {
		b.t.Fatalf("event digest: %v", err)
	}
	e.EventID = id
	e.Signature = b.h.sign(e.Tenant, capacitycred.EventSigningBody(e))
	return e
}

func (b *eventBuilder) mint(tenant capacitycred.TenantID, id capacitycred.CredID, owner capacitycred.NodeID, total uint64, params capacitycred.Params) (capacitycred.Event, []byte) {
	commit, err := capacitycred.ParamsCommitment(b.cb, params)
	if err != nil {
		b.t.Fatalf("commitment: %v", err)
	}
	e := capacitycred.Event{
		Kind:             capacitycred.KindMint,
		Tenant:           tenant,
		CredID:           id,
		From:             owner,
		Range:            capacitycred.Interval{Low: 0, High: total},
		Total:            total,
		ParamsCommitment: commit,
	}
	return b.finish(e), commit
}

func (b *eventBuilder) split(tenant capacitycred.TenantID, parent capacitycred.CredID, rootID capacitycred.CredID, prev capacitycred.EventID, children ...capacitycred.ChildSpec) capacitycred.Event {
	e := capacitycred.Event{
		Kind:     capacitycred.KindSplit,
		Tenant:   tenant,
		CredID:   parent,
		ParentID: rootID,
		Children: children,
		Prev:     prev,
	}
	return b.finish(e)
}

func (b *eventBuilder) handoff(tenant capacitycred.TenantID, id, rootID capacitycred.CredID, from, to capacitycred.NodeID, prev capacitycred.EventID) capacitycred.Event {
	e := capacitycred.Event{
		Kind:     capacitycred.KindHandoff,
		Tenant:   tenant,
		CredID:   id,
		ParentID: rootID,
		From:     from,
		To:       to,
		Prev:     prev,
	}
	return b.finish(e)
}

func (b *eventBuilder) revoke(tenant capacitycred.TenantID, id, rootID capacitycred.CredID, prev capacitycred.EventID) capacitycred.Event {
	e := capacitycred.Event{
		Kind:     capacitycred.KindRevoke,
		Tenant:   tenant,
		CredID:   id,
		ParentID: rootID,
		Prev:     prev,
	}
	return b.finish(e)
}

func (b *eventBuilder) receipt(rootID capacitycred.CredID, tenant capacitycred.TenantID, tip capacitycred.EventID) capacitycred.Receipt {
	r := capacitycred.Receipt{RootCredID: rootID, Tenant: tenant, PrefixTip: tip}
	r.Signature = b.h.sign(tenant, capacitycred.ReceiptSigningBody(r))
	return r
}

func mustReceive(t *testing.T, e *capacitycred.Engine, ev capacitycred.Event, cb capacitycred.Callbacks) {
	t.Helper()
	if err := e.Receive(capacitycred.Envelope{Event: ev, Callbacks: cb}); err != nil {
		t.Fatalf("receive %s: %v", ev.Kind, err)
	}
}

func findCred(t *testing.T, snap *capacitycred.Snapshot, id capacitycred.CredID) capacitycred.CredView {
	t.Helper()
	for _, cred := range snap.Credentials {
		if cred.ID == id {
			return cred
		}
	}
	t.Fatalf("credential %s missing", id)
	return capacitycred.CredView{}
}

func assertIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want %v, got %v", target, err)
	}
}

func testParams() capacitycred.Params {
	return capacitycred.Params{SCost: 4, Iterations: 1, Parallel: 1, Salt: []byte("salt"), Password: []byte("pw"), Label: "batch"}
}

func newTestEngine(t *testing.T, h *host) (*capacitycred.Engine, *capacitycred.CountingAllocator) {
	t.Helper()
	alloc := capacitycred.NewCountingAllocator(1 << 20)
	engine, err := capacitycred.NewEngine(h.callbacks(), nodeA, alloc)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	return engine, alloc
}

func permutations(events []capacitycred.Event) [][]capacitycred.Event {
	if len(events) <= 1 {
		return [][]capacitycred.Event{append([]capacitycred.Event(nil), events...)}
	}
	var out [][]capacitycred.Event
	for idx, ev := range events {
		rest := append(append([]capacitycred.Event(nil), events[:idx]...), events[idx+1:]...)
		for _, tail := range permutations(rest) {
			out = append(out, append([]capacitycred.Event{ev}, tail...))
		}
	}
	return out
}

func envelopeSet(events []capacitycred.Event, cb capacitycred.Callbacks) []capacitycred.Envelope {
	out := make([]capacitycred.Envelope, 0, len(events))
	for _, ev := range events {
		out = append(out, capacitycred.Envelope{Event: ev, Callbacks: cb})
	}
	return out
}
