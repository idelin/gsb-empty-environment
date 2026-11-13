package netsec

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"math/bits"
	"sort"
)

// MaxSetSize is the maximum number of distinct primes in an accumulator set.
const MaxSetSize = 128

// Params contains the host-provided trusted modulus and generator.
// NewParams copies both values.
type Params struct {
	N *big.Int
	G *big.Int
}

// Identity is the host-controlled namespace in which a proof is evaluated.
// RootDigest is a digest of the trusted root, never a root asserted by a proof.
type Identity struct {
	SetID      string
	Generation uint64
	RootDigest [32]byte
}

// RootDigestHex returns the lowercase hexadecimal digest.
func (id Identity) RootDigestHex() string {
	return fmt.Sprintf("%x", id.RootDigest[:])
}

// Accumulator is an algebraically-defined snapshot, except that Add and
// Delete deliberately advance the receiver to a new generation. Callers that
// need to retain an old snapshot should Clone before mutating.
type Accumulator struct {
	params   Params
	elements []uint32
	product  *big.Int
	root     *big.Int
	identity Identity
}

// NewParams validates and copies the trusted modulus and generator.
func NewParams(n, g *big.Int) (Params, error) {
	nCopy, gCopy, err := validateParams(n, g)
	if err != nil {
		return Params{}, err
	}
	return Params{N: nCopy, G: gCopy}, nil
}

// ParseParams is the canonical-decimal constructor counterpart to NewParams.
func ParseParams(n, g string) (Params, error) {
	nn, err := ParseCanonicalInt(n)
	if err != nil {
		return Params{}, fmt.Errorf("%w: %w", ErrInvalidModulus, err)
	}
	gg, err := parseCanonicalPositive(g, "generator")
	if err != nil {
		return Params{}, fmt.Errorf("%w: %w", ErrInvalidGenerator, err)
	}
	return NewParams(nn, gg)
}

// New creates an accumulator for setID at generation. Elements are sorted
// internally, so the product and root do not depend on input order.
func New(n, g *big.Int, setID string, generation uint64, elements []uint32) (*Accumulator, error) {
	params, err := NewParams(n, g)
	if err != nil {
		return nil, err
	}
	return buildAccumulator(params, setID, generation, elements)
}

// NewFromDecimals is New with canonical decimal strings for N and g.
func NewFromDecimals(n, g, setID string, generation uint64, elements []uint32) (*Accumulator, error) {
	params, err := ParseParams(n, g)
	if err != nil {
		return nil, err
	}
	return buildAccumulator(params, setID, generation, elements)
}

// NewFromStrings accepts canonical decimal strings for the set elements as
// well as for N and g. It is the path that rejects non-canonical integers
// supplied by an import or network peer.
func NewFromStrings(n, g, setID string, generation uint64, elements []string) (*Accumulator, error) {
	parsed, err := ParsePrimeElements(elements)
	if err != nil {
		return nil, err
	}
	return NewFromDecimals(n, g, setID, generation, parsed)
}

// NewAccumulator is an alias for New.
func NewAccumulator(n, g *big.Int, setID string, generation uint64, elements []uint32) (*Accumulator, error) {
	return New(n, g, setID, generation, elements)
}

