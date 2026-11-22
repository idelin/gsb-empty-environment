package refresh_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"math/big"
	"sort"

	"prescreen.local/ddd-9449-20261122-v2/refresh"
)

type testKey struct{ raw ed25519.PublicKey }

func (k testKey) Equals(other refresh.PublicKey) bool {
	o, ok := other.(testKey)
	return ok && k.raw.Equal(o.raw)
}

func (k testKey) Marshal() ([]byte, error) { return append([]byte("K:"), k.raw...), nil }

// memAuth is an in-memory authenticator: one Ed25519 key per canonical
// custodian, plus an alias map so several participant IDs share one key.
type memAuth struct {
	canonical map[string]string
	pub       map[string]ed25519.PublicKey
	priv      map[string]ed25519.PrivateKey
}

func newMemAuth(canonical []string, aliases map[string]string) *memAuth {
	a := &memAuth{
		canonical: map[string]string{},
		pub:       map[string]ed25519.PublicKey{},
		priv:      map[string]ed25519.PrivateKey{},
	}
	for _, id := range canonical {
		pub, priv, _ := ed25519.GenerateKey(rand.Reader)
		a.canonical[id] = id
		a.pub[id] = pub
		a.priv[id] = priv
	}
	for alias, canon := range aliases {
		a.canonical[alias] = canon
	}
	return a
}

func (a *memAuth) Canonical(id string) (string, refresh.PublicKey, error) {
	canon, ok := a.canonical[id]
	if !ok {
		return "", nil, errUnknownID
	}
	return canon, testKey{raw: a.pub[canon]}, nil
}

func (a *memAuth) Sign(id string, msg []byte) ([]byte, error) {
	canon := a.canonical[id]
	priv, ok := a.priv[canon]
	if !ok {
		return nil, errUnknownID
	}
	return ed25519.Sign(priv, msg), nil
}

func (a *memAuth) Verify(canonicalID string, msg, sig []byte) bool {
	pub, ok := a.pub[canonicalID]
	if !ok {
		return false
	}
	return ed25519.Verify(pub, msg, sig)
}

// harness holds one fully wired round environment.
type harness struct {
	field      *refresh.Field
	group      refresh.Group
	auth       *memAuth
	engine     *refresh.Engine
	senders    []string
	recipients []string
	threshold  int
	round      uint64
	// polys[sender] are that sender's refresh coefficients, low degree first,
	// with a zero constant term and fewer than k terms.
	polys  map[string][]*big.Int
	digest []byte
}

func newHarness(t testingT, group refresh.Group, field *refresh.Field, auth *memAuth,
	senders, recipients []string, threshold int, round uint64) *harness {
	t.Helper()
	engine, err := refresh.NewEngine(field, group, auth)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	h := &harness{
		field: field, group: group, auth: auth, engine: engine,
		senders:    append([]string(nil), senders...),
		recipients: append([]string(nil), recipients...),
		threshold:  threshold, round: round,
		polys: map[string][]*big.Int{},
	}
	h.rollPolynomials(t)
	h.digest = h.transcriptDigest(t)
	return h
}

func (h *harness) rollPolynomials(t testingT) {
	for _, sender := range h.senders {
		coeffs := make([]*big.Int, h.threshold)
		coeffs[0] = big.NewInt(0)
		for i := 1; i < h.threshold; i++ {
			v, err := rand.Int(rand.Reader, h.field.P())
			if err != nil {
				t.Fatalf("rand: %v", err)
			}
			coeffs[i] = v
		}
		h.polys[sender] = coeffs
	}
}

func (h *harness) commitment(t testingT, sender string) refresh.Commitment {
	t.Helper()
	terms := make([]refresh.Point, len(h.polys[sender]))
	for i, c := range h.polys[sender] {
		terms[i] = h.group.ScalarBaseMul(c)
	}
	return refresh.Commitment{Round: h.round, SenderID: sender, Terms: terms}
}

