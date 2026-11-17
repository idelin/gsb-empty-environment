package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"sort"
	"time"
)

// Request describes the access being authorized.
//
// SubjectPublicKey optionally selects the public key at which a chain must
// end. Leaf optionally selects an exact leaf certificate instead. If neither
// is supplied, the verifier returns the shortest supporting chain that ends at
// a certificate with no valid child in the pool.
type Request struct {
	Operation        string
	Resource         string
	At               time.Time
	SubjectPublicKey ed25519.PublicKey
	Leaf             *Certificate

	// Accepted spellings for callers that use action/path terminology.
	Action  string
	Path    string
	Time    time.Time
	Subject ed25519.PublicKey
}

// VerifyRequest is an alias for Request.
type VerifyRequest = Request

// VerificationRequest is an alias for Request.
type VerificationRequest = Request

// AccessRequest is an alias for Request.
type AccessRequest = Request

// TrustAnchor is a host-configured trust root. ID is the issuer identifier
// used by certificates. If ID is empty, the anchor accepts any issuer
// identifier, but a valid signature by PublicKey is still required.
type TrustAnchor struct {
	ID        string
	PublicKey ed25519.PublicKey

	// Accepted aliases for host configuration code.
	Name string
	Key  ed25519.PublicKey
}

func (a TrustAnchor) effectiveID() string {
	if a.ID != "" {
		return a.ID
	}
	return a.Name
}

func (a TrustAnchor) effectiveKey() ed25519.PublicKey {
	if len(a.PublicKey) == ed25519.PublicKeySize {
		return a.PublicKey
	}
	if len(a.Key) != 0 {
		return a.Key
	}
	return a.PublicKey
}

// Revocation identifies one certificate by the pair (issuer ID, serial
// number). Revoking a serial under one issuer does not revoke the same serial
// under another issuer.
type Revocation struct {
	IssuerID string
	Issuer   string
	Serial   uint64
}

func (r Revocation) effectiveIssuerID() string {
	if r.IssuerID != "" {
		return r.IssuerID
	}
	return r.Issuer
}

// Matches reports whether the revocation applies to cert.
func (r Revocation) Matches(cert Certificate) bool {
	return r.effectiveIssuerID() == cert.IssuerID && r.Serial == cert.Serial
}

// Policy is the effective authority of the returned chain.
type Policy struct {
	Operations               []string
	ResourcePrefixes         []string
	RemainingDelegationDepth uint32
}

// Result is a successfully verified delegation chain. Chain is ordered from
// leaf to trust anchor, matching Go's x509.Verify convention. The trust anchor
// itself is host configuration and is not a certificate in the chain.
type Result struct {
	Chain  []*Certificate
	RootID string
	Policy Policy
}

// Config configures a Verifier.
type Config struct {
	Roots           []TrustAnchor
	Revocations     []Revocation
	MaxCertificates int
}

// TrustStore is a convenient container for host-fixed roots.
type TrustStore struct {
	Roots           []TrustAnchor
	Revocations     []Revocation
	MaxCertificates int
}

// NewTrustStore constructs a trust store from fixed anchors.
func NewTrustStore(roots []TrustAnchor) *TrustStore {
	return &TrustStore{Roots: cloneAnchors(roots)}
}

// NewTrustStoreFromMap constructs a trust store keyed by issuer identifier.
func NewTrustStoreFromMap(roots map[string]ed25519.PublicKey) *TrustStore {
	anchors := make([]TrustAnchor, 0, len(roots))
	for id, publicKey := range roots {
		anchors = append(anchors, TrustAnchor{
			ID:        id,
			PublicKey: append(ed25519.PublicKey(nil), publicKey...),
		})
	}
	sort.Slice(anchors, func(i, j int) bool { return anchors[i].ID < anchors[j].ID })
	return &TrustStore{Roots: anchors}
}

func cloneAnchor(anchor TrustAnchor) TrustAnchor {
	anchor.PublicKey = append(ed25519.PublicKey(nil), anchor.PublicKey...)
	anchor.Key = append(ed25519.PublicKey(nil), anchor.Key...)
	return anchor
}

func cloneAnchors(anchors []TrustAnchor) []TrustAnchor {
	out := make([]TrustAnchor, len(anchors))
	for i, anchor := range anchors {
		out[i] = cloneAnchor(anchor)
	}
	return out
}

