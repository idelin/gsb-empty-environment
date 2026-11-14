package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// HashAlgorithmSHA1 is the only NSEC3 hash algorithm defined by RFC 5155.
	HashAlgorithmSHA1 uint8 = 1
	// DefaultNSEC3MaxIterations is the RFC 5155 upper bound. RFC 9276
	// recommends zero, but configurations may still need to validate older
	// zones, so the cache accepts up to this bound.
	DefaultNSEC3MaxIterations uint16 = 2500
)

// TenantView identifies one split-DNS tenant view.
type TenantView string

// ProofKind identifies the denial-record family that forms a cache key.
type ProofKind uint8

const (
	ProofNSEC  ProofKind = 1
	ProofNSEC3 ProofKind = 2
)

func (k ProofKind) String() string {
	switch k {
	case ProofNSEC:
		return "NSEC"
	case ProofNSEC3:
		return "NSEC3"
	default:
		return "unknown"
	}
}

// Parameters is the exact NSEC/NSEC3 parameter set bound into the cache key.
// For NSEC, HashAlgorithm, Iterations, and Salt must all be zero.
type Parameters struct {
	Kind          ProofKind
	HashAlgorithm uint8
	Iterations    uint16
	Salt          []byte
}

// ParamsNSEC returns the sole valid NSEC parameter set.
func ParamsNSEC() Parameters { return Parameters{Kind: ProofNSEC} }

// ParamsNSEC3 constructs an RFC 5155 SHA-1 parameter set. The salt is copied.
func ParamsNSEC3(iterations uint16, salt []byte) Parameters {
	return Parameters{
		Kind:          ProofNSEC3,
		HashAlgorithm: HashAlgorithmSHA1,
		Iterations:    iterations,
		Salt:          append([]byte(nil), salt...),
	}
}

// Clone returns a deep copy.
func (p Parameters) Clone() Parameters {
	p.Salt = append([]byte(nil), p.Salt...)
	return p
}

// Equal reports exact parameter-set equality.
func (p Parameters) Equal(o Parameters) bool {
	return p.Kind == o.Kind &&
		p.HashAlgorithm == o.HashAlgorithm &&
		p.Iterations == o.Iterations &&
		bytes.Equal(p.Salt, o.Salt)
}

func (p Parameters) validate(maxIterations uint16) error {
	switch p.Kind {
	case ProofNSEC:
		if p.HashAlgorithm != 0 || p.Iterations != 0 || len(p.Salt) != 0 {
			return fmt.Errorf("%w: NSEC parameters must be zero", ErrMalformed)
		}
	case ProofNSEC3:
		if p.HashAlgorithm != HashAlgorithmSHA1 {
			return fmt.Errorf("%w: unsupported NSEC3 hash algorithm %d", ErrMalformed, p.HashAlgorithm)
		}
		if p.Iterations > maxIterations {
			return fmt.Errorf("%w: NSEC3 iterations %d exceed policy limit %d", ErrMalformed, p.Iterations, maxIterations)
		}
		if len(p.Salt) > 255 {
			return fmt.Errorf("%w: NSEC3 salt too long", ErrMalformed)
		}
	default:
		return fmt.Errorf("%w: unknown proof kind", ErrMalformed)
	}
	return nil
}

func (p Parameters) String() string {
	if p.Kind == ProofNSEC {
		return "NSEC"
	}
	return fmt.Sprintf("NSEC3(alg=%d,iter=%d,salt=%s)", p.HashAlgorithm, p.Iterations, hex.EncodeToString(p.Salt))
}

func (p Parameters) id() string {
	var b strings.Builder
	b.WriteByte(byte(p.Kind))
	b.WriteByte(p.HashAlgorithm)
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], p.Iterations)
	b.Write(tmp[:])
	b.WriteString(hex.EncodeToString(p.Salt))
	return b.String()
}

