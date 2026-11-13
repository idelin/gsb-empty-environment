package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"net/netip"
	"sync"
	"time"
)

func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }

// Default limits applied when the host configuration leaves them at zero.
const (
	DefaultMaxPCountPerHop = 8
	DefaultMaxPathLength   = 32
)

// RelationshipPolicy evaluates whether a cryptographically valid route is also
// acceptable under the host's commercial relationships (e.g. route-policy /
// provider-customer / peer export rules). It is deliberately separate from the
// signature chain: a chain passing cryptographic checks does NOT imply the
// business relationship is valid, and vice versa.
type RelationshipPolicy interface {
	BusinessAccept(v *VerifiedPath, sessionPeerAS uint32) bool
}

// HostConfig parameterizes a routing host.
type HostConfig struct {
	LocalAS         uint32
	Registry        *KeyRegistry
	MaxPCountPerHop uint32 // upper bound for a single hop's pCount
	MaxPathLength   uint32 // upper bound for the expanded logical AS path
	Policy          RelationshipPolicy
	Now             func() time.Time // optional clock override (tests)
}

// InstalledRoute is a route that survived both cryptographic and business
// policy checks and was placed in the host's RIB.
type InstalledRoute struct {
	Prefix         netip.Prefix
	PathID         PathID
	LogicalPath    []uint32
	KeyGenerations []uint64
	InstalledAt    time.Time
}

// Host is a routing host that verifies and installs signed paths.
type Host struct {
	localAS       uint32
	registry      *KeyRegistry
	maxPCount     uint32
	maxPathLength uint32
	policy        RelationshipPolicy
	now           func() time.Time

	mu  sync.Mutex
	rib map[netip.Prefix]InstalledRoute
}

