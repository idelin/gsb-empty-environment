package netsec

import "errors"

// Digest is an opaque content digest identifying a snapshot. Values are
// compared byte-for-byte and must be non-empty.
type Digest []byte

// TenantID identifies a tenant. The empty tenant is invalid.
type TenantID string

// BlockID identifies a logical block within a snapshot chain.
type BlockID string

// SnapshotNotFoundError reports that a referenced snapshot manifest is not
// known to the host's ManifestStore.
type SnapshotNotFoundError struct {
	Digest Digest
}

func (e *SnapshotNotFoundError) Error() string {
	return "netsec: snapshot not found: " + digestString(e.Digest)
}

// DigestMismatchError reports that a manifest was stored under a digest that
// does not equal its own declared digest.
type DigestMismatchError struct {
	Requested Digest
	Declared  Digest
}

func (e *DigestMismatchError) Error() string {
	return "netsec: manifest digest mismatch: requested " + digestString(e.Requested) +
		", declared " + digestString(e.Declared)
}

// UnauthorizedError reports that a snapshot or block source belongs to a
// tenant that has not shared it with the importer, or that a share has been
// revoked.
type UnauthorizedError struct {
	// Snapshot is the digest whose authorization failed.
	Snapshot Digest
	// Owner is the tenant that owns the snapshot or block source.
	Owner TenantID
	// Importer is the tenant attempting the import.
	Importer TenantID
}

func (e *UnauthorizedError) Error() string {
	return "netsec: tenant " + string(e.Importer) + " is not authorized for snapshot " +
		digestString(e.Snapshot) + " owned by tenant " + string(e.Owner)
}

// CycleError reports a cycle in the parent (incremental base) graph.
type CycleError struct {
	// On is a snapshot whose parent edge closes a cycle.
	On Digest
	// Parent is the already-visited parent digest referenced by On.
	Parent Digest
}

func (e *CycleError) Error() string {
	return "netsec: cyclic parent reference: " + digestString(e.On) +
		" -> " + digestString(e.Parent)
}

// BlockConflictError reports that one logical block is provided by two
// different sources at the same deterministic priority, so there is no
// unique provider.
type BlockConflictError struct {
	Block  BlockID
	First  BlockSource
	Second BlockSource
	Depth  int
}

func (e *BlockConflictError) Error() string {
	return "netsec: conflicting sources for block " + string(e.Block) +
		" at depth " + itoa(e.Depth)
}

// InvalidSourceError reports that a block references a physical source whose
// snapshot is absent from the authorized closure or owned by a different
// tenant than the source claims.
type InvalidSourceError struct {
	Block  BlockID
	Source BlockSource
	Reason string
}

func (e *InvalidSourceError) Error() string {
	return "netsec: invalid source for block " + string(e.Block) + ": " + e.Reason
}

// InvalidManifestError reports a structurally invalid manifest.
type InvalidManifestError struct {
	Snapshot Digest
	Reason   string
}

func (e *InvalidManifestError) Error() string {
	return "netsec: invalid manifest " + digestString(e.Snapshot) + ": " + e.Reason
}

var (
	// ErrCanceled is returned when the context passed to Verify is canceled.
	ErrCanceled = errors.New("netsec: verification canceled")
)

// BlockSource identifies the physical provider of a logical block.
//
// Snapshot is the digest of the snapshot that physically holds the block and
// Tenant is the tenant that owns that physical snapshot. A source is only
// trustworthy when its snapshot belongs to the verified, authorized closure
// and the manifest of that snapshot names the same owner.
type BlockSource struct {
	Tenant   TenantID
	Snapshot Digest
}

// BlockEntry declares one logical block of a snapshot.
type BlockEntry struct {
	Block  BlockID
	Source BlockSource
}

// ParentRef points at an incremental base snapshot.
type ParentRef struct {
	// Digest is the content digest of the parent snapshot.
	Digest Digest
}

// Manifest is the self-describing snapshot metadata supplied by the host.
type Manifest struct {
	// Digest is the digest under which this manifest is stored. It must match
	// the key used to retrieve the manifest.
	Digest Digest
	// Owner is the tenant that created and owns the snapshot.
	Owner TenantID
	// Blocks lists the logical blocks provided by this snapshot. Block
	// identifiers must be unique within one manifest.
	Blocks []BlockEntry
	// Parents lists the incremental base snapshots this snapshot depends on.
	// Parent digests must be unique within one manifest.
	Parents []ParentRef
}

// ResolvedBlock is the unique provider selected for one logical block.
type ResolvedBlock struct {
	Block  BlockID
	Source BlockSource
	// Depth is the minimum number of parent edges from the imported root to
	// the snapshot that supplies the selected source. Lower depth wins.
	Depth int
}

// Closure is the verified result of an import request.
type Closure struct {
	// Importer is the tenant the closure was verified for.
	Importer TenantID
	// Root is the digest of the requested top-level snapshot.
	Root Digest
	// Snapshots is every snapshot reachable through parent edges, including
	// the root. It is sorted by digest for deterministic host processing.
	Snapshots []Digest
	// Depths records the minimum number of parent edges from the root to
	// each reachable snapshot. The root itself has depth zero.
	Depths map[string]int
	// Blocks maps every logical block in the closure to its unique source.
	Blocks map[BlockID]ResolvedBlock
}

// digestKey renders a digest as a stable string for maps and diagnostics.
func digestString(d Digest) string {
	const hexdigits = "0123456789abcdef"
	if len(d) == 0 {
		return "<empty>"
	}
	out := make([]byte, len(d)*2)
	for i, b := range d {
		out[i*2] = hexdigits[b>>4]
		out[i*2+1] = hexdigits[b&0x0f]
	}
	return string(out)
}

func itoa(n int) string {
	var buf [20]byte
	i := len(buf)
	if n == 0 {
		i--
		buf[i] = '0'
		return string(buf[i:])
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
