package taskpkg

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sort"
)

// Fork records every distinct, validly signed branch observed at one
// sequence number. VerifyPrefix never chooses one of these branches.
type Fork struct {
	Device  ed25519.PublicKey
	Boot    []byte
	Seq     uint64
	Records []Record
}

// Signatures returns one signature for each distinct branch in the fork.
func (f Fork) Signatures() [][]byte {
	sigs := make([][]byte, 0, len(f.Records))
	for _, r := range f.Records {
		sigs = append(sigs, append([]byte(nil), r.Sig...))
	}
	return sigs
}

// PrefixProof is a contiguous, ordered prefix from the chain genesis through
// EndSeq. Sealed is true only when a matching seal verified for EndSeq and
// Root.
type PrefixProof struct {
	Device    ed25519.PublicKey
	Boot      []byte
	StartSeq  uint64
	EndSeq    uint64
	Records   []Record
	Root      Digest
	Complete  bool
	Truncated bool
	Seal      *Seal
	Sealed    bool
}

// Proof is an alias for PrefixProof.
type Proof = PrefixProof

// Len returns the number of records in the proven prefix.
func (p PrefixProof) Len() int {
	return len(p.Records)
}

// ForkError is returned when a fork intersects the requested prefix. Forks
// contains every validly signed branch at the conflicting sequence.
type ForkError struct {
	Forks []Fork
	Proof *PrefixProof
}

func (e *ForkError) Error() string {
	if len(e.Forks) == 0 {
		return "taskpkg: fork detected"
	}
	f := e.Forks[0]
	return fmt.Sprintf("taskpkg: fork detected at sequence %d: %d valid signed branches",
		f.Seq, len(f.Records))
}

// Unwrap makes errors.Is(err, ErrFork) work.
func (e *ForkError) Unwrap() error { return ErrFork }

// IncompleteError describes where the contiguous prefix stopped.
type IncompleteError struct {
	MissingSeq uint64
	Reason     string
	Proof      *PrefixProof
}

func (e *IncompleteError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("taskpkg: chain is incomplete at sequence %d", e.MissingSeq)
	}
	return fmt.Sprintf("taskpkg: chain is incomplete at sequence %d: %s", e.MissingSeq, e.Reason)
}

// Unwrap makes errors.Is(err, ErrIncomplete) work.
func (e *IncompleteError) Unwrap() error { return ErrIncomplete }

// InvalidRecordError identifies a rejected record.
type InvalidRecordError struct {
	Index  int
	Record Record
	Err    error
}

func (e *InvalidRecordError) Error() string {
	return fmt.Sprintf("taskpkg: record %d rejected: %v", e.Index, e.Err)
}

// Unwrap makes errors.Is(err, ErrInvalidRecord) work.
func (e *InvalidRecordError) Unwrap() error { return ErrInvalidRecord }

// RejectedRecord is a record that failed individual signature validation.
type RejectedRecord struct {
	Index  int
	Record Record
	Err    error
}

// ChainReport describes one device/boot context in an Analyze report.
type ChainReport struct {
	Device         ed25519.PublicKey
	Boot           []byte
	StartSeq       uint64
	EndSeq         uint64
	Records        []Record
	Root           Digest
	NextSeq        uint64
	Complete       bool
	OriginFound    bool
	OriginConflict bool
	Forks          []Fork
	Seal           *Seal
	SealErr        error
	Sealed         bool
}

// Report is the non-strict result of Analyze. It separates every
// device/boot context, reports forks in full, and never emits a sealed
// result by selecting a fork branch.
type Report struct {
	Chains    []ChainReport
	Rejected  []RejectedRecord
	Forks     []Fork
	Complete  bool
	Sealed    bool
	SealError error
}

type contextKey struct {
	device string
	boot   string
}

func keyFor(r Record) contextKey {
	return contextKey{device: string(r.Device), boot: string(r.Boot)}
}

// MergeShards merges records from overlapping shards and removes exact
// duplicates. It deliberately keeps distinct branches at the same sequence.
func MergeShards(shards [][]Record) []Record {
	return mergeRecords(flatten(shards))
}

// Merge is the variadic form of MergeShards.
func Merge(shards ...[]Record) []Record {
	return MergeShards(shards)
}

// MergeRecords merges individual records and removes exact duplicates.
func MergeRecords(records ...Record) []Record {
	out := make([]Record, 0, len(records))
	for _, r := range records {
		out = append(out, r)
	}
	return mergeRecords(out)
}

func flatten(shards [][]Record) []Record {
	total := 0
	for _, shard := range shards {
		total += len(shard)
	}
	out := make([]Record, 0, total)
	for _, shard := range shards {
		out = append(out, shard...)
	}
	return out
}

func mergeRecords(records []Record) []Record {
	out := make([]Record, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, r := range records {
		id := string(r.Message()) + string(r.Sig)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, r)
	}
	return out
}

