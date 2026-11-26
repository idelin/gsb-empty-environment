package archiveproof

import (
	"bytes"
	"fmt"
	"sort"
)

func assembleCertificate(
	cs *candidateSet,
	domains map[ServiceID]Offset,
	selected []int,
	genesisRefs []Reference,
	genesisGroups [][]int,
	acceptRefs []Reference,
	acceptGroups [][]int,
) (*Certificate, error) {
	sel := make(map[int]bool, len(selected))
	for _, v := range selected {
		sel[v] = true
	}
	cert := &Certificate{
		Version:    CertificateVersion,
		Acceptance: make([]Reference, 0, len(acceptRefs)),
		Genesis:    make([]Reference, 0, len(genesisRefs)),
		Sources:    make(map[ServiceID]Offset),
	}
	for _, v := range selected {
		it := cs.items[v]
		if it.node != nil {
			n, err := redactNode(it.node, cs, sel)
			if err != nil {
				return nil, err
			}
			cert.Entries = append(cert.Entries, Entry{Node: n})
			continue
		}
		s, err := redactShared(it.shared, cs, sel)
		if err != nil {
			return nil, err
		}
		cert.Entries = append(cert.Entries, Entry{Shared: s})
	}
	usedSources := make(map[ServiceID]bool)
	for _, e := range cert.Entries {
		if e.Node != nil {
			usedSources[e.Node.ClockSource()] = true
		} else {
			usedSources[e.Shared.Source] = true
		}
	}
	for src := range usedSources {
		if d, ok := domains[src]; ok {
			cert.Sources[src] = d
		}
	}
	var err error
	cert.Genesis, err = chooseEndpointRefs(genesisRefs, genesisGroups, sel, cs, false)
	if err != nil {
		return nil, err
	}
	cert.Acceptance, err = chooseEndpointRefs(acceptRefs, acceptGroups, sel, cs, true)
	if err != nil {
		return nil, err
	}
	cert.EvidenceCount = len(cert.Entries)
	return cert, nil
}

func chooseEndpointRefs(refs []Reference, groups [][]int, selected map[int]bool, cs *candidateSet, requireAll bool) ([]Reference, error) {
	out := make([]Reference, 0, len(refs))
	seen := make(map[string]struct{})
	for i := range refs {
		chosen := -1
		for _, v := range groups[i] {
			if selected[v] {
				chosen = v
				break
			}
		}
		if chosen < 0 {
			if !requireAll {
				continue
			}
			return nil, fmt.Errorf("%w: endpoint %v is not covered", ErrNoCertificate, refs[i])
		}
		ref := cs.items[chosen].ref()
		if _, ok := seen[ref.key()]; ok {
			continue
		}
		seen[ref.key()] = struct{}{}
		out = append(out, ref)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no endpoint evidence is covered", ErrNoCertificate)
	}
	return out, nil
}

func redactNode(n *Node, cs *candidateSet, selected map[int]bool) (*Node, error) {
	cp := *n
	cp.Approved = true
	cp.Commits = nil
	cp.Renews = nil
	for _, link := range n.Commits {
		if j, ok := cs.commitIdx[link.Commitment]; ok && selected[j] {
			cp.Commits = append(cp.Commits, link)
		}
	}
	for _, r := range n.Renews {
		cp.Renews = append(cp.Renews, redactRenewal(r, cs, selected)...)
	}
	return &cp, nil
}

func redactShared(s *SharedEvidence, cs *candidateSet, selected map[int]bool) (*SharedEvidence, error) {
	if s.LicensedBy == "" {
		return nil, fmt.Errorf("%w: unlicensed shared evidence selected", ErrDisallowedEvidence)
	}
	cp := *s
	cp.Covers = nil
	cp.RenewsNodes = nil
	cp.RenewsShared = nil
	for _, r := range s.RenewsNodes {
		cp.RenewsNodes = append(cp.RenewsNodes, redactRenewal(r, cs, selected)...)
	}
	for _, r := range s.RenewsShared {
		if j, ok := cs.commitIdx[r.To]; ok && selected[j] {
			cp.RenewsShared = append(cp.RenewsShared, r)
		}
	}
	return &cp, nil
}