// ParsePrimeElements parses canonical decimal uint32 primes and rejects
// duplicates.
func ParsePrimeElements(elements []string) ([]uint32, error) {
	if len(elements) > MaxSetSize {
		return nil, ErrSetTooLarge
	}
	out := make([]uint32, 0, len(elements))
	seen := make(map[uint32]struct{}, len(elements))
	for _, s := range elements {
		v, err := ParseCanonicalUint32(s)
		if err != nil {
			return nil, errors.Join(ErrInvalidElement, err)
		}
		if !IsPrime32(v) {
			return nil, fmt.Errorf("%w: %d is not prime", ErrInvalidElement, v)
		}
		if _, ok := seen[v]; ok {
			return nil, fmt.Errorf("%w: %d", ErrDuplicateElement, v)
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// Params returns copies of the trusted parameters.
func (a *Accumulator) Params() Params {
	if a == nil {
		return Params{}
	}
	return Params{N: new(big.Int).Set(a.params.N), G: new(big.Int).Set(a.params.G)}
}

// Root returns a copy of A.
func (a *Accumulator) Root() *big.Int {
	if a == nil {
		return nil
	}
	return new(big.Int).Set(a.root)
}

// Product returns a copy of P. For an empty set this is 1.
func (a *Accumulator) Product() *big.Int {
	if a == nil {
		return nil
	}
	return new(big.Int).Set(a.product)
}

// Elements returns a sorted copy of the set.
func (a *Accumulator) Elements() []uint32 {
	if a == nil {
		return nil
	}
	out := make([]uint32, len(a.elements))
	copy(out, a.elements)
	return out
}

// SetID returns the host-controlled set identifier.
func (a *Accumulator) SetID() string {
	if a == nil {
		return ""
	}
	return a.identity.SetID
}

// Generation returns the current snapshot generation.
func (a *Accumulator) Generation() uint64 {
	if a == nil {
		return 0
	}
	return a.identity.Generation
}

// Identity returns the current trusted identity snapshot.
func (a *Accumulator) Identity() Identity {
	if a == nil {
		return Identity{}
	}
	return a.identity
}

// RootDigest returns the digest of Root under the trusted parameters.
func (a *Accumulator) RootDigest() [32]byte {
	if a == nil {
		return [32]byte{}
	}
	return a.identity.RootDigest
}

// Contains reports whether x is in this snapshot's set.
func (a *Accumulator) Contains(x uint32) bool {
	if a == nil {
		return false
	}
	i := sort.Search(len(a.elements), func(i int) bool { return a.elements[i] >= x })
	return i < len(a.elements) && a.elements[i] == x
}

// Clone makes a deep copy, suitable for retaining the old snapshot before a
// mutating Add or Delete.
func (a *Accumulator) Clone() *Accumulator {
	if a == nil {
		return nil
	}
	return &Accumulator{
		params:   Params{N: new(big.Int).Set(a.params.N), G: new(big.Int).Set(a.params.G)},
		elements: append([]uint32(nil), a.elements...),
		product:  new(big.Int).Set(a.product),
		root:     new(big.Int).Set(a.root),
		identity: a.identity,
	}
}

// Add inserts a distinct prime and advances the receiver to the next
// generation. It returns the receiver for convenient chaining. On error the
// receiver is unchanged.
func (a *Accumulator) Add(x uint32) (*Accumulator, error) {
	if a == nil {
		return nil, errors.New("netsec: nil accumulator")
	}
	if !IsPrime32(x) {
		return nil, fmt.Errorf("%w: %d is not a 32-bit prime", ErrInvalidElement, x)
	}
	if a.Contains(x) {
		return nil, fmt.Errorf("%w: %d", ErrDuplicateElement, x)
	}
	if len(a.elements) >= MaxSetSize {
		return nil, ErrSetTooLarge
	}
	if a.identity.Generation == ^uint64(0) {
		return nil, ErrGenerationOverflow
	}

	next := make([]uint32, 0, len(a.elements)+1)
	next = append(next, a.elements...)
	next = append(next, x)
	sort.Slice(next, func(i, j int) bool { return next[i] < next[j] })

	updated, err := buildAccumulator(a.params, a.identity.SetID, a.identity.Generation+1, next)
	if err != nil {
		return nil, err
	}
	*a = *updated
	return a, nil
}

// WithAdded returns a new snapshot with x added. The receiver is unchanged.
func (a *Accumulator) WithAdded(x uint32) (*Accumulator, error) {
	if a == nil {
		return nil, errors.New("netsec: nil accumulator")
	}
	return a.Clone().Add(x)
}

// Delete removes a member and advances the receiver to the next generation.
// Deleting an absent value is rejected. On error the receiver is unchanged.
func (a *Accumulator) Delete(x uint32) (*Accumulator, error) {
	if a == nil {
		return nil, errors.New("netsec: nil accumulator")
	}
	index := sort.Search(len(a.elements), func(i int) bool { return a.elements[i] >= x })
	if index >= len(a.elements) || a.elements[index] != x {
		return nil, fmt.Errorf("%w: %d", ErrNotMember, x)
	}
	if a.identity.Generation == ^uint64(0) {
		return nil, ErrGenerationOverflow
	}

	next := make([]uint32, 0, len(a.elements)-1)
	next = append(next, a.elements[:index]...)
	next = append(next, a.elements[index+1:]...)
	updated, err := buildAccumulator(a.params, a.identity.SetID, a.identity.Generation+1, next)
	if err != nil {
		return nil, err
	}
	*a = *updated
	return a, nil
}

// Remove is an alias for Delete.
func (a *Accumulator) Remove(x uint32) (*Accumulator, error) {
	return a.Delete(x)
}

// WithDeleted returns a new snapshot with x removed. The receiver is unchanged.
func (a *Accumulator) WithDeleted(x uint32) (*Accumulator, error) {
	if a == nil {
		return nil, errors.New("netsec: nil accumulator")
	}
	return a.Clone().Delete(x)
}

func buildAccumulator(params Params, setID string, generation uint64, elements []uint32) (*Accumulator, error) {
	normalized, product, err := normalizeSet(elements)
	if err != nil {
		return nil, err
	}
	root := new(big.Int).Exp(params.G, product, params.N)
	if err := checkUnit(root, params.N); err != nil {
		// Since g is a unit, this should be unreachable; keep it as a
		// defensive invariant.
		return nil, errors.Join(ErrInvalidGenerator, err)
	}
	digest, err := ComputeRootDigest(params.N, params.G, root)
	if err != nil {
		return nil, err
	}
	return &Accumulator{
		params:   Params{N: new(big.Int).Set(params.N), G: new(big.Int).Set(params.G)},
		elements: normalized,
		product:  product,
		root:     root,
		identity: Identity{
			SetID:      setID,
			Generation: generation,
			RootDigest: digest,
		},
	}, nil
}

func validateParams(n, g *big.Int) (*big.Int, *big.Int, error) {
	if n == nil {
		return nil, nil, fmt.Errorf("%w: nil", ErrInvalidModulus)
	}
	if n.Sign() <= 0 || n.Bit(0) == 0 || n.Cmp(big.NewInt(3)) < 0 {
		return nil, nil, fmt.Errorf("%w: N must be an odd integer >= 3", ErrInvalidModulus)
	}
	if g == nil {
		return nil, nil, fmt.Errorf("%w: nil", ErrInvalidGenerator)
	}
	if g.Sign() <= 0 || g.Cmp(n) >= 0 {
		return nil, nil, fmt.Errorf("%w: g must be in [1, N-1]", ErrInvalidGenerator)
	}
	if new(big.Int).GCD(nil, nil, g, n).Cmp(big.NewInt(1)) != 0 {
		return nil, nil, errors.Join(ErrInvalidGenerator, ErrNotUnit)
	}
	return new(big.Int).Set(n), new(big.Int).Set(g), nil
}

func normalizeSet(elements []uint32) ([]uint32, *big.Int, error) {
	if len(elements) > MaxSetSize {
		return nil, nil, ErrSetTooLarge
	}
	out := make([]uint32, len(elements))
	copy(out, elements)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })

	product := big.NewInt(1)
	var previous uint32
	for i, x := range out {
		if !IsPrime32(x) {
			return nil, nil, fmt.Errorf("%w: %d is not a 32-bit prime", ErrInvalidElement, x)
		}
		if i > 0 && x == previous {
			return nil, nil, fmt.Errorf("%w: %d", ErrDuplicateElement, x)
		}
		product.Mul(product, new(big.Int).SetUint64(uint64(x)))
		previous = x
	}
	return out, product, nil
}

func checkUnit(value, n *big.Int) error {
	if value == nil || value.Sign() <= 0 || value.Cmp(n) >= 0 {
		return errors.Join(ErrNotUnit, ErrInvalidRange)
	}
	if new(big.Int).GCD(nil, nil, value, n).Cmp(big.NewInt(1)) != 0 {
		return ErrNotUnit
	}
	return nil
}

// ComputeRootDigest computes a domain-separated SHA-256 digest of the root
// under the trusted parameters. The exact input is
// "netsec/q0043/root-digest/v1\n<N>\n<g>\n<A>", with each integer in canonical
// base-10 form. Binding N and g prevents a proof from accidentally being
// accepted under a different parameter set with the same root value.
func ComputeRootDigest(n, g, root *big.Int) ([32]byte, error) {
	var zero [32]byte
	if n == nil || g == nil || root == nil {
		return zero, errors.New("netsec: nil value in root digest")
	}
	if n.Sign() <= 0 || g.Sign() <= 0 || root.Sign() <= 0 {
		return zero, errors.New("netsec: non-positive value in root digest")
	}
	input := "netsec/q0043/root-digest/v1\n" + n.String() + "\n" + g.String() + "\n" + root.String()
	return sha256.Sum256([]byte(input)), nil
}

// IsPrime32 reports whether n is prime. Miller-Rabin with a deterministic
// witness set for 32-bit integers is used; this is set-membership validation,
// not a claim of cryptographic security for N.
func IsPrime32(n uint32) bool {
	if n < 2 {
		return false
	}
	for _, p := range [...]uint32{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37} {
		if n == p {
			return true
		}
		if n%p == 0 {
			return false
		}
	}

	d := uint64(n - 1)
	s := bits.TrailingZeros64(d)
	d >>= s
	for _, a := range [...]uint64{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37} {
		x := powMod32(a%uint64(n), d, uint64(n))
		if x == 1 || x == uint64(n-1) {
			continue
		}
		composite := true
		for r := 1; r < s; r++ {
			x = (x * x) % uint64(n)
			if x == uint64(n-1) {
				composite = false
				break
			}
		}
		if composite {
			return false
		}
	}
	return true
}

func powMod32(base, exponent, modulus uint64) uint64 {
	result := uint64(1)
	base %= modulus
	for exponent > 0 {
		if exponent&1 == 1 {
			result = (result * base) % modulus
		}
		base = (base * base) % modulus
		exponent >>= 1
	}
	return result
}
