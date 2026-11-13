package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

// MaxPages is the hard limit on guest memory pages.
const MaxPages = 64

// PageID identifies a guest memory page.  Valid values are [0, MaxPages).
type PageID uint8

// VMID is the stable identity of a virtual machine.
type VMID string

// InstanceID identifies one concrete incarnation of a VM.  A migration receipt
// is bound to both the source and target incarnation so a later incarnation
// cannot replay an old hand-off.
type InstanceID string

// Generation is a per-page write generation.  It is incremented, atomically
// with the write itself, on every source write.
type Generation uint64

// Epoch is the global logical clock attached to a migration snapshot.  It is
// advanced for every pre-copy round and once more for the frozen final cutover.
type Epoch uint64

// Digest is the SHA-256 digest of a page, manifest or device state.
type Digest [sha256.Size]byte

// DigestBytes returns the SHA-256 digest of data.
func DigestBytes(data []byte) Digest {
	return DigestFromSum(sha256.Sum256(data))
}

// DigestFromSum converts a raw SHA-256 sum into a Digest.
func DigestFromSum(sum [sha256.Size]byte) Digest {
	return Digest(sum)
}

// Diget is a short compatibility alias for DigestFromSum.
//
// Deprecated: use DigestFromSum.
func Diget(sum [sha256.Size]byte) Digest {
	return DigestFromSum(sum)
}

// String returns the lower-case hexadecimal form of the digest.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// Hex is an alias for String.
func (d Digest) Hex() string {
	return d.String()
}

// IsZero reports whether the digest is the all-zero value.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// ParseDigest decodes a hexadecimal SHA-256 digest.
func ParseDigest(s string) (Digest, error) {
	var d Digest
	if len(s) != sha256.Size*2 {
		return d, fmt.Errorf("%w: want %d hex characters, got %d", ErrInvalidDigest, sha256.Size*2, len(s))
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return d, fmt.Errorf("%w: %v", ErrInvalidDigest, err)
	}
	copy(d[:], raw)
	return d, nil
}

// WriteKind records the origin of a source write.  vCPU and device DMA writes
// are both blocked by a freeze and both count as in-flight writes while the
// freeze waits for quiescence.
type WriteKind string

const (
	WriteVCPU WriteKind = "vcpu"
	WriteDMA  WriteKind = "dma"
)

// PageSnapshot is an immutable cache-line-sized view of one source page.
type PageSnapshot struct {
	Page       PageID     `json:"page"`
	VM         VMID       `json:"vm"`
	Generation Generation `json:"generation"`
	Digest     Digest     `json:"digest"`
	Data       []byte     `json:"data"`
}

// PageRecord is the unit transferred to the target quarantine.  It contains
// exactly the fields required by the copy protocol: page number, generation,
// digest and bytes.
type PageRecord struct {
	Page       PageID     `json:"page"`
	VM         VMID       `json:"vm"`
	Generation Generation `json:"generation"`
	Digest     Digest     `json:"digest"`
	Data       []byte     `json:"data"`
}

// VerifyDigest recomputes the content digest of a record.
func (r PageRecord) VerifyDigest() error {
	if got := DigestBytes(r.Data); got != r.Digest {
		return &DigestMismatchError{Context: "page record", Got: got, Want: r.Digest}
	}
	return nil
}

// Clone returns a deep copy of a record.
func (r PageRecord) Clone() PageRecord {
	r.Data = append([]byte(nil), r.Data...)
	return r
}

// PageInfo is the externally visible state of one source page.
type PageInfo struct {
	Page       PageID     `json:"page"`
	VM         VMID       `json:"vm"`
	Generation Generation `json:"generation"`
	Digest     Digest     `json:"digest"`
	Dirty      bool       `json:"dirty"`
	Valid      bool       `json:"valid"`
}

// DirtySnapshot is the output of a pre-copy round snapshot.  Pages contains
// only pages that were dirty at snapshot time.
type DirtySnapshot struct {
	Epoch      Epoch              `json:"epoch"`
	VM         VMID               `json:"vm"`
	Pages      []PageSnapshot     `json:"pages"`
	PageIDs    []PageID           `json:"page_ids"`
	All        [MaxPages]PageInfo `json:"all"`
	Frozen     bool               `json:"frozen"`
	SnapshotID uint64             `json:"snapshot_id"`
}