// Verifier verifies delegated access using a fixed set of trust anchors.
// The zero value has no roots and therefore accepts nothing.
type Verifier struct {
	Roots           []TrustAnchor
	Revocations     []Revocation
	MaxCertificates int
}

// NewVerifier builds a verifier from Config, TrustStore, []TrustAnchor,
// map[string]ed25519.PublicKey, []Revocation and int values, in any order.
// The flexible form keeps the trust configuration explicit while accepting
// the common map-based root representation.
func NewVerifier(args ...any) *Verifier {
	verifier := &Verifier{}
	for _, arg := range args {
		applyVerifierArg(verifier, arg)
	}
	return verifier
}

func applyVerifierArg(verifier *Verifier, arg any) {
	switch value := arg.(type) {
	case Config:
		verifier.Roots = append(verifier.Roots, cloneAnchors(value.Roots)...)
		verifier.Revocations = append(verifier.Revocations, value.Revocations...)
		if value.MaxCertificates > 0 {
			verifier.MaxCertificates = value.MaxCertificates
		}
	case *Config:
		if value != nil {
			applyVerifierArg(verifier, *value)
		}
	case TrustStore:
		verifier.Roots = append(verifier.Roots, cloneAnchors(value.Roots)...)
		verifier.Revocations = append(verifier.Revocations, value.Revocations...)
		if value.MaxCertificates > 0 {
			verifier.MaxCertificates = value.MaxCertificates
		}
	case *TrustStore:
		if value != nil {
			applyVerifierArg(verifier, *value)
		}
	case []TrustAnchor:
		verifier.Roots = append(verifier.Roots, cloneAnchors(value)...)
	case []*TrustAnchor:
		for _, anchor := range value {
			if anchor != nil {
				verifier.Roots = append(verifier.Roots, cloneAnchor(*anchor))
			}
		}
	case TrustAnchor:
		verifier.Roots = append(verifier.Roots, cloneAnchor(value))
	case *TrustAnchor:
		if value != nil {
			verifier.Roots = append(verifier.Roots, cloneAnchor(*value))
		}
	case map[string]ed25519.PublicKey:
		verifier.Roots = append(verifier.Roots, NewTrustStoreFromMap(value).Roots...)
	case map[string][]byte:
		anchors := make([]TrustAnchor, 0, len(value))
		for id, publicKey := range value {
			anchors = append(anchors, TrustAnchor{
				ID:        id,
				PublicKey: append(ed25519.PublicKey(nil), publicKey...),
			})
		}
		sort.Slice(anchors, func(i, j int) bool { return anchors[i].ID < anchors[j].ID })
		verifier.Roots = append(verifier.Roots, anchors...)
	case []Revocation:
		verifier.Revocations = append(verifier.Revocations, value...)
	case map[string]uint64:
		for issuerID, serial := range value {
			verifier.Revocations = append(verifier.Revocations,
				Revocation{IssuerID: issuerID, Serial: serial})
		}
	case map[Revocation]struct{}:
		for revocation := range value {
			verifier.Revocations = append(verifier.Revocations, revocation)
		}
	case []*Revocation:
		for _, revocation := range value {
			if revocation != nil {
				verifier.Revocations = append(verifier.Revocations, *revocation)
			}
		}
	case Revocation:
		verifier.Revocations = append(verifier.Revocations, value)
	case *Revocation:
		if value != nil {
			verifier.Revocations = append(verifier.Revocations, *value)
		}
	case int:
		if value > 0 {
			verifier.MaxCertificates = value
		}
	case *Verifier:
		if value != nil {
			verifier.Roots = append(verifier.Roots, cloneAnchors(value.Roots)...)
			verifier.Revocations = append(verifier.Revocations, value.Revocations...)
			if value.MaxCertificates > 0 {
				verifier.MaxCertificates = value.MaxCertificates
			}
		}
	case Verifier:
		verifier.Roots = append(verifier.Roots, cloneAnchors(value.Roots)...)
		verifier.Revocations = append(verifier.Revocations, value.Revocations...)
		if value.MaxCertificates > 0 {
			verifier.MaxCertificates = value.MaxCertificates
		}
	case []ed25519.PublicKey:
		for _, publicKey := range value {
			verifier.Roots = append(verifier.Roots, TrustAnchor{
				PublicKey: append(ed25519.PublicKey(nil), publicKey...),
			})
		}
	case ed25519.PublicKey:
		verifier.Roots = append(verifier.Roots, TrustAnchor{
			PublicKey: append(ed25519.PublicKey(nil), value...),
		})
	case []byte:
		if len(value) == ed25519.PublicKeySize {
			verifier.Roots = append(verifier.Roots, TrustAnchor{
				PublicKey: append(ed25519.PublicKey(nil), value...),
			})
		}
	}
}