// Policy is the trusted, per-view configuration used both to validate proofs
// and to bind cache entries. Generation must change whenever policy state
// changes; KeyGeneration must advance when a validation key is rotated or
// revoked.
type Policy struct {
	View               TenantView
	Generation         uint64
	KeyGeneration      uint64
	MaxNSEC3Iterations uint16
	AllowNSEC3OptOut   bool
	AllowedParameters  []Parameters
	AllowedAlgorithms  []uint8
	Apexes             []Name
}

func (p Policy) withDefaults() Policy {
	if p.MaxNSEC3Iterations == 0 {
		p.MaxNSEC3Iterations = DefaultNSEC3MaxIterations
	}
	p.AllowedParameters = cloneParameters(p.AllowedParameters)
	p.AllowedAlgorithms = append([]uint8(nil), p.AllowedAlgorithms...)
	p.Apexes = append([]Name(nil), p.Apexes...)
	return p
}

func validatePolicy(p Policy) error {
	if p.View == "" {
		return fmt.Errorf("%w: empty tenant view", ErrInvalidPolicy)
	}
	if p.MaxNSEC3Iterations > DefaultNSEC3MaxIterations {
		return fmt.Errorf("%w: iteration limit exceeds RFC 5155 bound", ErrInvalidPolicy)
	}
	for _, params := range p.AllowedParameters {
		if err := params.validate(p.MaxNSEC3Iterations); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
		}
	}
	for _, n := range p.Apexes {
		if n.IsZero() {
			return fmt.Errorf("%w: zero apex", ErrInvalidPolicy)
		}
	}
	return nil
}

func (p Policy) fingerprint() [32]byte {
	var b strings.Builder
	writeLengthPrefixed(&b, string(p.View))
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], p.Generation)
	b.Write(tmp[:])
	binary.BigEndian.PutUint64(tmp[:], p.KeyGeneration)
	b.Write(tmp[:])
	binary.BigEndian.PutUint16(tmp[:2], p.MaxNSEC3Iterations)
	b.Write(tmp[:2])
	if p.AllowNSEC3OptOut {
		b.WriteByte(1)
	} else {
		b.WriteByte(0)
	}
	params := append([]Parameters(nil), p.AllowedParameters...)
	sort.Slice(params, func(i, j int) bool { return params[i].id() < params[j].id() })
	binary.BigEndian.PutUint32(tmp[:4], uint32(len(params)))
	b.Write(tmp[:4])
	for _, params := range params {
		writeLengthPrefixed(&b, params.id())
	}
	algs := append([]uint8(nil), p.AllowedAlgorithms...)
	sort.Slice(algs, func(i, j int) bool { return algs[i] < algs[j] })
	binary.BigEndian.PutUint32(tmp[:4], uint32(len(algs)))
	b.Write(tmp[:4])
	b.Write(algs)
	apexes := append([]Name(nil), p.Apexes...)
	sort.Slice(apexes, func(i, j int) bool { return CompareNames(apexes[i], apexes[j]) < 0 })
	binary.BigEndian.PutUint32(tmp[:4], uint32(len(apexes)))
	b.Write(tmp[:4])
	for _, apex := range apexes {
		writeLengthPrefixed(&b, apex.wire)
	}
	return sha256.Sum256([]byte(b.String()))
}

func writeLengthPrefixed(b *strings.Builder, s string) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(s)))
	b.Write(n[:])
	b.WriteString(s)
}

func cloneParameters(in []Parameters) []Parameters {
	out := make([]Parameters, len(in))
	for i := range in {
		out[i] = in[i].Clone()
	}
	return out
}

// RRSIG is the authenticated signature metadata attached to one denial RRset.
// Verified must only be set by the trusted DNSSEC validator. KeyGeneration is
// the resolver's operational key epoch, not an RRSIG wire field.
type RRSIG struct {
	TypeCovered   uint16
	Algorithm     uint8
	KeyTag        uint16
	Signer        Name
	KeyGeneration uint64
	ValidFrom     time.Time
	ValidUntil    time.Time
	Verified      bool
}