// VerifyPrefix verifies a complete prefix under a single device/boot
// context. When seal is non-nil, its Seq selects the prefix endpoint and the
// result is Sealed only if every record through that sequence is present,
// links correctly, and the seal binds that exact root.
//
// If seal is nil, every supplied record in that context must form the
// complete chain. Use ProvePrefix to verify a shorter explicit prefix.
func VerifyPrefix(records []Record, seal *Seal) (*PrefixProof, error) {
	var end *uint64
	if seal != nil {
		seq := seal.Seq
		end = &seq
	}
	return verifyPrefix(records, seal, end)
}

// VerifyChain is VerifyPrefix without a seal.
func VerifyChain(records []Record) (*PrefixProof, error) {
	return VerifyPrefix(records, nil)
}

// VerifyPrefixShards merges shards first, then verifies one prefix.
func VerifyPrefixShards(shards [][]Record, seal *Seal) (*PrefixProof, error) {
	return VerifyPrefix(MergeShards(shards), seal)
}

// VerifyChainShards is VerifyPrefixShards without a seal.
func VerifyChainShards(shards [][]Record) (*PrefixProof, error) {
	return VerifyPrefixShards(shards, nil)
}

// ProvePrefix verifies the exact prefix from genesis through end. Records
// after end are ignored, so a later gap or fork does not prevent proving an
// already complete prefix. If seal is supplied it must have Seq == end.
func ProvePrefix(records []Record, end uint64, seal *Seal) (*PrefixProof, error) {
	if seal != nil && seal.Seq != end {
		return nil, fmt.Errorf("%w: seal sequence %d, requested prefix end %d",
			ErrSealMismatch, seal.Seq, end)
	}
	return verifyPrefix(records, seal, &end)
}

func verifyPrefix(records []Record, seal *Seal, wantedEnd *uint64) (*PrefixProof, error) {
	records = mergeRecords(records)
	if len(records) == 0 {
		return nil, ErrEmpty
	}

	var ctx contextKey
	haveCtx := false
	for i, r := range records {
		if err := r.Verify(); err != nil {
			return nil, &InvalidRecordError{Index: i, Record: r, Err: err}
		}
		k := keyFor(r)
		if !haveCtx {
			ctx = k
			haveCtx = true
		} else if ctx != k {
			return nil, fmt.Errorf("%w at record %d", ErrMixedContext, i)
		}
	}

	if seal != nil {
		if err := seal.Verify(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidSeal, err)
		}
		if string(seal.Device) != ctx.device || string(seal.Boot) != ctx.boot {
			return nil, fmt.Errorf("%w: seal device/boot does not match records", ErrSealContext)
		}
	}

	bySeq := indexBySeq(records)
	origins := originSeqs(bySeq)
	if len(origins) == 0 {
		proof := baseProof(records, minSeq(bySeq))
		return proof, &IncompleteError{MissingSeq: minSeq(bySeq), Reason: "no chain genesis", Proof: proof}
	}
	if len(origins) > 1 {
		proof := baseProof(records, minSeq(bySeq))
		return proof, &IncompleteError{
			MissingSeq: origins[0],
			Reason:     fmt.Sprintf("multiple genesis sequences %v", origins),
			Proof:      proof,
		}
	}

	start := origins[0]
	expected := start
	prev := ZeroDigest
	prefix := make([]Record, 0, len(records))
	stopReason := ""
	var stopFork *Fork
	exhausted := false

	for {
		branches, ok := bySeq[expected]
		if !ok {
			stopReason = "missing sequence"
			break
		}
		branches = uniqueBranches(branches)
		if len(branches) > 1 {
			fork := makeFork(records[0].Device, records[0].Boot, expected, branches)
			stopFork = &fork
			stopReason = "fork"
			break
		}
		r := branches[0]
		if r.Prev != prev {
			stopReason = "previous digest does not match"
			break
		}
		prefix = append(prefix, r)
		prev = r.Link()
		if expected == ^uint64(0) {
			exhausted = true
			break
		}
		expected++
	}

	if wantedEnd == nil {
		if stopReason == "" {
			end := prefix[len(prefix)-1].Seq
			proof := makeProof(records, start, end, prefix, prev, seal, true, false)
			return proof, nil
		}
		proof := partialProof(records, start, prefix, prev, seal)
		if stopFork != nil {
			return proof, &ForkError{Forks: []Fork{*stopFork}, Proof: proof}
		}
		return proof, &IncompleteError{MissingSeq: expected, Reason: stopReason, Proof: proof}
	}

	target := *wantedEnd
	if target < start {
		proof := baseProof(records, start)
		return proof, &IncompleteError{
			MissingSeq: start, Reason: "prefix end precedes genesis", Proof: proof,
		}
	}
	if !exhausted && expected <= target {
		proof := partialProof(records, start, prefix, prev, seal)
		if stopFork != nil {
			return proof, &ForkError{Forks: []Fork{*stopFork}, Proof: proof}
		}
		if stopReason == "" {
			stopReason = "missing sequence"
		}
		return proof, &IncompleteError{MissingSeq: expected, Reason: stopReason, Proof: proof}
	}

	prefix = append([]Record(nil), prefix[:target-start+1]...)
	prev = prefix[len(prefix)-1].Link()
	proof := makeProof(records, start, target, prefix, prev, seal, true, false)

	if seal != nil {
		if seal.Seq != target {
			return proof, fmt.Errorf("%w: seal sequence %d, prefix end %d",
				ErrSealMismatch, seal.Seq, target)
		}
		if seal.Root != prev {
			return proof, fmt.Errorf("%w: seal root %s, computed root %s",
				ErrSealMismatch, seal.Root, prev)
		}
		proof.Sealed = true
	}
	return proof, nil
}