// Verify accepts a Request, a certificate pool, and verifier configuration in
// any order. Accepted certificate forms are []Certificate, []*Certificate,
// *Certificate and a marshaled certificate []byte. A bare ed25519.PublicKey is
// the subject when no subject or leaf is supplied; otherwise it is a trust
// anchor.
func Verify(args ...any) ([]*Certificate, error) {
	result, err := VerifyResult(args...)
	if err != nil {
		return nil, err
	}
	return result.Chain, nil
}

// VerifyResult is Verify plus the effective policy and matched root ID.
func VerifyResult(args ...any) (*Result, error) {
	var (
		request        *Request
		certs          []*Certificate
		config         = &Verifier{}
		pendingAt      time.Time
		pendingSubject ed25519.PublicKey
		bareKeys       []ed25519.PublicKey
	)

	for _, arg := range args {
		switch value := arg.(type) {
		case Request:
			copy := value
			request = &copy
		case *Request:
			if value != nil {
				copy := *value
				request = &copy
			}
		case []Certificate:
			for i := range value {
				certs = append(certs, &value[i])
			}
		case []*Certificate:
			for _, cert := range value {
				if cert != nil {
					certs = append(certs, cert)
				}
			}
		case Certificate:
			cert := value
			certs = append(certs, &cert)
		case *Certificate:
			if value != nil {
				certs = append(certs, value)
			}
		case *Verifier:
			if value != nil {
				config.Roots = append(config.Roots, value.Roots...)
				config.Revocations = append(config.Revocations, value.Revocations...)
				if value.MaxCertificates > 0 {
					config.MaxCertificates = value.MaxCertificates
				}
			}
		case time.Time:
			pendingAt = value
		case ed25519.PublicKey:
			bareKeys = append(bareKeys, append(ed25519.PublicKey(nil), value...))
		case []byte:
			if cert, parseErr := ParseCertificate(value); parseErr == nil {
				certs = append(certs, cert)
			} else if len(value) == ed25519.PublicKeySize {
				bareKeys = append(bareKeys, append(ed25519.PublicKey(nil), value...))
			}
		default:
			applyVerifierArg(config, arg)
		}
	}

	if request == nil {
		request = &Request{}
	}
	if request.At.IsZero() && request.Time.IsZero() && !pendingAt.IsZero() {
		request.At = pendingAt
	}
	if len(request.effectiveSubject()) == 0 && len(pendingSubject) == 0 &&
		request.Leaf == nil && len(bareKeys) != 0 {
		pendingSubject = bareKeys[0]
		bareKeys = bareKeys[1:]
	}
	for _, publicKey := range bareKeys {
		applyVerifierArg(config, publicKey)
	}
	if len(request.effectiveSubject()) == 0 && len(pendingSubject) != 0 {
		request.SubjectPublicKey = pendingSubject
	}
	if request.Leaf != nil {
		// If the caller supplied an exact leaf pointer outside the pool (for
		// example, a pointer into a []Certificate argument), return that same
		// pointer from the successful chain.
		for i, cert := range certs {
			if cert != nil && cert.Equal(*request.Leaf) {
				certs[i] = request.Leaf
			}
		}
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("%w: empty certificate pool", ErrNoValidChain)
	}
	return config.verify(*request, certs)
}

// VerifyChain is an alias for Verify.
func VerifyChain(args ...any) ([]*Certificate, error) {
	return Verify(args...)
}

// VerifyCertificates is an alias for Verify.
func VerifyCertificates(args ...any) ([]*Certificate, error) {
	return Verify(args...)
}

// VerifyCertificateChain is an alias for Verify.
func VerifyCertificateChain(args ...any) ([]*Certificate, error) {
	return Verify(args...)
}

// Verify verifies a request against the verifier's configured roots. The
// arguments have the same accepted forms as the package-level Verify.
func (v *Verifier) Verify(args ...any) ([]*Certificate, error) {
	result, err := v.VerifyResult(args...)
	if err != nil {
		return nil, err
	}
	return result.Chain, nil
}