// FreezePoint is the state captured after the source is frozen and every
// pre-freeze in-flight write has completed.
type FreezePoint struct {
	Epoch Epoch              `json:"epoch"`
	VM    VMID               `json:"vm"`
	Dirty []PageSnapshot     `json:"dirty"`
	All   [MaxPages]PageInfo `json:"all"`
}

// Clone returns a deep copy of a freeze point.
func (f FreezePoint) Clone() FreezePoint {
	out := f
	out.Dirty = make([]PageSnapshot, len(f.Dirty))
	for i, p := range f.Dirty {
		out.Dirty[i] = p
		out.Dirty[i].Data = append([]byte(nil), p.Data...)
	}
	return out
}

// PageGenerations returns the generation of every page at the freeze point.
func (f FreezePoint) PageGenerations() [MaxPages]Generation {
	var out [MaxPages]Generation
	for i := 0; i < MaxPages; i++ {
		out[i] = f.All[i].Generation
	}
	return out
}

// ManifestEntries returns the freeze point's manifest entries.
func (f FreezePoint) ManifestEntries() [MaxPages]PageManifestEntry {
	var out [MaxPages]PageManifestEntry
	for i := 0; i < MaxPages; i++ {
		out[i] = PageManifestEntry{
			Page:       PageID(i),
			Generation: f.All[i].Generation,
			Digest:     f.All[i].Digest,
		}
	}
	return out
}

// DMARegion describes a device DMA window that was quiesced at cutover.
type DMARegion struct {
	Device string `json:"device"`
	Start  uint64 `json:"start"`
	Length uint64 `json:"length"`
}

// DeviceState is the deterministic device state captured after DMA and vCPU
// writes are frozen.  State may carry an opaque, host-defined policy/key blob.
// Digest binds it into the migration manifest and receipt.
type DeviceState struct {
	DMARegions    []DMARegion `json:"dma_regions,omitempty"`
	Devices       []string    `json:"devices,omitempty"`
	PolicyVersion uint64      `json:"policy_version"`
	KeyEpoch      uint64      `json:"key_epoch"`
	State         []byte      `json:"state,omitempty"`
}

// Clone returns a deep copy of device state.
func (d DeviceState) Clone() DeviceState {
	out := d
	out.DMARegions = append([]DMARegion(nil), d.DMARegions...)
	out.Devices = append([]string(nil), d.Devices...)
	out.State = append([]byte(nil), d.State...)
	return out
}

// Digest returns a stable SHA-256 digest of the device state.
func (d DeviceState) Digest() Digest {
	h := sha256.New()
	cw := canonicalWriter{h: h}
	cw.text("netsec/q0142/device-state/v1")
	devices := sortedCopy(d.Devices)
	cw.uint64(uint64(len(devices)))
	for _, device := range devices {
		cw.text(device)
	}
	regions := append([]DMARegion(nil), d.DMARegions...)
	sort.Slice(regions, func(i, j int) bool {
		if regions[i].Start != regions[j].Start {
			return regions[i].Start < regions[j].Start
		}
		if regions[i].Length != regions[j].Length {
			return regions[i].Length < regions[j].Length
		}
		return regions[i].Device < regions[j].Device
	})
	cw.uint64(uint64(len(regions)))
	for _, r := range regions {
		cw.text(r.Device)
		cw.uint64(r.Start)
		cw.uint64(r.Length)
	}
	cw.uint64(d.PolicyVersion)
	cw.uint64(d.KeyEpoch)
	cw.bytes(d.State)
	var out Digest
	copy(out[:], h.Sum(nil))
	return out
}

// PageManifestEntry is one entry of the all-page final manifest.
type PageManifestEntry struct {
	Page       PageID     `json:"page"`
	Generation Generation `json:"generation"`
	Digest     Digest     `json:"digest"`
}

// Manifest is the complete, final description of the migrated VM.  It includes
// every page (invalid pages have generation zero) and the device-state digest.
// Root is derived from the canonical encoding of this structure.
type Manifest struct {
	VM             VMID                        `json:"vm"`
	SourceInstance InstanceID                  `json:"source_instance"`
	TargetInstance InstanceID                  `json:"target_instance"`
	Epoch          Epoch                       `json:"epoch"`
	Pages          [MaxPages]PageManifestEntry `json:"pages"`
	DeviceDigest   Digest                      `json:"device_digest"`
	DeviceState    *DeviceState                `json:"device_state,omitempty"`
}