func (h *harness) allCommitments(t testingT) []refresh.Commitment {
	senders := append([]string(nil), h.senders...)
	sort.Strings(senders)
	out := make([]refresh.Commitment, 0, len(senders))
	for _, s := range senders {
		out = append(out, h.commitment(t, s))
	}
	return out
}

func (h *harness) transcriptDigest(t testingT) []byte {
	t.Helper()
	d, err := refresh.TranscriptDigest(h.round, h.allCommitments(t))
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	return d
}

func (h *harness) coord(t testingT, recipient string) *big.Int {
	t.Helper()
	canon, _, err := h.auth.Canonical(recipient)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	x, err := refresh.RecipientCoordinate(h.field, canon)
	if err != nil {
		t.Fatalf("coordinate: %v", err)
	}
	return x
}

func (h *harness) delta(t testingT, sender, recipient string) *big.Int {
	return h.field.Eval(h.polys[sender], h.coord(t, recipient))
}

func (h *harness) increment(t testingT, sender, recipient string, digest []byte) refresh.RecordedIncrement {
	t.Helper()
	inc := refresh.RecordedIncrement{
		Round:            h.round,
		SenderID:         sender,
		RecipientID:      recipient,
		Delta:            h.delta(t, sender, recipient),
		TranscriptDigest: digest,
	}
	if err := refresh.SignIncrement(h.auth, &inc); err != nil {
		t.Fatalf("sign increment: %v", err)
	}
	return inc
}

func (h *harness) complaint(t testingT, sender, recipient, reason string, digest []byte) refresh.RecordedComplaint {
	t.Helper()
	c := refresh.RecordedComplaint{
		Round:            h.round,
		SenderID:         sender,
		RecipientID:      recipient,
		Reason:           reason,
		TranscriptDigest: digest,
	}
	if err := refresh.SignComplaint(h.auth, &c); err != nil {
		t.Fatalf("sign complaint: %v", err)
	}
	return c
}

func (h *harness) encodedCommitments(t testingT) []refresh.EncodedCommitment {
	out := h.allCommitments(t)
	enc := make([]refresh.EncodedCommitment, 0, len(out))
	for _, c := range out {
		terms := make([][]byte, len(c.Terms))
		for i, pt := range c.Terms {
			b, err := pt.Marshal()
			if err != nil {
				t.Fatalf("marshal point: %v", err)
			}
			terms[i] = b
		}
		enc = append(enc, refresh.EncodedCommitment{Round: c.Round, SenderID: c.SenderID, Terms: terms})
	}
	return enc
}

func (h *harness) healthyBundle(t testingT) refresh.EvidenceBundle {
	b := refresh.EvidenceBundle{Round: h.round, Commitments: h.encodedCommitments(t)}
	for _, sender := range h.senders {
		for _, recipient := range h.recipients {
			b.Increments = append(b.Increments, h.increment(t, sender, recipient, h.digest))
		}
	}
	return b
}

func (h *harness) healthyViews() []refresh.RecipientView {
	senders := append([]string(nil), h.senders...)
	sort.Strings(senders)
	views := make([]refresh.RecipientView, 0, len(h.recipients))
	for _, r := range h.recipients {
		views = append(views, refresh.RecipientView{
			RecipientID: r, SenderSet: append([]string(nil), senders...),
			Digest: h.digest, AcceptedSenders: senders,
		})
	}
	return views
}

// oldMasterCommitment returns a master commitment whose constant term is g*s0.
// Since refresh polynomials have zero constant, the new summed master keeps
// the identical constant term.
func (h *harness) oldMasterCommitment(secret *big.Int) []refresh.Point {
	terms := make([]refresh.Point, h.threshold)
	terms[0] = h.group.ScalarBaseMul(secret)
	for i := 1; i < len(terms); i++ {
		terms[i] = h.group.Identity()
	}
	return terms
}

// expectedConstant is the persisted master commitment's constant term g*s.
func (h *harness) expectedConstant(secret *big.Int) refresh.Point {
	return h.group.ScalarBaseMul(secret)
}

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
	Errorf(format string, args ...any)
}