func partialProof(records []Record, start uint64, prefix []Record, root Digest, seal *Seal) *PrefixProof {
	if len(prefix) == 0 {
		p := baseProof(records, start)
		if seal != nil {
			s := *seal
			p.Seal = &s
		}
		return p
	}
	end := prefix[len(prefix)-1].Seq
	return makeProof(records, start, end, prefix, root, seal, false, true)
}

// Analyze verifies all individually valid records and groups them by
// device/boot. It never merges contexts. The seal is attached only to the
// matching context and permits Sealed only for a complete chain.
func Analyze(records []Record, seal *Seal) *Report {
	report := &Report{}
	records = mergeRecords(records)

	groups := make(map[contextKey][]Record)
	for i, r := range records {
		if err := r.Verify(); err != nil {
			report.Rejected = append(report.Rejected, RejectedRecord{Index: i, Record: r, Err: err})
			continue
		}
		k := keyFor(r)
		groups[k] = append(groups[k], r)
	}

	if seal != nil {
		if err := seal.Verify(); err != nil {
			report.SealError = fmt.Errorf("%w: %v", ErrInvalidSeal, err)
		}
	}

	keys := make([]contextKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].device != keys[j].device {
			return keys[i].device < keys[j].device
		}
		return keys[i].boot < keys[j].boot
	})

	sealAttached := false
	for _, k := range keys {
		cr := analyzeChain(k, groups[k], seal)
		report.Chains = append(report.Chains, cr)
		report.Forks = append(report.Forks, cr.Forks...)
		if cr.Seal != nil {
			sealAttached = true
		}
	}

	if seal != nil && !sealAttached {
		if report.SealError == nil {
			report.SealError = ErrSealContext
		}
	}

	report.Complete = len(report.Chains) == 1 && report.Chains[0].Complete
	report.Sealed = len(report.Chains) == 1 && report.Chains[0].Sealed
	return report
}

// Audit is an alias for Analyze.
func Audit(records []Record, seal *Seal) *Report {
	return Analyze(records, seal)
}

func analyzeChain(ctx contextKey, records []Record, seal *Seal) ChainReport {
	bySeq := indexBySeq(records)
	origins := originSeqs(bySeq)
	forks := collectForks(records[0].Device, records[0].Boot, bySeq)

	cr := ChainReport{
		Device:      append(ed25519.PublicKey(nil), records[0].Device...),
		Boot:        append([]byte(nil), records[0].Boot...),
		Forks:       forks,
		StartSeq:    minSeq(bySeq),
		NextSeq:     minSeq(bySeq),
		OriginFound: len(origins) > 0,
	}
	if len(origins) > 1 {
		cr.OriginConflict = true
	}
	if len(origins) == 0 {
		if seal != nil && sealContextMatches(ctx, seal) {
			cr.Seal = seal
			cr.SealErr = fmt.Errorf("%w: no genesis record", ErrIncomplete)
		}
		return cr
	}

	start := origins[0]
	cr.StartSeq = start
	expected := start
	prev := ZeroDigest
	exhausted := false
	for {
		branches, ok := bySeq[expected]
		if !ok {
			break
		}
		branches = uniqueBranches(branches)
		if len(branches) != 1 {
			break
		}
		r := branches[0]
		if r.Prev != prev {
			break
		}
		cr.Records = append(cr.Records, r)
		prev = r.Link()
		if expected == ^uint64(0) {
			exhausted = true
			break
		}
		expected++
	}
	cr.NextSeq = expected
	cr.Root = prev
	if len(cr.Records) > 0 {
		cr.EndSeq = cr.Records[len(cr.Records)-1].Seq
	}
	cr.Complete = len(origins) == 1 && len(forks) == 0
	if !exhausted {
		cr.Complete = cr.Complete && expected == maxSeq(bySeq)+1
	}

	if seal != nil && sealContextMatches(ctx, seal) {
		cr.Seal = seal
		if err := seal.Verify(); err != nil {
			cr.SealErr = fmt.Errorf("%w: %v", ErrInvalidSeal, err)
		} else if !cr.Complete {
			cr.SealErr = fmt.Errorf("%w: cannot seal an incomplete chain", ErrIncomplete)
		} else if seal.Seq != cr.EndSeq {
			cr.SealErr = fmt.Errorf("%w: seal sequence %d, chain endpoint %d",
				ErrSealMismatch, seal.Seq, cr.EndSeq)
		} else if seal.Root != cr.Root {
			cr.SealErr = fmt.Errorf("%w: seal root %s, chain root %s",
				ErrSealMismatch, seal.Root, cr.Root)
		} else {
			cr.Sealed = true
		}
	}
	return cr
}