// redactRenewal performs selective disclosure.  A visible target that is in
// the certificate keeps its exact identity and content digest.  A hidden
// target is replaced by a selected shared commitment that covers it; all
// hidden identities and contents are removed.
func redactRenewal(r Renewal, cs *candidateSet, selected map[int]bool) []Renewal {
	if r.OldID != "" {
		if j, ok := cs.nodeIdx[r.OldID]; ok && selected[j] {
			return []Renewal{{OldID: r.OldID, OldContent: r.OldContent, Gap: r.Gap}}
		}
		if !r.OldCommitment.IsZero() {
			if j, ok := cs.commitIdx[r.OldCommitment]; ok && selected[j] {
				return []Renewal{{OldCommitment: r.OldCommitment, Gap: r.Gap}}
			}
		}
		var out []Renewal
		hiddenCommitment, hasHiddenCommitment := cs.hiddenCommit[r.OldID]
		for i := range cs.items {
			it := cs.items[i]
			if it.shared == nil || !selected[i] {
				continue
			}
			if hasHiddenCommitment && it.shared.Commitment == hiddenCommitment {
				out = append(out, Renewal{OldCommitment: it.shared.Commitment, Gap: r.Gap})
				continue
			}
			for _, id := range it.shared.Covers {
				if id == r.OldID {
					out = append(out, Renewal{OldCommitment: it.shared.Commitment, Gap: r.Gap})
					break
				}
			}
		}
		return out
	}
	if !r.OldCommitment.IsZero() {
		if j, ok := cs.commitIdx[r.OldCommitment]; ok && selected[j] {
			return []Renewal{{OldCommitment: r.OldCommitment, Gap: r.Gap}}
		}
	}
	return nil
}

// Validate checks that c is a self-contained, possibly selectively disclosed
// certificate.  In particular it rejects any shared entry that still carries
// archive-side covered identities.
func (c *Certificate) Validate() error {
	if c == nil {
		return fmt.Errorf("%w: nil certificate", ErrInvalidCertificate)
	}
	if c.Version != CertificateVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidCertificate, c.Version)
	}
	if len(c.Entries) == 0 {
		return fmt.Errorf("%w: empty certificate", ErrInvalidCertificate)
	}
	nodes := map[NodeID]*Node{}
	shared := map[Commitment]*SharedEvidence{}
	for i := range c.Entries {
		e := &c.Entries[i]
		if (e.Node == nil) == (e.Shared == nil) {
			return fmt.Errorf("%w: entry %d must contain exactly one evidence unit", ErrInvalidCertificate, i)
		}
		if e.Node != nil {
			n := e.Node
			if !n.Approved {
				return fmt.Errorf("%w: certificate contains unapproved node %q", ErrDisallowedEvidence, n.ID)
			}
			if n.ID == "" || n.ClockSource() == "" || !n.Offset.Valid() {
				return fmt.Errorf("%w: malformed node entry", ErrInvalidCertificate)
			}
			if _, ok := nodes[n.ID]; ok {
				return fmt.Errorf("%w: duplicate node %q", ErrInvalidCertificate, n.ID)
			}
			nodes[n.ID] = n
			continue
		}
		s := e.Shared
		if s.Commitment.IsZero() || s.Source == "" || !s.Offset.Valid() {
			return fmt.Errorf("%w: malformed shared entry", ErrInvalidCertificate)
		}
		if len(s.Covers) != 0 {
			return fmt.Errorf("%w: shared entry %s leaks covered identities", ErrInvalidCertificate, s.Commitment)
		}
		if s.LicensedBy == "" {
			return fmt.Errorf("%w: shared entry %s is unlicensed", ErrDisallowedEvidence, s.Commitment)
		}
		if _, ok := shared[s.Commitment]; ok {
			return fmt.Errorf("%w: duplicate commitment %s", ErrInvalidCertificate, s.Commitment)
		}
		shared[s.Commitment] = s
	}
	for _, n := range nodes {
		for _, link := range n.Commits {
			if !link.Gap.Valid() {
				return fmt.Errorf("%w: node %q has invalid commitment gap", ErrInvalidCertificate, n.ID)
			}
			if _, ok := shared[link.Commitment]; !ok {
				return fmt.Errorf("%w: node %q commits evidence not in certificate", ErrInvalidCertificate, n.ID)
			}
		}
		for _, r := range n.Renews {
			if err := validateCertificateRenewal(r, nodes, shared); err != nil {
				return err
			}
		}
	}
	for _, s := range shared {
		for _, r := range s.RenewsNodes {
			if err := validateCertificateRenewal(r, nodes, shared); err != nil {
				return err
			}
		}
		for _, r := range s.RenewsShared {
			if !r.Gap.Valid() {
				return fmt.Errorf("%w: shared evidence has invalid renewal gap", ErrInvalidCertificate)
			}
			if _, ok := shared[r.To]; !ok {
				return fmt.Errorf("%w: shared renewal target is absent", ErrInvalidCertificate)
			}
		}
	}
	for _, ref := range append(append([]Reference{}, c.Genesis...), c.Acceptance...) {
		if err := ref.Validate(); err != nil {
			return err
		}
		if ref.Node != "" {
			if _, ok := nodes[ref.Node]; !ok {
				return fmt.Errorf("%w: endpoint node %q is absent", ErrInvalidCertificate, ref.Node)
			}
		} else if _, ok := shared[ref.Commitment]; !ok {
			return fmt.Errorf("%w: endpoint commitment %s is absent", ErrInvalidCertificate, ref.Commitment)
		}
	}
	if len(c.Genesis) == 0 || len(c.Acceptance) == 0 {
		return fmt.Errorf("%w: missing endpoint set", ErrInvalidCertificate)
	}
	if c.EvidenceCount != 0 && c.EvidenceCount != len(c.Entries) {
		return fmt.Errorf("%w: evidence count mismatch", ErrInvalidCertificate)
	}
	for src, dom := range c.Sources {
		if src == "" || !dom.Valid() {
			return fmt.Errorf("%w: malformed source domain", ErrInvalidCertificate)
		}
	}
	if len(c.Sources) != 0 {
		for i := range c.Entries {
			e := &c.Entries[i]
			src, off := ServiceID(""), Offset{}
			if e.Node != nil {
				src, off = e.Node.ClockSource(), e.Node.Offset
			} else {
				src, off = e.Shared.Source, e.Shared.Offset
			}
			dom, ok := c.Sources[src]
			if !ok || !off.ContainsInterval(dom) {
				return fmt.Errorf("%w: source %q domain is inconsistent", ErrInvalidCertificate, src)
			}
		}
	}
	return nil
}