// VerifyResult verifies a request and returns the complete result.
func (v *Verifier) VerifyResult(args ...any) (*Result, error) {
	combined := make([]any, 0, len(args)+1)
	combined = append(combined, v)
	combined = append(combined, args...)
	return VerifyResult(combined...)
}

type certView struct {
	index      int
	cert       *Certificate
	publicKey  ed25519.PublicKey
	issuerID   string
	serial     uint64
	operations []string
	prefixes   []pathPrefix
	maxDepth   uint32
	notBefore  time.Time
	notAfter   time.Time
}

func newCertView(index int, cert *Certificate) (*certView, error) {
	if cert == nil {
		return nil, fmt.Errorf("%w: nil certificate", ErrInvalidCertificate)
	}
	if err := cert.validateFields(); err != nil {
		return nil, err
	}
	if len(cert.Signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: certificate %d has invalid signature length",
			ErrInvalidCertificate, index)
	}
	operations, err := normalizeOperations(cert.Operations)
	if err != nil {
		return nil, err
	}
	prefixes, err := normalizePrefixSet(cert.ResourcePrefixes)
	if err != nil {
		return nil, err
	}
	prefixes = reducePrefixes(prefixes)
	return &certView{
		index:      index,
		cert:       cert,
		publicKey:  append(ed25519.PublicKey(nil), cert.SubjectPublicKey...),
		issuerID:   cert.IssuerID,
		serial:     cert.Serial,
		operations: operations,
		prefixes:   prefixes,
		maxDepth:   cert.MaxDelegationDepth,
		notBefore:  cert.NotBefore,
		notAfter:   cert.NotAfter,
	}, nil
}

type normalizedRequest struct {
	operation string
	resource  pathPrefix
	at        time.Time
	subject   ed25519.PublicKey
	leaf      *Certificate
}

func normalizeRequest(request Request) (*normalizedRequest, error) {
	operation := request.Operation
	if operation == "" {
		operation = request.Action
	}
	if operation == "" {
		return nil, fmt.Errorf("%w: empty operation", ErrInvalidRequest)
	}
	resourceText := request.Resource
	if resourceText == "" {
		resourceText = request.Path
	}
	resource, err := parsePath(resourceText)
	if err != nil {
		return nil, err
	}
	at := request.At
	if at.IsZero() {
		at = request.Time
	}
	if at.IsZero() {
		at = time.Now()
	}
	subject := request.effectiveSubject()
	if len(subject) != 0 && len(subject) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: subject public key has length %d",
			ErrInvalidRequest, len(subject))
	}
	if request.Leaf != nil {
		if err := request.Leaf.validateFields(); err != nil {
			return nil, err
		}
		if len(subject) != 0 &&
			!bytes.Equal(subject, request.Leaf.SubjectPublicKey) {
			return nil, fmt.Errorf("%w: leaf subject does not match request subject",
				ErrInvalidRequest)
		}
	}
	return &normalizedRequest{
		operation: operation,
		resource:  resource,
		at:        at,
		subject:   append(ed25519.PublicKey(nil), subject...),
		leaf:      request.Leaf,
	}, nil
}

func (r Request) effectiveSubject() ed25519.PublicKey {
	if len(r.SubjectPublicKey) != 0 {
		return r.SubjectPublicKey
	}
	return r.Subject
}

type revKey struct {
	issuerID string
	serial   uint64
}

type chainState struct {
	path       []int
	visited    uint64
	operations []string
	prefixes   []pathPrefix
	remaining  uint32
	rootID     string
}