// Root computes the canonical manifest root.
func (m Manifest) Root() Digest {
	h := sha256.New()
	cw := canonicalWriter{h: h}
	cw.text("netsec/q0142/manifest/v1")
	cw.text(string(m.VM))
	cw.text(string(m.SourceInstance))
	cw.text(string(m.TargetInstance))
	cw.uint64(uint64(m.Epoch))
	for i := 0; i < MaxPages; i++ {
		p := m.Pages[i]
		// The array index is the canonical page number.  This keeps the root
		// stable for manifests built sparsely by callers.
		cw.byte(byte(i))
		if p.Generation == 0 {
			cw.byte(0)
			cw.uint64(0)
			cw.digest(Digest{})
			continue
		}
		cw.byte(1)
		cw.uint64(uint64(p.Generation))
		cw.digest(p.Digest)
	}
	cw.digest(m.DeviceDigest)
	var out Digest
	copy(out[:], h.Sum(nil))
	return out
}

// PageGenerations returns all page generations in page order.
func (m Manifest) PageGenerations() [MaxPages]Generation {
	var out [MaxPages]Generation
	for i := 0; i < MaxPages; i++ {
		out[i] = m.Pages[i].Generation
	}
	return out
}

// Validate performs structural validation.  Root and receipt binding are
// checked by the target.
func (m Manifest) Validate() error {
	if m.VM == "" {
		return fmt.Errorf("%w: empty VM identity", ErrInvalidManifest)
	}
	if m.SourceInstance == "" || m.TargetInstance == "" {
		return fmt.Errorf("%w: empty instance identity", ErrInvalidManifest)
	}
	if m.SourceInstance == m.TargetInstance {
		return fmt.Errorf("%w: source and target instances are identical", ErrInvalidManifest)
	}
	for i := 0; i < MaxPages; i++ {
		p := m.Pages[i]
		if int(p.Page) != i {
			// An entirely zero entry is an unset page.  If any field is
			// populated, the page identity must match its index.
			if p.Generation == 0 && p.Digest.IsZero() && p.Page == 0 {
				continue
			}
			return fmt.Errorf("%w: page %d stored at index %d", ErrInvalidManifest, p.Page, i)
		}
		if p.Generation == 0 && !p.Digest.IsZero() {
			return fmt.Errorf("%w: invalid page %d has a digest", ErrInvalidManifest, p.Page)
		}
	}
	return nil
}

// Receipt is the host-signed authorization that lets the target expose its
// quarantine and start.  Signature covers the canonical receipt payload.
type Receipt struct {
	VM             VMID                 `json:"vm"`
	SourceInstance InstanceID           `json:"source_instance"`
	TargetInstance InstanceID           `json:"target_instance"`
	FinalEpoch     Epoch                `json:"final_epoch"`
	Pages          [MaxPages]Generation `json:"pages"`
	DeviceDigest   Digest               `json:"device_digest"`
	ManifestRoot   Digest               `json:"manifest_root"`
	Signature      []byte               `json:"signature"`
}

// Payload returns the canonical bytes covered by Signature.
func (r Receipt) Payload() []byte {
	h := sha256.New()
	cw := canonicalWriter{h: h}
	cw.text("netsec/q0142/receipt/v1")
	cw.text(string(r.VM))
	cw.text(string(r.SourceInstance))
	cw.text(string(r.TargetInstance))
	cw.uint64(uint64(r.FinalEpoch))
	for i := 0; i < MaxPages; i++ {
		cw.uint64(uint64(r.Pages[i]))
	}
	cw.digest(r.DeviceDigest)
	cw.digest(r.ManifestRoot)
	return append([]byte(nil), h.Sum(nil)...)
}

// Digest is the digest of the signed receipt payload (the signature is not
// recursive).
func (r Receipt) Digest() Digest {
	sum := sha256.Sum256(r.Payload())
	return Diget(sum)
}

// Clone returns a deep copy of the receipt.
func (r Receipt) Clone() Receipt {
	r.Signature = append([]byte(nil), r.Signature...)
	return r
}

