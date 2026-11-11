package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"testing"
	"time"
)

const testTargetPath = "cli/app-1.0.0.tar.gz"

type testEnv struct {
	t       *testing.T
	now     time.Time
	signers map[string]*Signer
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	aliases := []string{"ra", "rb", "rc", "rd", "ts1", "ss1", "tg1", "ts2", "ss2", "tg2"}
	env := &testEnv{
		t:       t,
		now:     time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC),
		signers: make(map[string]*Signer, len(aliases)),
	}
	for _, alias := range aliases {
		signer, err := GenerateSigner(alias)
		if err != nil {
			t.Fatalf("generate signer %s: %v", alias, err)
		}
		env.signers[alias] = signer
	}
	return env
}

func (e *testEnv) clock() Clock {
	return func() time.Time { return e.now }
}

func (e *testEnv) signer(alias string) *Signer {
	signer, ok := e.signers[alias]
	if !ok {
		e.t.Fatalf("unknown signer %q", alias)
	}
	return signer
}

func (e *testEnv) publicKeys(aliases ...string) map[string]PublicKey {
	keys := make(map[string]PublicKey, len(aliases))
	for _, alias := range aliases {
		keys[alias] = e.signer(alias).PublicKey()
	}
	return keys
}

func role(threshold int, aliases ...string) RoleDef {
	return RoleDef{Threshold: threshold, KeyAliases: aliases}
}

func (e *testEnv) rootV1() (*Envelope, *RootFile) {
	root := &RootFile{
		Type:    typeRoot,
		Version: 1,
		Expires: e.now.Add(365 * 24 * time.Hour),
		Keys:    e.publicKeys("ra", "rb", "ts1", "ss1", "tg1"),
		Roles: map[string]RoleDef{
			RoleRoot:      role(2, "ra", "rb"),
			RoleTimestamp: role(1, "ts1"),
			RoleSnapshot:  role(1, "ss1"),
			RoleTargets:   role(1, "tg1"),
		},
	}
	envelope := e.sign(root, e.signer("ra"), e.signer("rb"))
	return envelope, root
}

// rootV2 rotates every role key. The returned envelope is signed by both
// generations so it can satisfy the old and the new root thresholds.
func (e *testEnv) rootV2() (*Envelope, *RootFile) {
	root := &RootFile{
		Type:    typeRoot,
		Version: 2,
		Expires: e.now.Add(2 * 365 * 24 * time.Hour),
		Keys:    e.publicKeys("rc", "rd", "ts2", "ss2", "tg2"),
		Roles: map[string]RoleDef{
			RoleRoot:      role(2, "rc", "rd"),
			RoleTimestamp: role(1, "ts2"),
			RoleSnapshot:  role(1, "ss2"),
			RoleTargets:   role(1, "tg2"),
		},
	}
	envelope := e.sign(root,
		e.signer("ra"), e.signer("rb"),
		e.signer("rc"), e.signer("rd"),
	)
	return envelope, root
}

func (e *testEnv) sign(doc any, signers ...*Signer) *Envelope {
	signed := make([]Signature, 0, len(signers))
	// EnvelopeFromSigned marshals doc exactly once; sign those exact bytes.
	payload := mustJSON(e.t, doc)
	for _, signer := range signers {
		signed = append(signed, signer.Sign(payload))
	}
	envelope, err := EnvelopeFromSigned(doc, signed)
	if err != nil {
		e.t.Fatalf("build envelope: %v", err)
	}
	return envelope
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := jsonMarshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func envelopeMetaRef(version int64, envelope *Envelope) MetaRef {
	raw := envelope.Bytes()
	sum256 := sha256.Sum256(raw)
	sum512 := sha512.Sum512(raw)
	return MetaRef{
		Version: version,
		Length:  int64(len(raw)),
		Hashes: map[HashAlgorithm]HexBytes{
			SHA256: sum256[:],
			SHA512: sum512[:],
		},
	}
}

func payloadHashes(t *testing.T, payload []byte) (int64, map[HashAlgorithm]HexBytes) {
	t.Helper()
	sum256 := sha256.Sum256(payload)
	sum512 := sha512.Sum512(payload)
	return int64(len(payload)), map[HashAlgorithm]HexBytes{
		SHA256: sum256[:],
		SHA512: sum512[:],
	}
}

// refreshBundle assures a fully valid, mutually consistent bundle for the
// supplied root generation.
func (e *testEnv) refreshBundle(
	root *RootFile,
	rootEnvelope *Envelope,
	tsVersion, snapVersion, targetVersion int64,
	payload []byte,
) Bundle {
	targets := &TargetFile{
		Type:    typeTargets,
		Version: targetVersion,
		Expires: e.now.Add(180 * 24 * time.Hour),
	}
	targets.Length, targets.Hashes = payloadHashes(e.t, payload)
	targetsEnvelope := e.sign(targets, e.signer(root.Roles[RoleTargets].KeyAliases[0]))

	snapshot := &SnapshotFile{
		Type:    typeSnapshot,
		Version: snapVersion,
		Expires: e.now.Add(180 * 24 * time.Hour),
		Meta: map[string]MetaRef{
			testTargetPath: envelopeMetaRef(targetVersion, targetsEnvelope),
		},
	}
	snapshotEnvelope := e.sign(snapshot, e.signer(root.Roles[RoleSnapshot].KeyAliases[0]))

	timestamp := &TimestampFile{
		Type:    typeTimestamp,
		Version: tsVersion,
		Expires: e.now.Add(30 * 24 * time.Hour),
		Meta:    envelopeMetaRef(snapVersion, snapshotEnvelope),
	}
	timestampEnvelope := e.sign(timestamp, e.signer(root.Roles[RoleTimestamp].KeyAliases[0]))

	return Bundle{
		Root:      rootEnvelope,
		Timestamp: timestampEnvelope,
		Snapshot:  snapshotEnvelope,
		Targets:   map[string]*Envelope{testTargetPath: targetsEnvelope},
	}
}

func (e *testEnv) bootstrapClient(ctx context.Context, store Store) *Client {
	e.t.Helper()
	client := NewClient(store).WithClock(e.clock())
	rootEnvelope, _ := e.rootV1()
	if err := client.Bootstrap(ctx, rootEnvelope); err != nil {
		e.t.Fatalf("bootstrap: %v", err)
	}
	return client
}

func assertErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error wrapping %v, got nil", target)
	}
	if !errorIs(err, target) {
		t.Fatalf("expected error wrapping %v, got %v", target, err)
	}
}

func assertEnvelopeRoundTrips(t *testing.T, envelope *Envelope) {
	t.Helper()
	parsed, err := ParseEnvelope(envelope.Bytes())
	if err != nil {
		t.Fatalf("re-parse envelope: %v", err)
	}
	if !bytes.Equal(parsed.Bytes(), envelope.Bytes()) {
		t.Fatalf("envelope did not round-trip canonically")
	}
}