func (s RRSIG) clone() RRSIG { return s }

// Attestation is the validator's statement that the denial chain was
// authenticated for one view, apex, class, parameter set, and key epoch.
type Attestation struct {
	View               TenantView
	Apex               Name
	Class              uint16
	KeyGeneration      uint64
	PolicyGeneration   uint64
	Parameters         Parameters
	ChainVerified      bool
	SignaturesVerified bool
	ValidFrom          time.Time
	ValidUntil         time.Time
}

// Question is the original query bound into a proof.
type Question struct {
	Name  Name
	Type  uint16
	Class uint16
}

// Delegation describes a delegation point observed by the validator. Signed
// means that a DS RRset exists; unsigned delegations are typically hidden by
// NSEC3 opt-out.
type Delegation struct {
	Name   Name
	Signed bool
}

// NSECRecord is an authenticated NSEC RRset and its type bitmap.
type NSECRecord struct {
	Owner Name
	Next  Name
	Types TypeBitmap
	Class uint16
	TTL   uint32
	RRSIG RRSIG
}

// NSEC3Record is an authenticated NSEC3 RRset. NextHashedOwner and Salt are
// raw octet strings, not presentation text. Owner is the full name whose first
// label is the base32hex-encoded hashed owner.
type NSEC3Record struct {
	Owner           Name
	NextHashedOwner []byte
	HashAlgorithm   uint8
	Iterations      uint16
	Salt            []byte
	OptOut          bool
	Types           TypeBitmap
	Class           uint16
	TTL             uint32
	RRSIG           RRSIG
}

// Parameters returns the record's parameter set.
func (r NSEC3Record) Parameters() Parameters {
	return Parameters{
		Kind:          ProofNSEC3,
		HashAlgorithm: r.HashAlgorithm,
		Iterations:    r.Iterations,
		Salt:          append([]byte(nil), r.Salt...),
	}
}

// RDATA returns canonical NSEC RDATA: next-name wire plus type bitmap.
func (r NSECRecord) RDATA() []byte {
	next := r.Next.Wire()
	bitmap := r.Types.Canonical().Wire()
	return append(append([]byte(nil), next...), bitmap...)
}

// RDATA returns canonical NSEC3 RDATA according to RFC 5155 section 3.3.
func (r NSEC3Record) RDATA() []byte {
	var out []byte
	out = append(out, r.HashAlgorithm)
	var flags byte
	if r.OptOut {
		flags |= 0x01
	}
	out = append(out, flags)
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], r.Iterations)
	out = append(out, tmp[:]...)
	out = append(out, byte(len(r.Salt)))
	out = append(out, r.Salt...)
	out = append(out, byte(len(r.NextHashedOwner)))
	out = append(out, r.NextHashedOwner...)
	out = append(out, r.Types.Canonical().Wire()...)
	return out
}

// Response is the per-view, already-validated upstream denial response that a
// caller offers to the cache. The cache re-checks all binding and semantic
// conditions before caching or synthesizing an answer.
type Response struct {
	Attestation Attestation
	Question    Question
	NSEC        []NSECRecord
	NSEC3       []NSEC3Record
	Delegations []Delegation
	NegativeTTL uint32
}

// ResultKind is the synthesized DNS negative conclusion.
type ResultKind uint8

const (
	ResultNXDOMAIN ResultKind = 1
	ResultNODATA   ResultKind = 2
)

func (k ResultKind) String() string {
	switch k {
	case ResultNXDOMAIN:
		return "NXDOMAIN"
	case ResultNODATA:
		return "NODATA"
	default:
		return "unknown"
	}
}

// WitnessRole identifies why an authenticated RRset was used.
type WitnessRole uint8