func sealContextMatches(ctx contextKey, seal *Seal) bool {
	return string(seal.Device) == ctx.device && string(seal.Boot) == ctx.boot
}

func makeProof(records []Record, start, end uint64, prefix []Record, root Digest,
	seal *Seal, complete, truncated bool) *PrefixProof {
	p := &PrefixProof{
		Device:    append(ed25519.PublicKey(nil), records[0].Device...),
		Boot:      append([]byte(nil), records[0].Boot...),
		StartSeq:  start,
		EndSeq:    end,
		Records:   append([]Record(nil), prefix...),
		Root:      root,
		Complete:  complete,
		Truncated: truncated,
	}
	if seal != nil {
		s := *seal
		p.Seal = &s
	}
	return p
}

func baseProof(records []Record, start uint64) *PrefixProof {
	return &PrefixProof{
		Device:    append(ed25519.PublicKey(nil), records[0].Device...),
		Boot:      append([]byte(nil), records[0].Boot...),
		StartSeq:  start,
		EndSeq:    start,
		Root:      ZeroDigest,
		Complete:  false,
		Truncated: true,
	}
}

func indexBySeq(records []Record) map[uint64][]Record {
	bySeq := make(map[uint64][]Record, len(records))
	for _, r := range records {
		bySeq[r.Seq] = append(bySeq[r.Seq], r)
	}
	return bySeq
}

func uniqueBranches(records []Record) []Record {
	out := make([]Record, 0, len(records))
	seen := make(map[Digest]struct{}, len(records))
	for _, r := range records {
		link := r.Link()
		if _, ok := seen[link]; ok {
			continue
		}
		seen[link] = struct{}{}
		out = append(out, r)
	}
	return out
}

func originSeqs(bySeq map[uint64][]Record) []uint64 {
	seqs := make([]uint64, 0)
	for seq, branches := range bySeq {
		for _, r := range uniqueBranches(branches) {
			if r.Prev.IsZero() {
				seqs = append(seqs, seq)
				break
			}
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs
}

func collectForks(device ed25519.PublicKey, boot []byte, bySeq map[uint64][]Record) []Fork {
	forks := make([]Fork, 0)
	seqs := make([]uint64, 0, len(bySeq))
	for seq := range bySeq {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for _, seq := range seqs {
		branches := uniqueBranches(bySeq[seq])
		if len(branches) < 2 {
			continue
		}
		forks = append(forks, makeFork(device, boot, seq, branches))
	}
	return forks
}

func makeFork(device ed25519.PublicKey, boot []byte, seq uint64, branches []Record) Fork {
	return Fork{
		Device:  append(ed25519.PublicKey(nil), device...),
		Boot:    append([]byte(nil), boot...),
		Seq:     seq,
		Records: append([]Record(nil), branches...),
	}
}

func minSeq(bySeq map[uint64][]Record) uint64 {
	var min uint64
	first := true
	for seq := range bySeq {
		if first || seq < min {
			min = seq
			first = false
		}
	}
	return min
}

func maxSeq(bySeq map[uint64][]Record) uint64 {
	var max uint64
	first := true
	for seq := range bySeq {
		if first || seq > max {
			max = seq
			first = false
		}
	}
	return max
}

// RootDigest returns the verified root of a complete chain.
func RootDigest(records []Record) (Digest, error) {
	proof, err := VerifyPrefix(records, nil)
	if proof == nil {
		return ZeroDigest, err
	}
	return proof.Root, err
}

// ContextEqual reports whether two records claim the same device and boot.
func ContextEqual(a, b Record) bool {
	return bytes.Equal(a.Device, b.Device) && bytes.Equal(a.Boot, b.Boot)
}

// IsFork reports whether err contains one or more full fork reports.
func IsFork(err error) (*ForkError, bool) {
	var forkErr *ForkError
	if errors.As(err, &forkErr) {
		return forkErr, true
	}
	return nil, false
}
