package netsec

import (
	"bytes"
	"errors"
	"fmt"
)

var (
	ErrNilProof          = errors.New("netsec: proof is nil")
	ErrMalformedHash     = errors.New("netsec: hash has invalid length")
	ErrProofTooDeep      = errors.New("netsec: proof path exceeds the index bit length")
	ErrInvalidVRFOutput  = errors.New("netsec: VRF output must be 32 bytes")
	ErrMissingLeaf       = errors.New("netsec: inclusion proof has no terminal leaf")
	ErrLeafIndexMismatch = errors.New("netsec: inclusion leaf index does not equal the VRF output")
	ErrLeafMatchesVRF    = errors.New("netsec: non-inclusion leaf index equals the VRF output")
	ErrPrefixMismatch    = errors.New("netsec: terminal leaf does not share the walked prefix")
	ErrRootMismatch      = errors.New("netsec: recomputed root does not match the epoch root")
)

// VRFVerifier is injected by the caller: it verifies the VRF proof for a
// user label and returns the 32-byte VRF output that indexes the tree.
type VRFVerifier interface {
	Verify(label, proof []byte) ([]byte, error)
}

// VRFVerifierFunc adapts a plain function to VRFVerifier.
type VRFVerifierFunc func(label, proof []byte) ([]byte, error)

// Verify implements VRFVerifier.
func (f VRFVerifierFunc) Verify(label, proof []byte) ([]byte, error) {
	return f(label, proof)
}

// Leaf is a terminal leaf of the prefix tree.
type Leaf struct {
	// Index is the full 256-bit VRF output the leaf was inserted under.
	Index [HashSize]byte
	// Value is the committed key material.
	Value []byte
}

// Proof is a prefix-tree proof for one user label. The VRF output derived
// from Label is walked bit by bit, MSB first, from the root down to a
// terminal node at depth len(Siblings). Siblings[d] is the sibling hash
// used when recomputing the parent at depth d, i.e. Siblings[0] sits just
// below the root and Siblings[len(Siblings)-1] neighbours the terminal.
type Proof struct {
	Label    []byte
	VRFProof []byte
	// Leaf is the terminal leaf; nil means the proof terminates in an
	// empty subtree (non-inclusion only).
	Leaf     *Leaf
	Siblings [][]byte
}

func (p *Proof) depth() int { return len(p.Siblings) }

func checkShape(root []byte, p *Proof) error {
	if p == nil {
		return ErrNilProof
	}
	if len(root) != HashSize {
		return fmt.Errorf("%w: epoch root is %d bytes", ErrMalformedHash, len(root))
	}
	if p.depth() > IndexBits {
		return ErrProofTooDeep
	}
	for i, s := range p.Siblings {
		if len(s) != HashSize {
			return fmt.Errorf("%w: sibling %d is %d bytes", ErrMalformedHash, i, len(s))
		}
	}
	return nil
}

func vrfIndex(vrf VRFVerifier, p *Proof) ([HashSize]byte, error) {
	var index [HashSize]byte
	out, err := vrf.Verify(p.Label, p.VRFProof)
	if err != nil {
		return index, fmt.Errorf("netsec: VRF verification failed: %w", err)
	}
	if len(out) != HashSize {
		return index, ErrInvalidVRFOutput
	}
	copy(index[:], out)
	return index, nil
}

// recomputeRoot walks from the terminal hash at the given depth back up to
// the root, choosing child order from the index bits.
func recomputeRoot(index *[HashSize]byte, depth int, terminal []byte, siblings [][]byte) []byte {
	h := terminal
	for d := depth - 1; d >= 0; d-- {
		if bit(index[:], d) == 0 {
			h = hashNode(d, h, siblings[d])
		} else {
			h = hashNode(d, siblings[d], h)
		}
	}
	return h
}

// VerifyInclusion verifies an inclusion proof against the epoch root and
// returns the committed key material. The terminal leaf index must equal
// the VRF output of the label.
func VerifyInclusion(vrf VRFVerifier, root []byte, p *Proof) ([]byte, error) {
	if err := checkShape(root, p); err != nil {
		return nil, err
	}
	index, err := vrfIndex(vrf, p)
	if err != nil {
		return nil, err
	}
	if p.Leaf == nil {
		return nil, ErrMissingLeaf
	}
	if p.Leaf.Index != index {
		return nil, ErrLeafIndexMismatch
	}
	terminal := hashLeaf(p.depth(), p.Leaf.Index[:], p.Leaf.Value)
	if !bytes.Equal(recomputeRoot(&index, p.depth(), terminal, p.Siblings), root) {
		return nil, ErrRootMismatch
	}
	return p.Leaf.Value, nil
}

// VerifyNonInclusion verifies a non-inclusion proof against the epoch
// root. The proof must terminate either in an empty subtree, or in a leaf
// whose index differs from the VRF output while sharing the walked prefix.
func VerifyNonInclusion(vrf VRFVerifier, root []byte, p *Proof) error {
	if err := checkShape(root, p); err != nil {
		return err
	}
	index, err := vrfIndex(vrf, p)
	if err != nil {
		return err
	}
	depth := p.depth()
	var terminal []byte
	if p.Leaf == nil {
		terminal = hashEmpty(depth)
	} else {
		if p.Leaf.Index == index {
			return ErrLeafMatchesVRF
		}
		if !prefixEqual(p.Leaf.Index[:], index[:], depth) {
			return ErrPrefixMismatch
		}
		terminal = hashLeaf(depth, p.Leaf.Index[:], p.Leaf.Value)
	}
	if !bytes.Equal(recomputeRoot(&index, depth, terminal, p.Siblings), root) {
		return ErrRootMismatch
	}
	return nil
}