func (v *Verifier) verify(request Request, certs []*Certificate) (*Result, error) {
	req, err := normalizeRequest(request)
	if err != nil {
		return nil, err
	}

	limit := 32
	if v.MaxCertificates > 0 && v.MaxCertificates < limit {
		limit = v.MaxCertificates
	}
	if len(certs) > limit {
		return nil, fmt.Errorf("%w: got %d, limit %d",
			ErrTooManyCertificates, len(certs), limit)
	}
	if len(v.Roots) == 0 {
		return nil, fmt.Errorf("%w: no trust anchors", ErrNoValidChain)
	}

	views := make([]*certView, 0, len(certs))
	for i := range certs {
		view, err := newCertView(i, certs[i])
		if err != nil {
			continue
		}
		view.index = len(views)
		views = append(views, view)
	}
	if len(views) == 0 {
		return nil, fmt.Errorf("%w: no structurally valid certificates", ErrNoValidChain)
	}

	revoked := make(map[revKey]struct{}, len(v.Revocations))
	for _, revocation := range v.Revocations {
		revoked[revKey{issuerID: revocation.effectiveIssuerID(), serial: revocation.Serial}] = struct{}{}
	}
	isRevoked := func(view *certView) bool {
		_, ok := revoked[revKey{issuerID: view.issuerID, serial: view.serial}]
		return ok
	}

	targets, err := findTargets(req, views)
	if err != nil {
		return nil, err
	}

	rootMatches := make([][]string, len(views))
	for i, view := range views {
		if isRevoked(view) || !validAt(view, req.at) {
			continue
		}
		rootMatches[i] = matchingRoots(v.Roots, view)
	}

	links := make([][]int, len(views))
	for parentIndex, parent := range views {
		for childIndex, child := range views {
			if parentIndex == childIndex || isRevoked(child) || !validAt(child, req.at) {
				continue
			}
			if !issuerIDMatchesKey(child.issuerID, parent.publicKey) {
				continue
			}
			if child.cert.VerifySignature(parent.publicKey) != nil {
				continue
			}
			links[parentIndex] = append(links[parentIndex], childIndex)
		}
	}
	if targets == nil {
		// With no explicit subject or leaf, return a complete chain ending at
		// a certificate that has no valid child in the pool.
		targets = make(map[int]bool)
		for index := range views {
			if len(links[index]) == 0 {
				targets[index] = true
			}
		}
		if len(targets) == 0 {
			// Every certificate has a structural child. A cycle cannot be
			// traversed, so allow any acyclic prefix that supports the
			// request rather than rejecting the whole pool.
			for index := range views {
				targets[index] = true
			}
		}
	}

	type queuedState struct {
		state chainState
	}
	var queue []queuedState
	seen := make(map[string]struct{})

	for index, view := range views {
		if len(rootMatches[index]) == 0 || isRevoked(view) || !validAt(view, req.at) {
			continue
		}
		if !containsOperation(view.operations, req.operation) {
			continue
		}
		if !anyPrefixMatches(view.prefixes, req.resource) {
			continue
		}
		for _, rootID := range rootMatches[index] {
			state := chainState{
				path:       []int{index},
				visited:    uint64(1) << uint(index),
				operations: append([]string(nil), view.operations...),
				prefixes:   append([]pathPrefix(nil), view.prefixes...),
				remaining:  view.maxDepth,
				rootID:     rootID,
			}
			key := chainStateKey(state)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			queue = append(queue, queuedState{state: state})
		}
	}

	for head := 0; head < len(queue); head++ {
		current := queue[head].state
		last := current.path[len(current.path)-1]
		if targets[last] {
			return buildResult(current, views), nil
		}
		if current.remaining == 0 {
			continue
		}
		for _, childIndex := range links[last] {
			bit := uint64(1) << uint(childIndex)
			if current.visited&bit != 0 || subjectRepeated(current.path, childIndex, views) {
				continue
			}
			child := views[childIndex]
			operations := intersectOperations(current.operations, child.operations)
			if !containsOperation(operations, req.operation) {
				continue
			}
			prefixes := intersectPrefixes(current.prefixes, child.prefixes)
			if !anyPrefixMatches(prefixes, req.resource) {
				continue
			}
			// The parent's remaining budget bounds the child's declared
			// depth; a child can never increase authority.
			remaining := current.remaining - 1
			if child.maxDepth < remaining {
				remaining = child.maxDepth
			}
			path := append(append([]int(nil), current.path...), childIndex)
			state := chainState{
				path:       path,
				visited:    current.visited | bit,
				operations: operations,
				prefixes:   prefixes,
				remaining:  remaining,
				rootID:     current.rootID,
			}
			key := chainStateKey(state)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			queue = append(queue, queuedState{state: state})
		}
	}

	return nil, fmt.Errorf("%w: no single acyclic chain supports %q on %q",
		ErrNoValidChain, req.operation, req.resource.text)
}