// StartupConfirmation is returned by the target after it has verified the
// receipt and exposed the quarantine as live memory.  The source must validate
// it before terminating.
type StartupConfirmation struct {
	VM             VMID                 `json:"vm"`
	SourceInstance InstanceID           `json:"source_instance"`
	TargetInstance InstanceID           `json:"target_instance"`
	FinalEpoch     Epoch                `json:"final_epoch"`
	Pages          [MaxPages]Generation `json:"pages"`
	DeviceDigest   Digest               `json:"device_digest"`
	ManifestRoot   Digest               `json:"manifest_root"`
	ReceiptDigest  Digest               `json:"receipt_digest"`
}

// Clone returns a copy of the confirmation.
func (c StartupConfirmation) Clone() StartupConfirmation {
	return c
}

// Handoff groups everything the host sends to the target.
type Handoff struct {
	Manifest     Manifest    `json:"manifest"`
	ManifestRoot Digest      `json:"manifest_root"`
	Receipt      Receipt     `json:"receipt"`
	FreezePoint  FreezePoint `json:"freeze_point"`
}

// Clone returns a deep copy of the handoff.
func (h Handoff) Clone() Handoff {
	out := h
	out.Manifest = h.Manifest
	if h.Manifest.DeviceState != nil {
		device := h.Manifest.DeviceState.Clone()
		out.Manifest.DeviceState = &device
	}
	out.Receipt = h.Receipt.Clone()
	out.FreezePoint = h.FreezePoint.Clone()
	return out
}

// DigestMismatchError reports a failed content verification.
type DigestMismatchError struct {
	Context string
	Got     Digest
	Want    Digest
}

func (e *DigestMismatchError) Error() string {
	return fmt.Sprintf("%s digest mismatch: got %s, want %s", e.Context, e.Got, e.Want)
}

func (e *DigestMismatchError) Is(target error) bool {
	return target == ErrDigestMismatch
}

// PageMismatch describes one page whose target state does not match the frozen
// manifest.  It is the concise witness used when a stale target is refused.
type PageMismatch struct {
	Page               PageID     `json:"page"`
	TargetGeneration   Generation `json:"target_generation"`
	ExpectedGeneration Generation `json:"expected_generation"`
	TargetDigest       Digest     `json:"target_digest"`
	ExpectedDigest     Digest     `json:"expected_digest"`
	Missing            bool       `json:"missing"`
}

// StaleStartupError is returned when the target cannot prove that every page
// is at least equal to the source freeze point.  It is deliberately an error:
// a target with an old page generation must never become visible.
type StaleStartupError struct {
	VM             VMID           `json:"vm"`
	TargetInstance InstanceID     `json:"target_instance"`
	FinalEpoch     Epoch          `json:"final_epoch"`
	ManifestRoot   Digest         `json:"manifest_root"`
	Mismatches     []PageMismatch `json:"mismatches"`
}

func (e *StaleStartupError) Error() string {
	if len(e.Mismatches) == 0 {
		return "target startup refused: stale target state"
	}
	m := e.Mismatches[0]
	if m.Missing {
		return fmt.Sprintf("target startup refused: page %d missing, source freeze generation %d", m.Page, m.ExpectedGeneration)
	}
	switch {
	case m.TargetGeneration < m.ExpectedGeneration:
		return fmt.Sprintf("target startup refused: page %d target generation %d is below source freeze generation %d",
			m.Page, m.TargetGeneration, m.ExpectedGeneration)
	case m.TargetGeneration > m.ExpectedGeneration:
		return fmt.Sprintf("target startup refused: page %d target generation %d is above source freeze generation %d",
			m.Page, m.TargetGeneration, m.ExpectedGeneration)
	default:
		return fmt.Sprintf("target startup refused: page %d content digest does not match the frozen manifest", m.Page)
	}
}

func (e *StaleStartupError) Is(target error) bool {
	return target == ErrStaleStartup
}

// ShortestWitness returns the mismatch with the lowest page number.  Page
// order is a canonical, shortest textual witness of the first divergence.
func (e *StaleStartupError) ShortestWitness() PageMismatch {
	if len(e.Mismatches) == 0 {
		return PageMismatch{}
	}
	w := e.Mismatches[0]
	for _, m := range e.Mismatches[1:] {
		if m.Page < w.Page {
			w = m
		}
	}
	return w
}

