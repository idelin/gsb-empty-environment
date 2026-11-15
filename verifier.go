package netsec

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"sort"
	"time"
)

// MaxChainCertificates is the hard upper bound on the number of candidate
// certificates accepted by one verification call.
const MaxChainCertificates = 32

// Verifier holds host-fixed trust configuration. A Verifier is safe for
// concurrent use as long as its slices and clock are not mutated.
type Verifier struct {
	// Roots are the trust anchors. A root-issued certificate must name the
	// matching root ID and verify under that root's public key.
	Roots []TrustRoot

	// Revocations is consulted by the exact (IssuerID, Serial) pair.
	Revocations []Revocation

	// MaxCertificates may lower the hard limit of 32. Values above 32 or zero
	// use the hard limit.
	MaxCertificates int

	// Now supplies verification time. Nil means time.Now.
	Now func() time.Time
}

// Verify verifies certificates against roots at at. It returns the complete
// trusted delegation chain from the root-issued certificate through the leaf.
// The root itself is not included because it is host configuration, not an
// input certificate.
func Verify(
	certificates []Certificate,
	roots []TrustRoot,
	request Request,
	at time.Time,
	revocations []Revocation,
) ([]Certificate, error) {
	verifier := Verifier{
		Roots:       roots,
		Revocations: revocations,
	}
	return verifier.VerifyAt(certificates, request, at)
}

// Verify verifies certificates at the current time supplied by v.Now.
func (v *Verifier) Verify(certificates []Certificate, request Request) ([]Certificate, error) {
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	return v.VerifyAt(certificates, request, now())
}

// VerifyAt verifies certificates at the supplied instant.
func (v *Verifier) VerifyAt(certificates []Certificate, request Request, at time.Time) ([]Certificate, error) {
	limit := MaxChainCertificates
	if v.MaxCertificates > 0 && v.MaxCertificates < limit {
		limit = v.MaxCertificates
	}
	if len(certificates) > limit {
		return nil, fmt.Errorf("%w: got %d, limit %d", ErrTooManyCertificates, len(certificates), limit)
	}

	roots, err := normalizeTrustRoots(v.Roots)
	if err != nil {
		return nil, err
	}
	operation, resource, target, err := normalizeRequest(request)
	if err != nil {
		return nil, err
	}

	revoked := make(map[revocationKey]struct{}, len(v.Revocations))
	for _, entry := range v.Revocations {
		revoked[revocationKey{issuerID: entry.IssuerID, serial: entry.Serial}] = struct{}{}
	}

	pool := make([]parsedCertificate, len(certificates))
	for i, certificate := range certificates {
		pool[i] = parsePoolCertificate(certificate)
	}

	stateQueue := make([]chainState, 0, len(pool))
	queued := make(map[string]struct{})
	for _, root := range roots {
		for i := range pool {
			candidate := &pool[i]
			if candidate.err != nil {
				continue
			}
			if candidate.certificate.IssuerID != root.ID {
				continue
			}
			if !ed25519.Verify(root.PublicKey, candidate.message, candidate.certificate.Signature) {
				continue
			}
			if _, isRevoked := revoked[revocationKey{
				issuerID: candidate.certificate.IssuerID,
				serial:   candidate.certificate.Serial,
			}]; isRevoked {
				continue
			}
			if !certificateValidAt(candidate.normalized, at) {
				continue
			}

			state := chainState{
				index:       i,
				chain:       []int{i},
				operations:  candidate.normalized.operations,
				prefixes:    candidate.normalized.prefixes,
				remaining:   candidate.normalized.maxDepth,
				usedSubject: map[subjectKey]struct{}{subjectKeyOf(candidate.normalized): {}},
			}
			if supports(state.operations, state.prefixes, operation, resource) &&
				matchesTarget(candidate.normalized, target) {
				return chainFromIndices(pool, state.chain), nil
			}
			if key := state.key(); key != "" {
				if _, seen := queued[key]; !seen {
					queued[key] = struct{}{}
					stateQueue = append(stateQueue, state)
				}
			}
		}
	}

	for head := 0; head < len(stateQueue); head++ {
		state := stateQueue[head]
		if state.remaining == 0 {
			continue
		}

		parent := &pool[state.index]
		for i := range pool {
			child := &pool[i]
			if child.err != nil {
				continue
			}

			key := subjectKeyOf(child.normalized)
			if _, used := state.usedSubject[key]; used {
				continue
			}
			if !ed25519.Verify(
				parent.normalized.subjectPublicKey,
				child.message,
				child.certificate.Signature,
			) {
				continue
			}
			if _, isRevoked := revoked[revocationKey{
				issuerID: child.certificate.IssuerID,
				serial:   child.certificate.Serial,
			}]; isRevoked {
				continue
			}
			if !certificateValidAt(child.normalized, at) {
				continue
			}

			operations := intersectStringSets(state.operations, child.normalized.operations)
			prefixes := intersectPrefixes(state.prefixes, child.normalized.prefixes)
			if len(operations) == 0 || len(prefixes) == 0 {
				continue
			}

			remaining := child.normalized.maxDepth
			if state.remaining-1 < remaining {
				remaining = state.remaining - 1
			}

			next := chainState{
				index:       i,
				chain:       appendIndex(state.chain, i),
				operations:  operations,
				prefixes:    prefixes,
				remaining:   remaining,
				usedSubject: copySubjectSet(state.usedSubject),
			}
			next.usedSubject[key] = struct{}{}

			if supports(next.operations, next.prefixes, operation, resource) &&
				matchesTarget(child.normalized, target) {
				return chainFromIndices(pool, next.chain), nil
			}
			if next.remaining == 0 {
				continue
			}
			if key := next.key(); key != "" {
				if _, seen := queued[key]; !seen {
					queued[key] = struct{}{}
					stateQueue = append(stateQueue, next)
				}
			}
		}
	}

	return nil, ErrNoTrustedPath
}

