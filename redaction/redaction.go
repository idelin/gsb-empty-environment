// Package redaction implements redaction (cut-down) proofs for ordered audit
// logs.
//
// The owner of an ordered log builds a Prover over all entries. Every entry
// gets a commitment that binds the log identifier, its zero-based serial
// number and a hash of its payload. The commitments form the leaves of a
// Merkle tree, and a final proof root additionally binds the log identifier
// and the total number of entries.
//
// An export may redact arbitrary entries:
//   - Hidden entries expose only their commitment.
//   - Public entries expose the original payload, their commitment and a
//     Merkle membership path proving the position of the entry.
//
// Verify reconstructs every leaf in canonical serial order (hidden
// commitments are supplied directly, public commitments are recomputed from
// the payload) and rebuilds the root, so omitted, duplicated, reordered or
// spliced entries cannot validate. Log identifiers are mixed into every hash
// with domain-separation prefixes, so proofs from different logs cannot be
// spliced together even when payloads are identical.
package redaction

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
)

const hashSize = sha256.Size

// Domain-separation prefixes make commitments, tree leaves, internal nodes
// and the final root mutually unambiguous inside SHA-256.
var (
	domainCommitment = []byte{0x01}
	domainLeaf       = []byte{0x02}
	domainNode       = []byte{0x03}
	domainRoot       = []byte{0x04}
)

// Sentinel errors returned by the package; wrapped errors preserve them for
// errors.Is.
var (
	ErrEmptyLog           = errors.New("redaction: log must contain at least one entry")
	ErrInvalidBundle      = errors.New("redaction: invalid bundle")
	ErrRootSize           = errors.New("redaction: root must be 32 bytes")
	ErrIndexOutOfOrder    = errors.New("redaction: items must appear in index order with no gaps or duplicates")
	ErrInvalidCommitment  = errors.New("redaction: commitment must be 32 bytes")
	ErrHiddenItemLeak     = errors.New("redaction: hidden item must not carry payload or merkle path")
	ErrCommitmentMismatch = errors.New("redaction: commitment does not bind the declared index and payload")
	ErrPathLength         = errors.New("redaction: unexpected merkle path length")
	ErrInvalidSibling     = errors.New("redaction: merkle path sibling must be 32 bytes")
	ErrRootMismatch       = errors.New("redaction: recomputed root does not match the exported root")
)

func writeUint64(dst []byte, value int) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(value))
	return append(dst, buf[:]...)
}

// Commitment returns the entry commitment H(domain ‖ len(logID) ‖ logID ‖
// index ‖ len(payload) ‖ payload). It binds the entry to one log and to one
// serial number, so moving a payload to another position or another log
// changes the commitment.
func Commitment(logID []byte, index int, payload []byte) []byte {
	h := sha256.New()
	h.Write(domainCommitment)
	h.Write(writeUint64(nil, len(logID)))
	h.Write(logID)
	h.Write(writeUint64(nil, index))
	h.Write(writeUint64(nil, len(payload)))
	h.Write(payload)
	return h.Sum(nil)
}

func leafHash(commitment []byte) []byte {
	h := sha256.New()
	h.Write(domainLeaf)
	h.Write(commitment)
	return h.Sum(nil)
}