var (
	// ErrPageOutOfRange means a PageID was outside [0, MaxPages).
	ErrPageOutOfRange = errors.New("netsec: page id out of range")
	// ErrPageNotPresent means the page has never been written.
	ErrPageNotPresent = errors.New("netsec: page not present")
	// ErrFrozen means new source writes are blocked during cutover.
	ErrFrozen = errors.New("netsec: source is frozen")
	// ErrNotFrozen means a thaw was requested for a running source.
	ErrNotFrozen = errors.New("netsec: source is not frozen")
	// ErrSealed means a successful cutover has sealed the freeze; only the
	// migration host may thaw it through a validated abort.
	ErrSealed = errors.New("netsec: source freeze is sealed by cutover")
	// ErrTerminated means the confirmed source may never be used again.
	ErrTerminated = errors.New("netsec: source is permanently terminated")
	// ErrNotTerminated means source memory cannot be released yet.
	ErrNotTerminated = errors.New("netsec: source has not been terminated")
	// ErrReleased means the source memory has been released.
	ErrReleased = errors.New("netsec: source memory has been released")
	// ErrVMNotOwner means a page/record belongs to another VM.
	ErrVMNotOwner = errors.New("netsec: page is owned by another VM")
	// ErrQuarantineHidden means the target quarantine is not visible.
	ErrQuarantineHidden = errors.New("netsec: target quarantine is not visible")
	// ErrTargetAborted means the target quarantine was invalidated.
	ErrTargetAborted = errors.New("netsec: target quarantine was aborted")
	// ErrAlreadyStarted means the target is already live.
	ErrAlreadyStarted = errors.New("netsec: target is already started")
	// ErrNotStarted means an operation needs a live target.
	ErrNotStarted = errors.New("netsec: target has not started")
	// ErrStalePage means a transferred page is older than the quarantined page.
	ErrStalePage = errors.New("netsec: stale page record")
	// ErrConflictingPage means equal generations have different content.
	ErrConflictingPage = errors.New("netsec: conflicting page record")
	// ErrNotStaged means the target has no quarantine copy of the page.
	ErrNotStaged = errors.New("netsec: page is not staged")
	// ErrDigestMismatch is a content verification failure.
	ErrDigestMismatch = errors.New("netsec: digest mismatch")
	// ErrStaleStartup means target startup was refused because of stale state.
	ErrStaleStartup = errors.New("netsec: stale target startup")
	// ErrInvalidManifest means the manifest is structurally invalid.
	ErrInvalidManifest = errors.New("netsec: invalid manifest")
	// ErrInvalidReceipt means the receipt fields are inconsistent.
	ErrInvalidReceipt = errors.New("netsec: invalid receipt")
	// ErrBadSignature means the host signature did not verify.
	ErrBadSignature = errors.New("netsec: bad host signature")
	// ErrUntrustedHost means the target has no provisioned host public key.
	ErrUntrustedHost = errors.New("netsec: target has no trusted host key")
	// ErrInvalidDigest means a hex digest could not be parsed.
	ErrInvalidDigest = errors.New("netsec: invalid digest")
	// ErrInvalidState means a migration operation is not valid in this state.
	ErrInvalidState = errors.New("netsec: invalid migration state")
	// ErrTargetStarted means an abort cannot be performed after target start.
	ErrTargetStarted = errors.New("netsec: target already started; reconcile confirmation instead")
	// ErrNoHandoff means no cutover handoff exists.
	ErrNoHandoff = errors.New("netsec: no cutover handoff available")
	// ErrInjectedFault is wrapped by fault-injection errors.
	ErrInjectedFault = errors.New("netsec: injected fault")
)

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// canonicalWriter makes length-prefixed encodings unambiguous, preventing
// field-boundary confusion in manifests, receipts and device digests.
type canonicalWriter struct {
	h interface {
		Write([]byte) (int, error)
	}
}

func (w canonicalWriter) byte(v byte) {
	_, _ = w.h.Write([]byte{v})
}

func (w canonicalWriter) uint64(v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	_, _ = w.h.Write(buf[:])
}

func (w canonicalWriter) text(s string) {
	_, _ = w.h.Write([]byte{0})
	w.uint64(uint64(len(s)))
	_, _ = w.h.Write([]byte(s))
}

func (w canonicalWriter) bytes(b []byte) {
	w.uint64(uint64(len(b)))
	_, _ = w.h.Write(b)
}

func (w canonicalWriter) digest(d Digest) {
	_, _ = w.h.Write(d[:])
}