func findTargets(req *normalizedRequest, views []*certView) (map[int]bool, error) {
	if req.leaf != nil {
		targets := make(map[int]bool)
		for _, view := range views {
			if view.cert.Equal(*req.leaf) {
				targets[view.index] = true
			}
		}
		if len(targets) == 0 {
			return nil, fmt.Errorf("%w: supplied leaf is not in the certificate pool",
				ErrNoValidChain)
		}
		return targets, nil
	}
	if len(req.subject) == 0 {
		return nil, nil
	}
	targets := make(map[int]bool)
	for _, view := range views {
		if bytes.Equal(view.publicKey, req.subject) {
			targets[view.index] = true
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("%w: no certificate for requested subject", ErrNoValidChain)
	}
	return targets, nil
}

func matchingRoots(roots []TrustAnchor, view *certView) []string {
	matches := make([]string, 0, len(roots))
	for _, root := range roots {
		publicKey := root.effectiveKey()
		if len(publicKey) != ed25519.PublicKeySize {
			continue
		}
		rootID := root.effectiveID()
		if rootID != "" && view.issuerID != rootID &&
			!issuerIDMatchesKey(view.issuerID, publicKey) {
			continue
		}
		if view.cert.VerifySignature(publicKey) != nil {
			continue
		}
		if rootID == "" {
			rootID = view.issuerID
		}
		matches = append(matches, rootID)
	}
	sort.Strings(matches)
	return matches
}

func validAt(view *certView, at time.Time) bool {
	return !at.Before(view.notBefore) && at.Before(view.notAfter)
}

func containsOperation(operations []string, operation string) bool {
	for _, item := range operations {
		if item == operation {
			return true
		}
	}
	return false
}

func intersectOperations(left, right []string) []string {
	rightSet := make(map[string]struct{}, len(right))
	for _, item := range right {
		rightSet[item] = struct{}{}
	}
	out := make([]string, 0)
	for _, item := range left {
		if _, ok := rightSet[item]; ok {
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}

func subjectRepeated(path []int, childIndex int, views []*certView) bool {
	child := views[childIndex]
	for _, index := range path {
		if bytes.Equal(views[index].publicKey, child.publicKey) {
			return true
		}
	}
	return false
}

func chainStateKey(state chainState) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%016x|%d|", state.path[len(state.path)-1], state.visited, state.remaining)
	for _, operation := range state.operations {
		fmt.Fprintf(h, "%d:", len(operation))
		_, _ = h.Write([]byte(operation))
	}
	_, _ = h.Write([]byte{'|'})
	_, _ = h.Write([]byte(prefixStateKey(state.prefixes)))
	return string(h.Sum(nil))
}

func buildResult(state chainState, views []*certView) *Result {
	chain := make([]*Certificate, 0, len(state.path))
	for i := len(state.path) - 1; i >= 0; i-- {
		chain = append(chain, views[state.path[i]].cert)
	}
	return &Result{
		Chain:  chain,
		RootID: state.rootID,
		Policy: Policy{
			Operations:               append([]string(nil), state.operations...),
			ResourcePrefixes:         append([]string(nil), prefixTexts(state.prefixes)...),
			RemainingDelegationDepth: state.remaining,
		},
	}
}

// EffectivePolicy re-computes the policy represented by a leaf-to-root chain.
// It is primarily useful to callers that received a chain and want to inspect
// its authority. The chain must be non-empty.
func EffectivePolicy(chain []*Certificate) (Policy, error) {
	if len(chain) == 0 {
		return Policy{}, fmt.Errorf("%w: empty chain", ErrInvalidRequest)
	}
	views := make([]*certView, 0, len(chain))
	for i, cert := range chain {
		view, err := newCertView(i, cert)
		if err != nil {
			return Policy{}, err
		}
		views = append(views, view)
	}

	var (
		operations []string
		prefixes   []pathPrefix
		remaining  uint32
	)
	for i := len(views) - 1; i >= 0; i-- {
		view := views[i]
		if i == len(views)-1 {
			operations = append([]string(nil), view.operations...)
			prefixes = append([]pathPrefix(nil), view.prefixes...)
			remaining = view.maxDepth
			continue
		}
		operations = intersectOperations(operations, view.operations)
		prefixes = intersectPrefixes(prefixes, view.prefixes)
		if remaining == 0 {
			return Policy{}, fmt.Errorf("%w: chain exceeds delegation depth", ErrNoValidChain)
		}
		remaining--
		if view.maxDepth < remaining {
			remaining = view.maxDepth
		}
	}
	return Policy{
		Operations:               operations,
		ResourcePrefixes:         prefixTexts(prefixes),
		RemainingDelegationDepth: remaining,
	}, nil
}