func nodeHash(left, right []byte) []byte {
	h := sha256.New()
	h.Write(domainNode)
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

func rootHash(logID []byte, total int, merkleRoot []byte) []byte {
	h := sha256.New()
	h.Write(domainRoot)
	h.Write(writeUint64(nil, len(logID)))
	h.Write(logID)
	h.Write(writeUint64(nil, total))
	h.Write(merkleRoot)
	return h.Sum(nil)
}

// pathLength is the Merkle depth for a tree with n leaves. A single entry
// has an empty membership path.
func pathLength(n int) int {
	return bits.Len(uint(n - 1))
}

// buildLevels returns the Merkle tree with leaves at level 0. At every level
// with an odd number of nodes the last node is duplicated, which keeps the
// tree balanced and deterministic.
func buildLevels(leaves [][]byte) [][][]byte {
	levels := [][][]byte{leaves}
	for len(levels[len(levels)-1]) > 1 {
		current := levels[len(levels)-1]
		next := make([][]byte, (len(current)+1)/2)
		for i := range next {
			left := current[2*i]
			right := left
			if 2*i+1 < len(current) {
				right = current[2*i+1]
			}
			next[i] = nodeHash(left, right)
		}
		levels = append(levels, next)
	}
	return levels
}

// Prover holds the complete ordered log and its precomputed Merkle tree.
type Prover struct {
	logID   []byte
	payload [][]byte
	levels  [][][]byte
	root    []byte
}

// NewProver copies payloads and builds the Merkle tree and the total-bound
// proof root. logID should be unique per log to defeat cross-log splicing.
func NewProver(logID string, payloads [][]byte) (*Prover, error) {
	if len(payloads) == 0 {
		return nil, ErrEmptyLog
	}

	id := []byte(logID)
	copied := make([][]byte, len(payloads))
	leaves := make([][]byte, len(payloads))
	for i, payload := range payloads {
		entry := append([]byte(nil), payload...)
		copied[i] = entry
		leaves[i] = leafHash(Commitment(id, i, entry))
	}

	levels := buildLevels(leaves)
	merkleRoot := levels[len(levels)-1][0]

	return &Prover{
		logID:   append([]byte(nil), id...),
		payload: copied,
		levels:  levels,
		root:    rootHash(id, len(payloads), merkleRoot),
	}, nil
}

// LogID returns the log identifier bound into every hash.
func (p *Prover) LogID() string {
	return string(p.logID)
}

// Total returns the number of log entries.
func (p *Prover) Total() int {
	return len(p.payload)
}

// Root returns a copy of the total-bound proof root.
func (p *Prover) Root() []byte {
	return append([]byte(nil), p.root...)
}

// CommitmentAt returns a copy of the commitment of entry index.
func (p *Prover) CommitmentAt(index int) ([]byte, error) {
	if index < 0 || index >= len(p.payload) {
		return nil, fmt.Errorf("%w: index %d of %d", ErrInvalidBundle, index, len(p.payload))
	}
	return Commitment(p.logID, index, p.payload[index]), nil
}

// merklePath returns copies of the sibling hashes from the leaf at index up
// to the tree root. Left/right placement is derived from the index bits, so
// the path does not need explicit direction flags.
func (p *Prover) merklePath(index int) [][]byte {
	path := make([][]byte, 0, pathLength(len(p.payload)))
	position := index
	for level := 0; level < len(p.levels)-1; level++ {
		nodes := p.levels[level]
		sibling := position ^ 1
		if sibling >= len(nodes) {
			// Odd level: the last node was duplicated, so the node is its
			// own sibling.
			sibling = position
		}
		path = append(path, append([]byte(nil), nodes[sibling]...))
		position >>= 1
	}
	return path
}

// Item is one entry of an exported bundle.
//
// A hidden item carries only Index and Commitment. A public item additionally
// carries the original Payload and the MerklePath proving its position.
type Item struct {
	Index      int      `json:"index"`
	Hidden     bool     `json:"hidden"`
	Commitment []byte   `json:"commitment"`
	Payload    []byte   `json:"payload,omitempty"`
	MerklePath [][]byte `json:"merkle_path,omitempty"`
}

// Bundle is a redacted export of a log. It serializes directly with
// encoding/json (byte slices become base64 strings).
type Bundle struct {
	LogID string `json:"log_id"`
	Total int    `json:"total"`
	Root  []byte `json:"root"`
	Items []Item `json:"items"`
}

// Export builds a redacted bundle. isPublic reports whether entry index must
// be disclosed; a nil function hides every entry. Items are emitted in
// canonical index order.
func (p *Prover) Export(isPublic func(index int) bool) *Bundle {
	items := make([]Item, len(p.payload))
	for i := range p.payload {
		commitment := Commitment(p.logID, i, p.payload[i])
		if isPublic != nil && isPublic(i) {
			items[i] = Item{
				Index:      i,
				Hidden:     false,
				Commitment: commitment,
				Payload:    append([]byte(nil), p.payload[i]...),
				MerklePath: p.merklePath(i),
			}
			continue
		}
		items[i] = Item{
			Index:      i,
			Hidden:     true,
			Commitment: commitment,
		}
	}

	return &Bundle{
		LogID: string(p.logID),
		Total: len(p.payload),
		Root:  append([]byte(nil), p.root...),
		Items: items,
	}
}

// Verify checks a redacted bundle:
//
//   - Items must cover every serial 0..Total-1 exactly once, in order.
//   - Every commitment must be 32 bytes; hidden items must carry nothing
//     else, public commitments must equal the recomputed commitment of the
//     declared index and payload.
//   - The full ordered leaf layer is rebuilt from all commitments and the
//     Merkle root is recomputed, so hidden commitments are pinned to their
//     positions as well.
//   - The final root must match, proving the log identifier and total count
//     in addition to the tree.
//   - Every public membership path must resolve to the same Merkle root.
func Verify(bundle *Bundle) error {
	if bundle == nil {
		return fmt.Errorf("%w: nil bundle", ErrInvalidBundle)
	}
	if len(bundle.Root) != hashSize {
		return fmt.Errorf("%w: got %d bytes", ErrRootSize, len(bundle.Root))
	}
	if bundle.Total != len(bundle.Items) || bundle.Total == 0 {
		return fmt.Errorf("%w: total=%d items=%d", ErrInvalidBundle, bundle.Total, len(bundle.Items))
	}

	logID := []byte(bundle.LogID)
	leaves := make([][]byte, bundle.Total)

	for i, item := range bundle.Items {
		if item.Index != i {
			return fmt.Errorf("%w: slot %d carries index %d", ErrIndexOutOfOrder, i, item.Index)
		}
		if len(item.Commitment) != hashSize {
			return fmt.Errorf("%w: index %d", ErrInvalidCommitment, i)
		}
		if item.Hidden {
			if len(item.Payload) != 0 || len(item.MerklePath) != 0 {
				return fmt.Errorf("%w: index %d", ErrHiddenItemLeak, i)
			}
		} else {
			recomputed := Commitment(logID, i, item.Payload)
			if !bytes.Equal(recomputed, item.Commitment) {
				return fmt.Errorf("%w: index %d", ErrCommitmentMismatch, i)
			}
		}
		leaves[i] = leafHash(item.Commitment)
	}

	levels := buildLevels(leaves)
	merkleRoot := levels[len(levels)-1][0]
	expectedRoot := rootHash(logID, bundle.Total, merkleRoot)
	if !bytes.Equal(expectedRoot, bundle.Root) {
		return fmt.Errorf("%w: log %q total %d", ErrRootMismatch, bundle.LogID, bundle.Total)
	}

	wantPathLength := pathLength(bundle.Total)
	for i, item := range bundle.Items {
		if item.Hidden {
			continue
		}
		if len(item.MerklePath) != wantPathLength {
			return fmt.Errorf("%w: index %d has %d siblings, want %d",
				ErrPathLength, i, len(item.MerklePath), wantPathLength)
		}
		current := leafHash(item.Commitment)
		for level, sibling := range item.MerklePath {
			if len(sibling) != hashSize {
				return fmt.Errorf("%w: index %d level %d", ErrInvalidSibling, i, level)
			}
			if (i>>level)&1 == 1 {
				current = nodeHash(sibling, current)
			} else {
				current = nodeHash(current, sibling)
			}
		}
		if !bytes.Equal(current, merkleRoot) {
			return fmt.Errorf("%w: public index %d path", ErrRootMismatch, i)
		}
	}

	return nil
}