// ValidateAgainst additionally checks the privacy boundary against the
// archive that produced the certificate.  It rejects hidden node identities,
// hidden content digests, unlicensed commitments, and archive-side covered
// identity metadata.
func (c *Certificate) ValidateAgainst(a *Archive) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := a.Validate(); err != nil {
		return err
	}
	idx, err := a.buildIndex()
	if err != nil {
		return err
	}
	approvedContent := map[Digest]bool{}
	for _, n := range idx.approved {
		approvedContent[n.Content] = true
	}
	for _, n := range idx.hidden {
		if approvedContent[n.Content] {
			continue
		}
		for i := range c.Entries {
			e := &c.Entries[i]
			if e.Node != nil && e.Node.Content == n.Content {
				return fmt.Errorf("%w: certificate contains hidden content %s", ErrDisallowedEvidence, n.Content)
			}
			if e.Node != nil {
				for _, r := range e.Node.Renews {
					if r.OldContent == n.Content {
						return fmt.Errorf("%w: certificate renewal contains hidden content %s", ErrDisallowedEvidence, n.Content)
					}
				}
			}
			if e.Shared != nil {
				for _, r := range e.Shared.RenewsNodes {
					if r.OldContent == n.Content {
						return fmt.Errorf("%w: shared renewal contains hidden content %s", ErrDisallowedEvidence, n.Content)
					}
				}
			}
		}
	}
	licensed := a.licenseMap(idx)
	for i := range c.Entries {
		e := &c.Entries[i]
		if e.Node != nil {
			n := e.Node
			archiveNode, ok := idx.approved[n.ID]
			if !ok {
				return fmt.Errorf("%w: certificate node %q is not approved", ErrDisallowedEvidence, n.ID)
			}
			if n.Service != archiveNode.Service || n.Key != archiveNode.Key ||
				n.Time != archiveNode.Time || n.Content != archiveNode.Content ||
				n.Offset != archiveNode.Offset || n.ClockSource() != archiveNode.ClockSource() {
				return fmt.Errorf("%w: certificate node %q differs from the signed record", ErrInvalidCertificate, n.ID)
			}
			for _, link := range n.Commits {
				if !containsCommitmentLink(archiveNode.Commits, link) {
					return fmt.Errorf("%w: certificate node %q contains an unsigned commitment link", ErrInvalidCertificate, n.ID)
				}
			}
			for _, r := range n.Renews {
				if r.OldID != "" {
					if old, ok := idx.hidden[r.OldID]; ok {
						return fmt.Errorf("%w: certificate leaks hidden identity %q", ErrDisallowedEvidence, old.ID)
					}
				}
				if !renewalPermitted(r, archiveNode.Renews, idx) {
					return fmt.Errorf("%w: certificate node %q contains an unsigned renewal", ErrInvalidCertificate, n.ID)
				}
			}
			for _, link := range n.Commits {
				if _, ok := licensed[link.Commitment]; !ok {
					return fmt.Errorf("%w: certificate contains unlicensed commitment %s", ErrDisallowedEvidence, link.Commitment)
				}
			}
			continue
		}
		s := e.Shared
		if len(s.Covers) != 0 {
			return fmt.Errorf("%w: certificate leaks covered identities", ErrDisallowedEvidence)
		}
		archiveShared, ok := idx.shared[s.Commitment]
		if !ok {
			return fmt.Errorf("%w: certificate contains unknown commitment %s", ErrInvalidCertificate, s.Commitment)
		}
		if s.Time != archiveShared.Time || s.Source != archiveShared.Source ||
			s.Offset != archiveShared.Offset || licensed[s.Commitment] != s.LicensedBy ||
			!bytes.Equal(s.LicenseProof, archiveShared.LicenseProof) {
			return fmt.Errorf("%w: certificate commitment %s differs from the signed evidence", ErrInvalidCertificate, s.Commitment)
		}
		if _, ok := licensed[s.Commitment]; !ok {
			return fmt.Errorf("%w: certificate contains unlicensed commitment %s", ErrDisallowedEvidence, s.Commitment)
		}
		for _, r := range s.RenewsNodes {
			if !renewalPermitted(r, archiveShared.RenewsNodes, idx) {
				return fmt.Errorf("%w: certificate commitment %s contains an unsigned renewal", ErrInvalidCertificate, s.Commitment)
			}
		}
		for _, r := range s.RenewsShared {
			found := false
			for _, ar := range archiveShared.RenewsShared {
				if ar.To == r.To && ar.Gap == r.Gap {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: certificate commitment %s contains an unsigned shared renewal", ErrInvalidCertificate, s.Commitment)
			}
		}
	}
	for _, refs := range [][]Reference{c.Genesis, c.Acceptance} {
		for _, ref := range refs {
			if ref.Node != "" {
				if _, ok := idx.hidden[ref.Node]; ok {
					return fmt.Errorf("%w: certificate endpoint leaks hidden identity %q", ErrDisallowedEvidence, ref.Node)
				}
			} else if _, ok := licensed[ref.Commitment]; !ok {
				return fmt.Errorf("%w: certificate endpoint uses unlicensed commitment %s", ErrDisallowedEvidence, ref.Commitment)
			}
		}
	}
	return nil
}