const (
	WitnessClosestEncloser WitnessRole = iota + 1
	WitnessNextCloserCover
	WitnessWildcardCover
	WitnessWildcardMatch
	WitnessExactMatch
	WitnessEmptyNonTerminal
	WitnessOptOutCover
	WitnessDelegation
)

func (r WitnessRole) String() string {
	switch r {
	case WitnessClosestEncloser:
		return "closest-encloser"
	case WitnessNextCloserCover:
		return "next-closer-cover"
	case WitnessWildcardCover:
		return "wildcard-cover"
	case WitnessWildcardMatch:
		return "wildcard-match"
	case WitnessExactMatch:
		return "exact-match"
	case WitnessEmptyNonTerminal:
		return "empty-non-terminal"
	case WitnessOptOutCover:
		return "opt-out-cover"
	case WitnessDelegation:
		return "delegation"
	default:
		return "unknown"
	}
}

// RRSetWitness is the exact authenticated RRset used by the synthesizer.
type RRSetWitness struct {
	Owner Name
	Type  uint16
	Class uint16
	TTL   uint32
	RDATA []byte
	RRSIG RRSIG
}

// Witness pairs a role with the RRset that fulfilled it.
type Witness struct {
	Role  WitnessRole
	RRSet RRSetWitness
	NSEC  *NSECRecord
	NSEC3 *NSEC3Record
}

// Answer is a synthesized negative conclusion and the witnesses used.
type Answer struct {
	Result           ResultKind
	View             TenantView
	Apex             Name
	Class            uint16
	QueryName        Name
	QueryType        uint16
	Parameters       Parameters
	KeyGeneration    uint64
	PolicyGeneration uint64
	ValidFrom        time.Time
	ValidUntil       time.Time
	Witnesses        []Witness
	CacheHit         bool
}

func (a Answer) clone() Answer {
	a.Parameters = a.Parameters.Clone()
	a.Witnesses = cloneWitnesses(a.Witnesses)
	return a
}

func cloneWitnesses(in []Witness) []Witness {
	out := make([]Witness, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].RRSet.RDATA = append([]byte(nil), in[i].RRSet.RDATA...)
		if in[i].NSEC != nil {
			rec := *in[i].NSEC
			rec.Types = in[i].NSEC.Types.Canonical()
			out[i].NSEC = &rec
		}
		if in[i].NSEC3 != nil {
			rec := *in[i].NSEC3
			rec.Salt = append([]byte(nil), in[i].NSEC3.Salt...)
			rec.NextHashedOwner = append([]byte(nil), in[i].NSEC3.NextHashedOwner...)
			rec.Types = in[i].NSEC3.Types.Canonical()
			out[i].NSEC3 = &rec
		}
	}
	return out
}

// CacheKey binds a cache entry to the split view, authenticated apex, class,
// proof parameter set, key epoch, and canonical query name. Query type is
// intentionally absent: one NXDOMAIN proof applies to all types, while NODATA
// proofs are tested type-by-type against their authenticated bitmap.
type CacheKey struct {
	View          TenantView
	Apex          Name
	Class         uint16
	Parameters    Parameters
	KeyGeneration uint64
	QueryName     Name
}

func (k CacheKey) id() string {
	var b strings.Builder
	writeLengthPrefixed(&b, string(k.View))
	writeLengthPrefixed(&b, k.Apex.wire)
	var tmp [8]byte
	binary.BigEndian.PutUint16(tmp[:2], k.Class)
	b.Write(tmp[:2])
	writeLengthPrefixed(&b, k.Parameters.id())
	binary.BigEndian.PutUint64(tmp[:], k.KeyGeneration)
	b.Write(tmp[:])
	writeLengthPrefixed(&b, k.QueryName.wire)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Query asks the cache for a negative conclusion. Apex and Parameters are
// optional narrowing filters; leaving them zero lets the cache choose the most
// specific authenticated entry for the view and name.
type Query struct {
	View       TenantView
	Name       Name
	Type       uint16
	Class      uint16
	Apex       Name
	Parameters *Parameters
}
