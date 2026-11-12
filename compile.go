package netsec

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Mode selects how descriptors whose decoded run leaves the firmware
// address domain are handled.
type Mode int

const (
	// Strict rejects any descriptor whose exact arithmetic falls outside
	// the representable address range or the firmware image.
	Strict Mode = iota

	// ModularDecode reduces the computed start through the representation
	// modulus (2^bits) and then through the image-size modulus, modelling
	// hardware that truncates addresses. Representation wrap, domain wrap
	// and non-injective mappings are reported on the result instead of
	// being rejected.
	ModularDecode
)

// Descriptor is one raw region description emitted by an update payload.
//
// The physical start address is formed as Base + Offset*Scale using exact
// integer arithmetic; Length bytes are written contiguously from that start.
// Base and Scale must be non-negative. Offset may be negative, which can
// drive the start below zero.
type Descriptor struct {
	Name   string
	Base   *big.Int
	Offset *big.Int
	Scale  *big.Int
	Length int64
}

// Region is a protected physical byte range, half-open [Start, End).
type Region struct {
	Name  string
	Start uint64
	End   uint64
}

// Segment is one element of the canonical, pairwise disjoint physical write
// set. Descriptors lists every original descriptor name that writes into
// [Start, End), sorted and de-duplicated: this is the original-description
// chain proving which aliasing descriptions cover the physical range.
type Segment struct {
	Start       uint64
	End         uint64
	Descriptors []string
}

// Violation describes a physical overlap between the actual write set and a
// protected region. Descriptors is the full original-description chain
// responsible for the overlap, including aliasing descriptors that a
// surface-level per-descriptor check would miss.
type Violation struct {
	Region      string
	Start       uint64
	End         uint64
	Descriptors []string
}

// DescriptorResult records the exact arithmetic and wrap behaviour observed
// while compiling one descriptor.
type DescriptorResult struct {
	Name string
	// Start and End are the exact, unreduced values Base+Offset*Scale and
	// Start+Length, before any modulus is applied.
	Start *big.Int
	End   *big.Int
	// Underflow is set when the exact start is negative.
	Underflow bool
	// RepresentationWrap is set when the run crosses the 2^bits
	// representation modulus on the positive side.
	RepresentationWrap bool
	// DomainWrap is set when the decoded run crosses the firmware image
	// boundary (physical modulus D).
	DomainWrap bool
	// NonInjective is set when two logical offsets of this descriptor map
	// to the same physical byte, which for a contiguous run happens exactly
	// when Length exceeds the image size D.
	NonInjective bool
}

// Report is the output of Compile.
type Report struct {
	// WriteSet is the normalized, sorted, pairwise disjoint physical write
	// set, each segment carrying its original-descriptor chain.
	WriteSet []Segment
	// Violations is the sorted intersection of WriteSet with the protected
	// regions.
	Violations []Violation
	// Results holds the per-descriptor exact-arithmetic diagnostics.
	Results []DescriptorResult
}

// CompileError explains why one descriptor (or one protected region) was
// rejected.
type CompileError struct {
	Descriptor string
	Op         string
	Reason     string
}

func (e *CompileError) Error() string {
	if e.Descriptor == "" {
		return fmt.Sprintf("netsec: %s: %s", e.Op, e.Reason)
	}
	return fmt.Sprintf("netsec: %s: %s: %s", e.Op, e.Descriptor, e.Reason)
}

// CompileErrors aggregates every structural or strict-mode error found in one
// Compile call.
type CompileErrors []*CompileError

func (es CompileErrors) Error() string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

type config struct {
	mode Mode
	bits uint
}

// Option configures a Compiler.
type Option func(*config)

// WithModularDecode selects ModularDecode instead of the default Strict mode.
func WithModularDecode() Option {
	return func(c *config) { c.mode = ModularDecode }
}

// withRepresentationBits overrides the width of the integer address
// representation (default 64). It exists for reduced-domain verification;
// production callers leave it untouched.
func withRepresentationBits(bits uint) Option {
	return func(c *config) {
		c.bits = bits
	}
}

// Compiler compiles firmware region descriptors against one firmware image.
type Compiler struct {
	d    *big.Int // image size (physical domain modulus)
	m    *big.Int // representation modulus, 2^bits
	bits uint
	mode Mode
}

// New builds a Compiler for an image of imageSize bytes. In ModularDecode
// mode the image size must divide the representation modulus, i.e. it must be
// a power of two no larger than 2^bits.
func New(imageSize uint64, opts ...Option) (*Compiler, error) {
	if imageSize == 0 {
		return nil, errors.New("netsec: image size must be positive")
	}
	cfg := config{mode: Strict, bits: 64}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.bits == 0 || cfg.bits > 64 {
		return nil, fmt.Errorf("netsec: representation bits %d out of range (1..64)", cfg.bits)
	}
	m := new(big.Int).Lsh(big.NewInt(1), cfg.bits)
	d := new(big.Int).SetUint64(imageSize)
	if cfg.mode == ModularDecode && new(big.Int).Mod(m, d).Sign() != 0 {
		return nil, fmt.Errorf("netsec: modular decode requires image size %d to divide 2^%d", imageSize, cfg.bits)
	}
	return &Compiler{d: d, m: m, bits: cfg.bits, mode: cfg.mode}, nil
}