func containsCommitmentLink(links []CommitmentLink, want CommitmentLink) bool {
	for _, l := range links {
		if l.Commitment == want.Commitment && l.Gap == want.Gap {
			return true
		}
	}
	return false
}

func renewalPermitted(r Renewal, source []Renewal, idx *archiveIndex) bool {
	for _, ar := range source {
		if ar.Gap != r.Gap {
			continue
		}
		if r.OldID != "" {
			if ar.OldID == r.OldID && ar.OldContent == r.OldContent {
				return true
			}
			continue
		}
		if ar.OldCommitment == r.OldCommitment {
			return true
		}
		if ar.OldID != "" && !r.OldCommitment.IsZero() {
			if hidden, ok := idx.hidden[ar.OldID]; ok && hidden.NodeCommitment() == r.OldCommitment {
				return true
			}
			shared, ok := idx.shared[r.OldCommitment]
			if !ok {
				continue
			}
			for _, id := range shared.Covers {
				if id == ar.OldID {
					return true
				}
			}
		}
	}
	return false
}

func validateCertificateRenewal(r Renewal, nodes map[NodeID]*Node, shared map[Commitment]*SharedEvidence) error {
	if !r.Gap.Valid() {
		return fmt.Errorf("%w: invalid renewal gap", ErrInvalidCertificate)
	}
	hasID := r.OldID != ""
	hasCommitment := !r.OldCommitment.IsZero()
	if hasID == hasCommitment {
		return fmt.Errorf("%w: certificate renewal must bind exactly one target form", ErrInvalidCertificate)
	}
	if hasID {
		target, ok := nodes[r.OldID]
		if !ok {
			return fmt.Errorf("%w: renewal target %q is absent", ErrInvalidCertificate, r.OldID)
		}
		if r.OldContent != target.Content {
			return fmt.Errorf("%w: renewal target %q digest mismatch", ErrUnboundRenewal, r.OldID)
		}
		return nil
	}
	if _, ok := shared[r.OldCommitment]; !ok {
		return fmt.Errorf("%w: renewal commitment %s is absent", ErrInvalidCertificate, r.OldCommitment)
	}
	return nil
}