// NewHost builds a host. Zero-valued limits get documented defaults.
func NewHost(cfg HostConfig) *Host {
	if cfg.MaxPCountPerHop == 0 {
		cfg.MaxPCountPerHop = DefaultMaxPCountPerHop
	}
	if cfg.MaxPathLength == 0 {
		cfg.MaxPathLength = DefaultMaxPathLength
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Host{
		localAS:       cfg.LocalAS,
		registry:      cfg.Registry,
		maxPCount:     cfg.MaxPCountPerHop,
		maxPathLength: cfg.MaxPathLength,
		policy:        cfg.Policy,
		now:           now,
		rib:           make(map[netip.Prefix]InstalledRoute),
	}
}

// VerifyCryptographic runs every chain check but does not touch the RIB and
// does not evaluate business relationships. It is exported so the host (or an
// embedded test harness) can confirm a chain independently of policy.
func (h *Host) VerifyCryptographic(sp SignedPath, sessionPeerAS uint32) (*VerifiedPath, error) {
	if h.registry == nil {
		return nil, ErrNoRegistry
	}
	if h.localAS == 0 {
		return nil, ErrNoLocalAS
	}
	if len(sp.Records) == 0 {
		return nil, ErrEmptyChain
	}

	prefix := sp.Prefix.Masked()
	if !prefix.IsValid() || prefix != sp.Prefix {
		return nil, ErrInvalidPrefix
	}
	if sp.OriginAS == 0 {
		return nil, ErrReservedAS
	}

	now := h.now()
	evidence := make([]HopEvidence, 0, len(sp.Records))
	chainOrder := make([]uint32, 0, len(sp.Records))
	keyGens := make([]uint64, 0, len(sp.Records))
	logicalPath := make([]uint32, 0, len(sp.Records))

	var totalLength uint32
	seenSigner := make(map[uint32]struct{}, len(sp.Records))
	var prevDigest [32]byte

	for i := range sp.Records {
		rec := sp.Records[i]

		if rec.SignerAS == 0 || rec.NextPeerAS == 0 {
			return nil, ErrReservedAS
		}
		if rec.PCount < 1 || rec.PCount > h.maxPCount {
			return nil, ErrPCountRange
		}
		if _, dup := seenSigner[rec.SignerAS]; dup {
			return nil, ErrASLoop
		}
		seenSigner[rec.SignerAS] = struct{}{}

		// Origin record rules: zero previousHash and signer == origin.
		if i == 0 {
			if rec.PreviousHash != ZeroHash || rec.SignerAS != sp.OriginAS {
				return nil, ErrBadOrigin
			}
		} else {
			if rec.PreviousHash != prevDigest {
				return nil, ErrPreviousHash
			}
			if sp.Records[i-1].NextPeerAS != rec.SignerAS {
				return nil, ErrHopLinkage
			}
		}

		// Resolve the trusted key for (signerAS, keyGeneration); revoked or
		// expired keys are rejected before any cryptographic use.
		trusted, err := h.registry.Lookup(rec.SignerAS, rec.KeyGeneration, now)
		if err != nil {
			return nil, err
		}
		if len(rec.Signature) != ed25519.SignatureSize {
			return nil, ErrBadSignature
		}
		if !ed25519.Verify(trusted.Public, recordTranscript(prefix, sp.PathID, rec), rec.Signature) {
			return nil, ErrBadSignature
		}

		digest := RecordDigest(prefix, sp.PathID, rec)
		evidence = append(evidence, HopEvidence{Record: rec, Digest: digest})
		chainOrder = append(chainOrder, rec.SignerAS)
		keyGens = append(keyGens, rec.KeyGeneration)
		prevDigest = digest

		// Expand pCount into the logical path as AS prepending: the signer AS
		// is repeated pCount times (consecutive repeats are legitimate
		// prepending). The host's maximum path length is enforced as the path
		// grows, so an inflated pCount cannot push the route past the limit.
		for j := uint32(0); j < rec.PCount; j++ {
			logicalPath = append(logicalPath, rec.SignerAS)
			totalLength++
			if totalLength > h.maxPathLength {
				return nil, ErrPathTooLong
			}
		}
	}

	last := sp.Records[len(sp.Records)-1]
	if last.NextPeerAS != h.localAS {
		return nil, ErrFinalRecipient
	}
	if sessionPeerAS != last.SignerAS {
		return nil, ErrSessionPeer
	}

	// logicalPath currently runs origin -> receiver; reverse to the BGP
	// receiver -> origin AS_PATH order, then run the explicit loop scan.
	reversed := make([]uint32, len(logicalPath))
	for i, as := range logicalPath {
		reversed[len(logicalPath)-1-i] = as
	}
	if hasASLoop(reversed) {
		return nil, ErrASLoop
	}

	return &VerifiedPath{
		Prefix:         prefix,
		PathID:         sp.PathID,
		OriginAS:       sp.OriginAS,
		LogicalPath:    reversed,
		ChainOrder:     chainOrder,
		Evidence:       evidence,
		KeyGenerations: keyGens,
	}, nil
}

// hasASLoop reports a non-consecutive repeated AS. Consecutive repeats (AS
// prepending inside one hop) are produced by pCount and allowed; a repeated AS
// separated by other ASes is a loop.
func hasASLoop(path []uint32) bool {
	seen := make(map[uint32]struct{}, len(path))
	var prev uint32
	for i, as := range path {
		if i > 0 && as == prev {
			prev = as
			continue
		}
		if _, ok := seen[as]; ok {
			return true
		}
		seen[as] = struct{}{}
		prev = as
	}
	return false
}

// Install verifies the chain fully, enforces the business policy, and only then
// writes the route into the RIB. Any failure returns an error and installs
// nothing (zero-install semantics).
func (h *Host) Install(sp SignedPath, sessionPeerAS uint32) (*VerifiedPath, error) {
	verified, err := h.VerifyCryptographic(sp, sessionPeerAS)
	if err != nil {
		return nil, err
	}
	if h.policy == nil {
		return nil, ErrPolicyNotConfigured
	}
	if !h.policy.BusinessAccept(verified, sessionPeerAS) {
		return nil, ErrBusinessPolicy
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.rib[verified.Prefix] = InstalledRoute{
		Prefix:         verified.Prefix,
		PathID:         verified.PathID,
		LogicalPath:    append([]uint32(nil), verified.LogicalPath...),
		KeyGenerations: append([]uint64(nil), verified.KeyGenerations...),
		InstalledAt:    h.now(),
	}
	return verified, nil
}

// InstalledRoute returns the RIB entry for a prefix, if present.
func (h *Host) InstalledRoute(prefix netip.Prefix) (InstalledRoute, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rib[prefix.Masked()]
	return r, ok
}

// InstalledCount reports how many routes are currently installed in the RIB.
func (h *Host) InstalledCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.rib)
}