type parsedCertificate struct {
	certificate Certificate
	normalized  normalizedCertificate
	message     []byte
	err         error
}

func parsePoolCertificate(certificate Certificate) parsedCertificate {
	normalized, err := normalizeCertificate(certificate, true)
	if err != nil {
		return parsedCertificate{certificate: certificate, err: err}
	}
	message, err := normalized.signingBytes()
	return parsedCertificate{
		certificate: certificate,
		normalized:  normalized,
		message:     message,
		err:         err,
	}
}

type subjectKey [ed25519.PublicKeySize]byte

func subjectKeyOf(certificate normalizedCertificate) subjectKey {
	var key subjectKey
	copy(key[:], certificate.subjectPublicKey)
	return key
}

type revocationKey struct {
	issuerID string
	serial   uint64
}

type chainState struct {
	index       int
	chain       []int
	operations  []string
	prefixes    []string
	remaining   uint32
	usedSubject map[subjectKey]struct{}
}

func (s chainState) key() string {
	keys := make([]subjectKey, 0, len(s.usedSubject))
	for key := range s.usedSubject {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return string(keys[i][:]) < string(keys[j][:])
	})

	key := fmt.Sprintf("%d/%d/%q/%q", s.index, s.remaining, s.operations, s.prefixes)
	for _, subject := range keys {
		key += "/" + string(subject[:])
	}
	return key
}

func appendIndex(chain []int, index int) []int {
	next := make([]int, len(chain)+1)
	copy(next, chain)
	next[len(chain)] = index
	return next
}

func copySubjectSet(source map[subjectKey]struct{}) map[subjectKey]struct{} {
	result := make(map[subjectKey]struct{}, len(source)+1)
	for key := range source {
		result[key] = struct{}{}
	}
	return result
}

func chainFromIndices(pool []parsedCertificate, indices []int) []Certificate {
	chain := make([]Certificate, len(indices))
	for i, index := range indices {
		chain[i] = pool[index].certificate
	}
	return chain
}

func certificateValidAt(certificate normalizedCertificate, at time.Time) bool {
	return !at.Before(certificate.notBefore) && at.Before(certificate.notAfter)
}

func supports(operations, prefixes []string, operation, resource string) bool {
	if len(operations) == 0 || len(prefixes) == 0 {
		return false
	}
	operationIndex := sort.SearchStrings(operations, operation)
	if operationIndex == len(operations) || operations[operationIndex] != operation {
		return false
	}
	for _, prefix := range prefixes {
		if pathHasPrefix(prefix, resource) {
			return true
		}
	}
	return false
}

func normalizeRequest(request Request) (string, string, ed25519.PublicKey, error) {
	if request.Operation == "" {
		return "", "", nil, fmt.Errorf("%w: operation is empty", ErrInvalidRequest)
	}
	resource, err := CanonicalResourcePath(request.ResourcePath)
	if err != nil {
		return "", "", nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if len(request.SubjectPublicKey) != 0 && len(request.SubjectPublicKey) != ed25519.PublicKeySize {
		return "", "", nil, fmt.Errorf("%w: target Ed25519 public key must be %d bytes", ErrInvalidRequest, ed25519.PublicKeySize)
	}
	target := append(ed25519.PublicKey(nil), request.SubjectPublicKey...)
	return request.Operation, resource, target, nil
}

func matchesTarget(certificate normalizedCertificate, target ed25519.PublicKey) bool {
	return len(target) == 0 || bytes.Equal(certificate.subjectPublicKey, target)
}

func normalizeTrustRoots(roots []TrustRoot) ([]TrustRoot, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("%w: no roots configured", ErrInvalidTrustRoot)
	}

	normalized := make([]TrustRoot, len(roots))
	keysByID := make(map[string]subjectKey, len(roots))
	for i, root := range roots {
		if root.ID == "" {
			return nil, fmt.Errorf("%w: root ID is empty", ErrInvalidTrustRoot)
		}
		if len(root.PublicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: root %q public key must be %d bytes", ErrInvalidTrustRoot, root.ID, ed25519.PublicKeySize)
		}
		publicKey := append(ed25519.PublicKey(nil), root.PublicKey...)
		var key subjectKey
		copy(key[:], publicKey)
		if previous, ok := keysByID[root.ID]; ok && previous != key {
			return nil, fmt.Errorf("%w: root ID %q has conflicting public keys", ErrInvalidTrustRoot, root.ID)
		}
		keysByID[root.ID] = key
		normalized[i] = TrustRoot{ID: root.ID, PublicKey: publicKey}
	}
	return normalized, nil
}