func certificateCandidates(c *Certificate) (*candidateSet, error) {
	cs := &candidateSet{
		nodeIdx:      map[NodeID]int{},
		commitIdx:    map[Commitment]int{},
		hiddenCommit: map[NodeID]Commitment{},
	}
	for i := range c.Entries {
		e := &c.Entries[i]
		if e.Node != nil {
			cs.add(candidate{node: e.Node})
		} else {
			cs.add(candidate{shared: e.Shared})
		}
	}
	return cs, nil
}

func certReferenceGroups(cs *candidateSet, refs []Reference) ([][]int, error) {
	out := make([][]int, 0, len(refs))
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		if ref.Node != "" {
			i, ok := cs.nodeIdx[ref.Node]
			if !ok {
				return nil, fmt.Errorf("%w: endpoint node %q is absent", ErrInvalidCertificate, ref.Node)
			}
			out = append(out, []int{i})
			continue
		}
		i, ok := cs.commitIdx[ref.Commitment]
		if !ok {
			return nil, fmt.Errorf("%w: endpoint commitment %s is absent", ErrInvalidCertificate, ref.Commitment)
		}
		out = append(out, []int{i})
	}
	return out, nil
}

func (c *Certificate) sourceDomains() map[ServiceID]Offset {
	out := make(map[ServiceID]Offset, len(c.Sources))
	for k, v := range c.Sources {
		out[k] = v
	}
	if len(out) != 0 {
		return out
	}
	for i := range c.Entries {
		e := &c.Entries[i]
		src, off := ServiceID(""), Offset{}
		if e.Node != nil {
			src, off = e.Node.ClockSource(), e.Node.Offset
		} else {
			src, off = e.Shared.Source, e.Shared.Offset
		}
		if cur, ok := out[src]; ok {
			if next, ok := cur.Intersect(off); ok {
				out[src] = next
			}
		} else {
			out[src] = off
		}
	}
	return out
}

type preparedCertificate struct {
	cs           *candidateSet
	edges        []edge
	acceptGroups [][]int
	goals        []int
	domains      map[ServiceID]Offset
}

func (c *Certificate) prepare() (*preparedCertificate, error) {
	cs, err := certificateCandidates(c)
	if err != nil {
		return nil, err
	}
	acceptGroups, err := certReferenceGroups(cs, c.Acceptance)
	if err != nil {
		return nil, err
	}
	genesis, err := certReferenceGroups(cs, c.Genesis)
	if err != nil {
		return nil, err
	}
	return &preparedCertificate{
		cs:           cs,
		edges:        buildEdges(nil, nil, cs),
		acceptGroups: acceptGroups,
		goals:        flattenGroups(genesis),
		domains:      c.sourceDomains(),
	}, nil
}

func (c *Certificate) verifyPrepared(p *preparedCertificate, clocking Clocking) (*Proof, error) {
	for src, dom := range p.domains {
		v, ok := clocking[src]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrMissingClocking, src)
		}
		if !dom.Contains(v) {
			return nil, fmt.Errorf("%w: source %q offset %d outside [%d,%d]", ErrClockOutOfRange, src, v, dom.Lo, dom.Hi)
		}
	}
	for i := range p.cs.items {
		it := p.cs.items[i]
		v, ok := clocking[it.source()]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrMissingClocking, it.source())
		}
		if !it.offset().Contains(v) {
			return nil, fmt.Errorf("%w: source %q offset %d outside evidence bound", ErrClockOutOfRange, it.source(), v)
		}
	}
	adj := make([][]int, p.cs.len())
	for _, e := range p.edges {
		f := p.cs.items[e.from]
		t := p.cs.items[e.to]
		ok, err := gapAllows(f.timestamp(), t.timestamp(), f.source(), t.source(), e.gap, clocking)
		if err != nil {
			return nil, err
		}
		if ok {
			adj[e.from] = append(adj[e.from], e.to)
		}
	}
	proof := &Proof{Clocking: clocking.Clone()}
	seen := make(map[string]struct{})
	for i, group := range p.acceptGroups {
		var path []int
		for _, a := range group {
			path = findPath(adj, allNodesMask, a, p.goals)
			if path != nil {
				break
			}
		}
		if path == nil {
			return nil, fmt.Errorf("%w: acceptance %v has no chain at clocking %v", ErrBrokenCertificate, c.Acceptance[i], clocking)
		}
		chain := Chain{Acceptance: c.Acceptance[i], Genesis: p.cs.items[path[len(path)-1]].ref()}
		for _, v := range path {
			chain.Path = append(chain.Path, p.cs.items[v].ref())
		}
		if _, ok := seen[chain.Acceptance.key()]; ok {
			continue
		}
		seen[chain.Acceptance.key()] = struct{}{}
		proof.Chains = append(proof.Chains, chain)
	}
	return proof, nil
}

