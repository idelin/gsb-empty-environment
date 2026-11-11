package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Output describes a single build artifact: a path and the digest of its
// content.
type Output struct {
	Path   string
	Digest string
}

// Proof is a build attestation produced by a builder. It binds the digest of
// the input source tree, the builder identity, the full set of build outputs
// and the target environment the build is valid for. The signature covers the
// canonical encoding of all four fields.
type Proof struct {
	InputTreeDigest string
	BuilderID       string
	Outputs         []Output
	TargetEnv       string
	Signature       []byte
}

// Plan is a release plan: the set of files to publish into a target
// environment.
type Plan struct {
	ID          string
	Environment string
	Files       []Output
}

// Ticket is a consumption ticket. It binds one proof to exactly one release
// plan digest, so files approved under it cannot be recombined into another
// plan.
type Ticket struct {
	ProofHash  string
	PlanDigest string
	Files      []Output
}

var (
	ErrUnknownBuilder      = errors.New("netsec: unknown builder identity")
	ErrBadSignature        = errors.New("netsec: invalid proof signature")
	ErrEnvironmentMismatch = errors.New("netsec: proof target environment does not match plan environment")
	ErrOutputNotCovered    = errors.New("netsec: plan file not covered by proof outputs")
	ErrCrossPlanReuse      = errors.New("netsec: proof already consumed by a different release plan")
	ErrTicketPlanMismatch  = errors.New("netsec: ticket does not match release plan")
	ErrUnknownProof        = errors.New("netsec: ticket references a proof that was never consumed")
)

func writeField(buf *bytes.Buffer, s string) {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(s)))
	buf.Write(lenBuf[:])
	buf.WriteString(s)
}

func sortedOutputs(outputs []Output) []Output {
	sorted := make([]Output, len(outputs))
	copy(sorted, outputs)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Path != sorted[j].Path {
			return sorted[i].Path < sorted[j].Path
		}
		return sorted[i].Digest < sorted[j].Digest
	})
	return sorted
}

// canonicalProof returns the deterministic encoding of the attested fields.
func canonicalProof(p *Proof) []byte {
	var buf bytes.Buffer
	writeField(&buf, p.InputTreeDigest)
	writeField(&buf, p.BuilderID)
	writeField(&buf, p.TargetEnv)
	for _, out := range sortedOutputs(p.Outputs) {
		writeField(&buf, out.Path)
		writeField(&buf, out.Digest)
	}
	return buf.Bytes()
}

// SignProof signs the proof's canonical encoding with the builder key.
func SignProof(p *Proof, priv ed25519.PrivateKey) {
	p.Signature = ed25519.Sign(priv, canonicalProof(p))
}

// Hash returns the content hash of the proof, used as its unique identity.
func (p *Proof) Hash() string {
	sum := sha256.Sum256(canonicalProof(p))
	return hex.EncodeToString(sum[:])
}

// Digest returns the release plan digest: a hash over the environment and the
// full ordered file set. Any change to the plan (environment, file list, file
// content) changes the digest.
func (p *Plan) Digest() string {
	var buf bytes.Buffer
	writeField(&buf, p.Environment)
	for _, f := range sortedOutputs(p.Files) {
		writeField(&buf, f.Path)
		writeField(&buf, f.Digest)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

// Verifier consumes build proofs on behalf of the release host. It is safe
// for concurrent use.
type Verifier struct {
	mu       sync.Mutex
	builders map[string]ed25519.PublicKey
	// bindings records, per proof hash, the release plan digest the proof was
	// consumed for. A proof may be consumed any number of times for the same
	// plan, but never for two different plans.
	bindings map[string]string
}

func NewVerifier() *Verifier {
	return &Verifier{
		builders: make(map[string]ed25519.PublicKey),
		bindings: make(map[string]string),
	}
}

// RegisterBuilder registers a trusted builder identity and its public key.
func (v *Verifier) RegisterBuilder(id string, pub ed25519.PublicKey) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.builders[id] = pub
}

// Consume validates the proof against the plan and, on success, records the
// proof-to-plan binding and returns a consumption ticket. Every file in the
// plan must be covered by the proof outputs; a partially covered plan is
// rejected. Re-consuming the same proof for the same plan is idempotent, so
// multiple files of one legitimate release may share a single proof.
func (v *Verifier) Consume(proof *Proof, plan *Plan) (*Ticket, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	pub, ok := v.builders[proof.BuilderID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownBuilder, proof.BuilderID)
	}
	if !ed25519.Verify(pub, canonicalProof(proof), proof.Signature) {
		return nil, ErrBadSignature
	}
	if proof.TargetEnv != plan.Environment {
		return nil, fmt.Errorf("%w: proof=%q plan=%q", ErrEnvironmentMismatch, proof.TargetEnv, plan.Environment)
	}

	covered := make(map[Output]struct{}, len(proof.Outputs))
	for _, out := range proof.Outputs {
		covered[out] = struct{}{}
	}
	for _, f := range plan.Files {
		if _, ok := covered[f]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrOutputNotCovered, f.Path)
		}
	}

	proofHash := proof.Hash()
	planDigest := plan.Digest()
	if bound, seen := v.bindings[proofHash]; seen && bound != planDigest {
		return nil, fmt.Errorf("%w: proof %s already bound to plan %s", ErrCrossPlanReuse, proofHash, bound)
	}
	v.bindings[proofHash] = planDigest

	return &Ticket{
		ProofHash:  proofHash,
		PlanDigest: planDigest,
		Files:      sortedOutputs(plan.Files),
	}, nil
}

// Approve checks that a ticket authorizes the given plan: the ticket must
// have been issued by this verifier and must bind to this exact plan digest.
// A ticket replayed against a different plan is rejected.
func (v *Verifier) Approve(ticket *Ticket, plan *Plan) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	bound, seen := v.bindings[ticket.ProofHash]
	if !seen {
		return fmt.Errorf("%w: %s", ErrUnknownProof, ticket.ProofHash)
	}
	if ticket.PlanDigest != plan.Digest() || bound != ticket.PlanDigest {
		return ErrTicketPlanMismatch
	}
	return nil
}