type piece struct {
	lo, hi *big.Int
	name   string
}

// Compile normalizes the descriptors into the actual physical write set and
// intersects it with the protected regions. In Strict mode any wrapping or
// malformed descriptor produces CompileErrors and a nil report.
func (c *Compiler) Compile(descs []Descriptor, protected []Region) (*Report, error) {
	var errs CompileErrors

	seen := make(map[string]struct{}, len(descs))
	pieces := make([]piece, 0, len(descs)*2)
	results := make([]DescriptorResult, 0, len(descs))

	for i := range descs {
		desc := &descs[i]
		name := desc.Name
		if name == "" {
			name = fmt.Sprintf("descriptor-%d", i)
		}
		if _, dup := seen[name]; dup {
			errs = append(errs, &CompileError{Descriptor: name, Op: "compile", Reason: "duplicate descriptor name"})
			continue
		}
		seen[name] = struct{}{}

		result, ps, err := c.compileOne(name, desc)
		results = append(results, result)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		pieces = append(pieces, ps...)
	}

	domainSize := c.d.Uint64()
	for _, pr := range protected {
		if pr.End <= pr.Start {
			errs = append(errs, &CompileError{Descriptor: pr.Name, Op: "protect", Reason: "region end must be greater than start"})
			continue
		}
		if pr.End > domainSize {
			errs = append(errs, &CompileError{Descriptor: pr.Name, Op: "protect", Reason: "region extends past firmware image"})
		}
	}

	if len(errs) > 0 {
		return nil, errs
	}

	report := &Report{Results: results}
	report.WriteSet = c.sweep(pieces)
	report.Violations = intersect(report.WriteSet, protected)
	return report, nil
}

func (c *Compiler) compileOne(name string, desc *Descriptor) (DescriptorResult, []piece, *CompileError) {
	result := DescriptorResult{Name: name}
	fail := func(reason string) (DescriptorResult, []piece, *CompileError) {
		return result, nil, &CompileError{Descriptor: name, Op: "compile", Reason: reason}
	}

	if desc.Base == nil || desc.Offset == nil || desc.Scale == nil {
		return fail("base, offset and scale must be non-nil")
	}
	if desc.Base.Sign() < 0 {
		return fail("base must be non-negative")
	}
	if desc.Scale.Sign() < 0 {
		return fail("scale must be non-negative")
	}
	if desc.Length <= 0 {
		return fail("length must be positive")
	}

	// Exact integer arithmetic first; range checks happen afterwards.
	// start = base + offset*scale, end = start + length.
	start := new(big.Int).Mul(desc.Offset, desc.Scale)
	start.Add(start, desc.Base)
	length := big.NewInt(desc.Length)
	end := new(big.Int).Add(start, length)
	result.Start = new(big.Int).Set(start)
	result.End = new(big.Int).Set(end)

	if c.mode == Strict {
		if start.Sign() < 0 {
			return fail("computed start is negative (wrap rejected in strict mode)")
		}
		if start.Cmp(c.d) >= 0 || end.Cmp(c.d) > 0 {
			return fail("descriptor run leaves the firmware image (wrap rejected in strict mode)")
		}
		return result, []piece{{lo: new(big.Int).Set(start), hi: new(big.Int).Set(end), name: name}}, nil
	}

	// ModularDecode: record every wrap layer instead of rejecting.
	if start.Sign() < 0 {
		result.Underflow = true
	} else if start.Cmp(c.m) >= 0 || end.Cmp(c.m) > 0 {
		result.RepresentationWrap = true
	}

	// Because D divides M, ((x mod M) mod D) == x mod D: the physical
	// address of the run start is start mod D.
	physStart := new(big.Int).Mod(new(big.Int).Mod(start, c.m), c.d)
	physEnd := new(big.Int).Add(physStart, length)
	// Domain wrap means the exact run crosses an image boundary: either the
	// exact start was already out of domain, or the reduced tail runs past D.
	if start.Sign() < 0 || start.Cmp(c.d) >= 0 || physEnd.Cmp(c.d) > 0 {
		result.DomainWrap = true
	}
	if length.Cmp(c.d) > 0 {
		result.NonInjective = true
	}

	switch {
	case length.Cmp(c.d) >= 0:
		// The run covers the whole image, possibly repeatedly.
		return result, []piece{{lo: big.NewInt(0), hi: new(big.Int).Set(c.d), name: name}}, nil
	case physEnd.Cmp(c.d) <= 0:
		return result, []piece{{lo: physStart, hi: physEnd, name: name}}, nil
	default:
		// One domain wrap: split into the tail and the head of the image.
		return result, []piece{
			{lo: new(big.Int).Set(physStart), hi: new(big.Int).Set(c.d), name: name},
			{lo: big.NewInt(0), hi: new(big.Int).Sub(physEnd, c.d), name: name},
		}, nil
	}
}