// Verify evaluates the fixed certificate under one concrete clocking.  It
// returns one chain per acceptance record; the chain is chosen from the
// certificate only and never consults the archive.
func (c *Certificate) Verify(clocking Clocking) (*Proof, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	p, err := c.prepare()
	if err != nil {
		return nil, err
	}
	return c.verifyPrepared(p, clocking)
}

// VerifyAll checks the fixed certificate against every representative
// compatible clocking.
func (c *Certificate) VerifyAll(options ...*Options) error {
	var o Options
	if len(options) > 0 && options[0] != nil {
		o = *options[0]
	}
	o = o.normalized()
	if err := c.Validate(); err != nil {
		return err
	}
	p, err := c.prepare()
	if err != nil {
		return err
	}
	constraints := constraintsForEdges(p.cs, p.edges)
	clockings, err := enumerateClockings(p.domains, constraints, o.MaxExactClockings, o.MaxScenarios)
	if err != nil {
		return err
	}
	for _, clk := range clockings {
		if _, err := c.verifyPrepared(p, clk); err != nil {
			return err
		}
	}
	return nil
}

// SecureAt audits the host-side full archive (including hidden records) under
// one clocking.  It is intentionally separate from Certificate.Verify: the
// verifier-facing path cannot use these hidden nodes, but tests and archive
// operators can use this to show that a hidden chain existed.
func (a *Archive) SecureAt(clocking Clocking) (bool, error) {
	if err := a.Validate(); err != nil {
		return false, err
	}
	idx, err := a.buildIndex()
	if err != nil {
		return false, err
	}
	cs := &candidateSet{
		nodeIdx:      map[NodeID]int{},
		commitIdx:    map[Commitment]int{},
		hiddenCommit: map[NodeID]Commitment{},
	}
	for id, n := range idx.hidden {
		cs.hiddenCommit[id] = n.NodeCommitment()
	}
	ids := make([]NodeID, 0, len(a.Nodes))
	for i := range a.Nodes {
		ids = append(ids, a.Nodes[i].ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		cs.add(candidate{node: idx.allNodes[id]})
	}
	commitments := make([]Commitment, 0, len(a.Shared))
	for i := range a.Shared {
		commitments = append(commitments, a.Shared[i].Commitment)
	}
	sort.Slice(commitments, func(i, j int) bool { return commitments[i].String() < commitments[j].String() })
	for _, c := range commitments {
		cs.add(candidate{shared: idx.shared[c]})
	}
	edges := buildEdges(a, idx, cs)
	adj := make([][]int, cs.len())
	for _, e := range edges {
		f := cs.items[e.from]
		t := cs.items[e.to]
		ok, err := gapAllows(f.timestamp(), t.timestamp(), f.source(), t.source(), e.gap, clocking)
		if err != nil {
			return false, err
		}
		if ok {
			adj[e.from] = append(adj[e.from], e.to)
		}
	}
	acceptGroups, err := resolveReferenceGroups(cs, idx, a.Acceptance)
	if err != nil {
		return false, err
	}
	genesisGroups, err := resolveReferenceGroups(cs, idx, a.Genesis)
	if err != nil {
		return false, err
	}
	goals := flattenGroups(genesisGroups)
	for _, group := range acceptGroups {
		found := false
		for _, start := range group {
			path := findPath(adj, allNodesMask, start, goals)
			if path == nil {
				continue
			}
			valid := true
			for _, v := range path {
				it := cs.items[v]
				off, ok := clocking[it.source()]
				if !ok {
					return false, fmt.Errorf("%w: %q", ErrMissingClocking, it.source())
				}
				if !it.offset().Contains(off) {
					valid = false
					break
				}
			}
			if valid {
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}
