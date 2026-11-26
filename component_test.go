package archiveproof

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func dig(s string) Digest { return DigestString(s) }

func cmt(s string) Commitment { return Commitment(DigestString(s)) }

func mkNode(id, service string, tm int64, content string, off Interval) Node {
	return Node{
		ID:       NodeID(id),
		Service:  ServiceID(service),
		Time:     tm,
		Content:  dig(content),
		Offset:   off,
		Approved: true,
	}
}

func renew(id NodeID, content Digest, gap Gap) Renewal {
	return Renewal{OldID: id, OldContent: content, Gap: gap}
}

func mustBuild(t *testing.T, a *Archive) *Certificate {
	t.Helper()
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	c, err := BuildCertificate(a, nil)
	if err != nil {
		t.Fatalf("BuildCertificate: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("certificate Validate: %v", err)
	}
	return c
}

func mustVerify(t *testing.T, c *Certificate, clk Clocking) *Proof {
	t.Helper()
	p, err := c.Verify(clk)
	if err != nil {
		t.Fatalf("Verify(%v): %v", clk, err)
	}
	return p
}

func pathNodes(p *Proof, acceptance NodeID) []NodeID {
	for _, ch := range p.Chains {
		if ch.Acceptance.Node == acceptance {
			out := make([]NodeID, 0, len(ch.Path))
			for _, r := range ch.Path {
				out = append(out, r.Node)
			}
			return out
		}
	}
	return nil
}

func equalIDs(a, b []NodeID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTemporalAdjacency(t *testing.T) {
	g := mkNode("g", "svc", 0, "genesis", Exact(0))
	a := mkNode("a", "svc", 10, "record-a", Exact(0))
	a.Renews = []Renewal{renew("g", g.Content, Exact(10))}
	ar := &Archive{Nodes: []Node{g, a}, Genesis: []Reference{RefNode("g")}, Acceptance: []Reference{RefNode("a")}}

	c := mustBuild(t, ar)
	if c.EvidenceCount != 2 {
		t.Fatalf("evidence count = %d, want 2", c.EvidenceCount)
	}
	p := mustVerify(t, c, Clocking{"svc": 0})
	if got := pathNodes(p, "a"); !equalIDs(got, []NodeID{"a", "g"}) {
		t.Fatalf("path = %v, want [a g]", got)
	}
	if err := c.VerifyAll(nil); err != nil {
		t.Fatalf("VerifyAll: %v", err)
	}

	bad := *ar
	bad.Nodes = append([]Node(nil), ar.Nodes...)
	bad.Nodes[1].Renews[0].Gap = Exact(11)
	if _, err := BuildCertificate(&bad, nil); !errors.Is(err, ErrNoCertificate) {
		t.Fatalf("bad adjacency error = %v, want ErrNoCertificate", err)
	}
}

func TestNontrivialGapEmptyAtDifferentClocking(t *testing.T) {
	g := mkNode("g", "svc", 0, "genesis", Exact(0))
	a := mkNode("a", "svc", 10, "record-a", Exact(0))
	a.Renews = []Renewal{renew("g", g.Content, Between(10, 12))}
	ar := &Archive{Nodes: []Node{g, a}, Genesis: []Reference{RefNode("g")}, Acceptance: []Reference{RefNode("a")}}
	c := mustBuild(t, ar)
	if _, err := c.Verify(Clocking{"svc": 0}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, err := c.Verify(Clocking{"svc": 1}); !errors.Is(err, ErrClockOutOfRange) {
		t.Fatalf("offset outside signed bound error = %v, want ErrClockOutOfRange", err)
	}
}

func TestRenewalMustBindIdentityAndContentDigest(t *testing.T) {
	g := mkNode("g", "svc", 0, "genesis", Exact(0))
	a := mkNode("a", "svc", 10, "record-a", Exact(0))
	a.Renews = []Renewal{renew("g", dig("not-the-content"), Exact(10))}
	ar := &Archive{Nodes: []Node{g, a}, Genesis: []Reference{RefNode("g")}, Acceptance: []Reference{RefNode("a")}}
	if err := ar.Validate(); !errors.Is(err, ErrUnboundRenewal) {
		t.Fatalf("wrong digest error = %v, want ErrUnboundRenewal", err)
	}
	if _, err := BuildCertificate(ar, nil); !errors.Is(err, ErrUnboundRenewal) {
		t.Fatalf("BuildCertificate wrong digest error = %v", err)
	}

	a.Renews[0].OldContent = Digest{}
	if err := ar.Validate(); !errors.Is(err, ErrUnboundRenewal) {
		t.Fatalf("missing digest error = %v, want ErrUnboundRenewal", err)
	}
}

func TestSharedPredecessor(t *testing.T) {
	g := mkNode("g", "svc", 0, "genesis", Exact(0))
	a := mkNode("a", "svc", 5, "record-a", Exact(0))
	b := mkNode("b", "svc", 6, "record-b", Exact(0))
	a.Renews = []Renewal{renew("g", g.Content, Exact(5))}
	b.Renews = []Renewal{renew("g", g.Content, Exact(6))}
	ar := &Archive{
		Nodes:      []Node{g, a, b},
		Genesis:    []Reference{RefNode("g")},
		Acceptance: []Reference{RefNode("a"), RefNode("b")},
	}
	c := mustBuild(t, ar)
	if c.EvidenceCount != 3 {
		t.Fatalf("evidence count = %d, want 3 (shared genesis counted once)", c.EvidenceCount)
	}
	p := mustVerify(t, c, Clocking{"svc": 0})
	if got := pathNodes(p, "a"); !equalIDs(got, []NodeID{"a", "g"}) {
		t.Fatalf("path a = %v", got)
	}
	if got := pathNodes(p, "b"); !equalIDs(got, []NodeID{"b", "g"}) {
		t.Fatalf("path b = %v", got)
	}
}

func TestServiceKeyRotationSharesClockSource(t *testing.T) {
	g := mkNode("key-1-node", "permanent-service", 0, "genesis", Between(0, 3))
	g.Key = "key-1"
	a := mkNode("key-2-node", "permanent-service", 10, "record-a", Between(2, 5))
	a.Key = "key-2"
	a.Renews = []Renewal{renew("key-1-node", g.Content, Exact(10))}
	ar := &Archive{
		Nodes:      []Node{g, a},
		Genesis:    []Reference{RefNode("key-1-node")},
		Acceptance: []Reference{RefNode("key-2-node")},
	}
	c := mustBuild(t, ar)
	if got := c.Sources["permanent-service"]; got != Between(2, 3) {
		t.Fatalf("shared source domain = %v, want [2,3]", got)
	}
	if len(c.Sources) != 1 {
		t.Fatalf("key rotation created extra clock source: %v", c.Sources)
	}
	for _, off := range []int64{2, 3} {
		if _, err := c.Verify(Clocking{"permanent-service": off}); err != nil {
			t.Fatalf("Verify(%d): %v", off, err)
		}
	}

	// The same permanent identity cannot have two different shared offsets.
	bad := *ar
	bad.Nodes = append([]Node(nil), ar.Nodes...)
	bad.Nodes[1].Offset = Exact(5)
	if err := bad.Validate(); !errors.Is(err, ErrIncompatibleClocks) {
		t.Fatalf("disjoint key-rotation offsets error = %v, want ErrIncompatibleClocks", err)
	}
}

func TestDifferentClockingsChooseDifferentFixedChains(t *testing.T) {
	g := mkNode("g", "signer-a", 0, "genesis", Exact(0))
	g.Source = "clock-a"
	x := mkNode("x", "signer-a", 50, "branch-x", Exact(0))
	x.Source = "clock-a"
	x.Renews = []Renewal{renew("g", g.Content, Exact(50))}
	y := mkNode("y", "signer-a", 55, "branch-y", Exact(0))
	y.Source = "clock-a"
	y.Renews = []Renewal{renew("g", g.Content, Exact(55))}
	acc := mkNode("acc", "signer-b", 100, "acceptance", Between(0, 10))
	acc.Source = "clock-b"
	acc.Renews = []Renewal{
		renew("x", x.Content, Between(50, 54)),
		renew("y", y.Content, Between(50, 55)),
	}
	ar := &Archive{
		Nodes:      []Node{g, x, y, acc},
		Genesis:    []Reference{RefNode("g")},
		Acceptance: []Reference{RefNode("acc")},
	}
	c := mustBuild(t, ar)
	if c.EvidenceCount != 4 {
		t.Fatalf("evidence count = %d, want 4 (both conditional branches fixed in advance)", c.EvidenceCount)
	}
	for _, tc := range []struct {
		off  int64
		want NodeID
	}{
		{0, "x"}, {4, "x"}, {5, "y"}, {10, "y"},
	} {
		p := mustVerify(t, c, Clocking{"clock-a": 0, "clock-b": tc.off})
		got := pathNodes(p, "acc")
		if len(got) != 3 || got[1] != tc.want {
			t.Fatalf("clock-b=%d path = %v, want acc/%s/g", tc.off, got, tc.want)
		}
	}
	if err := c.VerifyAll(nil); err != nil {
		t.Fatalf("VerifyAll: %v", err)
	}
}

func TestLargeOffsetDomainUsesSignedBreakpoints(t *testing.T) {
	g := mkNode("g", "signer-a", 0, "genesis", Exact(0))
	g.Source = "clock-a"
	x := mkNode("x", "signer-a", 50, "branch-x", Exact(0))
	x.Source = "clock-a"
	x.Renews = []Renewal{renew("g", g.Content, Exact(50))}
	y := mkNode("y", "signer-a", 55, "branch-y", Exact(0))
	y.Source = "clock-a"
	y.Renews = []Renewal{renew("g", g.Content, Exact(55))}
	z := mkNode("z", "signer-a", 50, "branch-z", Exact(0))
	z.Source = "clock-a"
	z.Renews = []Renewal{renew("g", g.Content, Exact(50))}
	acc := mkNode("acc", "signer-b", 100, "acceptance", Between(0, 1_000_000))
	acc.Source = "clock-b"
	acc.Renews = []Renewal{
		renew("x", x.Content, Between(50, 54)),
		renew("y", y.Content, Between(50, 55)),
		renew("z", z.Content, Between(61, 1_000_050)),
	}
	ar := &Archive{
		Nodes:      []Node{g, x, y, z, acc},
		Genesis:    []Reference{RefNode("g")},
		Acceptance: []Reference{RefNode("acc")},
	}
	c := mustBuild(t, ar)
	if err := c.VerifyAll(nil); err != nil {
		t.Fatalf("VerifyAll over large domain: %v", err)
	}
	for _, tc := range []struct {
		off  int64
		want NodeID
	}{{0, "x"}, {4, "x"}, {5, "y"}, {10, "y"}, {11, "z"}, {999_999, "z"}} {
		p := mustVerify(t, c, Clocking{"clock-a": 0, "clock-b": tc.off})
		got := pathNodes(p, "acc")
		if len(got) != 3 || got[1] != tc.want {
			t.Fatalf("off=%d path=%v, want branch %s", tc.off, got, tc.want)
		}
	}
}

func TestCertificateMustCoverEveryCompatibleClocking(t *testing.T) {
	g := mkNode("g", "signer-a", 0, "genesis", Exact(0))
	g.Source = "clock-a"
	acc := mkNode("acc", "signer-b", 100, "acceptance", Between(0, 10))
	acc.Source = "clock-b"
	// The only chain works for offsets 0..4, leaving offsets 5..10 without
	// a continuity proof.
	acc.Renews = []Renewal{renew("g", g.Content, Between(100, 104))}
	ar := &Archive{
		Nodes:      []Node{g, acc},
		Genesis:    []Reference{RefNode("g")},
		Acceptance: []Reference{RefNode("acc")},
	}
	if _, err := BuildCertificate(ar, nil); !errors.Is(err, ErrNoCertificate) {
		t.Fatalf("BuildCertificate error = %v, want ErrNoCertificate", err)
	}
}

func hiddenBatchArchive(t *testing.T) (*Archive, Commitment, Commitment, Commitment) {
	t.Helper()
	g := mkNode("g", "signer-a", 0, "genesis", Exact(0))
	g.Source = "clock-a"
	acc := mkNode("acc", "signer-b", 100, "acceptance", Between(0, 10))
	acc.Source = "clock-b"

	c1 := cmt("hidden-branch-one")
	c2 := cmt("hidden-branch-two")
	batch := cmt("licensed-early-batch")
	acc.Commits = []CommitmentLink{
		{Commitment: batch, Gap: Between(50, 60)},
	}
	acc.Renews = []Renewal{
		// The signer knows both hidden branches, but only the batch is
		// licensed for disclosure.
		{OldCommitment: c1, Gap: Between(50, 54)},
		{OldCommitment: c2, Gap: Between(50, 55)},
	}
	ar := &Archive{
		Nodes:      []Node{g, acc},
		Genesis:    []Reference{RefNode("g")},
		Acceptance: []Reference{RefNode("acc")},
		Shared: []SharedEvidence{
			{
				Commitment: c1, Time: 50, Source: "clock-a", Offset: Exact(0),
				RenewsNodes: []Renewal{renew("g", g.Content, Exact(50))},
				Covers:      []NodeID{"hidden-1"},
			},
			{
				Commitment: c2, Time: 55, Source: "clock-a", Offset: Exact(0),
				RenewsNodes: []Renewal{renew("g", g.Content, Exact(55))},
				Covers:      []NodeID{"hidden-2"},
			},
			{
				Commitment: batch, Time: 50, Source: "clock-a", Offset: Exact(0),
				RenewsNodes:  []Renewal{renew("g", g.Content, Exact(50))},
				LicensedBy:   "acc",
				LicenseProof: []byte("signed-by-acc"),
				Covers:       []NodeID{"hidden-1", "hidden-2"},
			},
		},
	}
	return ar, c1, c2, batch
}

func TestHiddenUnionUsesOneLicensedSharedCommitment(t *testing.T) {
	ar, c1, c2, batch := hiddenBatchArchive(t)

	// Each clocking has a host-side chain through a different hidden record.
	for _, off := range []int64{0, 4} {
		ok, err := ar.SecureAt(Clocking{"clock-a": 0, "clock-b": off})
		if err != nil || !ok {
			t.Fatalf("hidden chain at %d: ok=%v err=%v", off, ok, err)
		}
	}
	for _, off := range []int64{5, 10} {
		ok, err := ar.SecureAt(Clocking{"clock-a": 0, "clock-b": off})
		if err != nil || !ok {
			t.Fatalf("hidden chain at %d: ok=%v err=%v", off, ok, err)
		}
	}

	c := mustBuild(t, ar)
	if c.EvidenceCount != 3 {
		t.Fatalf("evidence count = %d, want 3 (acc, genesis, one shared batch)", c.EvidenceCount)
	}
	foundBatch := false
	for _, e := range c.Entries {
		if e.Shared == nil {
			continue
		}
		if len(e.Shared.Covers) != 0 {
			t.Fatalf("certificate leaked covered identities: %v", e.Shared.Covers)
		}
		if e.Shared.Commitment == batch {
			foundBatch = true
		}
		if e.Shared.Commitment == c1 || e.Shared.Commitment == c2 {
			t.Fatalf("certificate disclosed an unlicensed hidden commitment")
		}
	}
	if !foundBatch {
		t.Fatalf("certificate does not contain the licensed batch")
	}
	for off := int64(0); off <= 10; off++ {
		clk := Clocking{"clock-a": 0, "clock-b": off}
		p, err := c.Verify(clk)
		if err != nil {
			t.Fatalf("Verify(%d): %v", off, err)
		}
		if len(p.Chains) != 1 || len(p.Chains[0].Path) != 3 {
			t.Fatalf("Verify(%d) path = %+v", off, p.Chains)
		}
		if p.Chains[0].Path[1].Commitment != batch {
			t.Fatalf("Verify(%d) did not use the batch", off)
		}
	}

	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, secret := range []string{"hidden-1", "hidden-2", c1.String(), c2.String()} {
		if strings.Contains(text, secret) {
			t.Fatalf("certificate JSON leaked %q:\n%s", secret, text)
		}
	}
	if strings.Contains(text, `"covers"`) {
		t.Fatalf("certificate JSON contains covers metadata")
	}

	var round Certificate
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if err := round.Validate(); err != nil {
		t.Fatalf("round-trip Validate: %v", err)
	}
	if err := round.ValidateAgainst(ar); err != nil {
		t.Fatalf("round-trip ValidateAgainst: %v", err)
	}
	if err := round.VerifyAll(nil); err != nil {
		t.Fatalf("round-trip VerifyAll: %v", err)
	}
}

func TestOneTimestampMayCommitMultipleEvidenceNodes(t *testing.T) {
	g := mkNode("g", "signer-a", 0, "genesis", Exact(0))
	g.Source = "clock-a"
	acc := mkNode("acc", "signer-b", 100, "acceptance", Between(0, 10))
	acc.Source = "clock-b"
	c1 := cmt("committed-branch-one")
	c2 := cmt("committed-branch-two")
	acc.Commits = []CommitmentLink{
		{Commitment: c1, Gap: Between(50, 54)},
		{Commitment: c2, Gap: Between(50, 55)},
	}
	ar := &Archive{
		Nodes:      []Node{g, acc},
		Genesis:    []Reference{RefNode("g")},
		Acceptance: []Reference{RefNode("acc")},
		Shared: []SharedEvidence{
			{
				Commitment: c1, Time: 50, Source: "clock-a", Offset: Exact(0),
				RenewsNodes: []Renewal{renew("g", g.Content, Exact(50))},
				LicensedBy:  "acc", LicenseProof: []byte("one"),
			},
			{
				Commitment: c2, Time: 55, Source: "clock-a", Offset: Exact(0),
				RenewsNodes: []Renewal{renew("g", g.Content, Exact(55))},
				LicensedBy:  "acc", LicenseProof: []byte("two"),
			},
		},
	}
	c := mustBuild(t, ar)
	if c.EvidenceCount != 4 {
		t.Fatalf("evidence count = %d, want 4", c.EvidenceCount)
	}
	for _, tc := range []struct {
		off  int64
		want Commitment
	}{
		{0, c1}, {4, c1}, {5, c2}, {10, c2},
	} {
		p := mustVerify(t, c, Clocking{"clock-a": 0, "clock-b": tc.off})
		if p.Chains[0].Path[1].Commitment != tc.want {
			t.Fatalf("off=%d path=%+v, want commitment %s", tc.off, p.Chains[0].Path, tc.want)
		}
	}
}

func TestHiddenNodeCanBeLicensedByItsOwnRecordCommitment(t *testing.T) {
	hidden := Node{
		ID:       "hidden-genesis",
		Service:  "svc",
		Time:     0,
		Content:  dig("hidden-content"),
		Offset:   Exact(0),
		Approved: false,
	}
	c := hidden.NodeCommitment()
	acc := mkNode("acc", "svc", 10, "acceptance", Exact(0))
	acc.Commits = []CommitmentLink{{Commitment: c, Gap: Exact(10)}}
	ar := &Archive{
		Nodes:      []Node{hidden, acc},
		Genesis:    []Reference{RefNode("hidden-genesis")},
		Acceptance: []Reference{RefNode("acc")},
		Shared: []SharedEvidence{{
			Commitment: c, Time: 0, Source: "svc", Offset: Exact(0),
			// Permission is inferred from acc's signed commitment link.
		}},
	}
	cert := mustBuild(t, ar)
	if cert.EvidenceCount != 2 {
		t.Fatalf("evidence count = %d, want 2", cert.EvidenceCount)
	}
	if cert.Genesis[0].Commitment != c || cert.Genesis[0].Node != "" {
		t.Fatalf("genesis = %+v, want opaque commitment", cert.Genesis[0])
	}
	if _, err := cert.Verify(Clocking{"svc": 0}); err != nil {
		t.Fatal(err)
	}
}

func TestWithoutLicensedBatchNoCertificate(t *testing.T) {
	ar, _, _, _ := hiddenBatchArchive(t)
	// Remove the signer's permission for the batch and every other reference
	// to it.  The hidden host-side chains still exist, but the verifier must
	// not be handed them at runtime.
	found := 0
	for i := range ar.Nodes {
		if ar.Nodes[i].ID != "acc" {
			continue
		}
		links := ar.Nodes[i].Commits[:0]
		for _, l := range ar.Nodes[i].Commits {
			if l.Commitment.String() != cmt("licensed-early-batch").String() {
				links = append(links, l)
			}
		}
		ar.Nodes[i].Commits = links
		found++
	}
	if found == 0 {
		t.Fatal("test setup failed")
	}
	for i := range ar.Shared {
		if ar.Shared[i].Commitment == cmt("licensed-early-batch") {
			ar.Shared[i].LicensedBy = ""
			ar.Shared[i].LicenseProof = nil
		}
	}
	if err := ar.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if _, err := BuildCertificate(ar, nil); !errors.Is(err, ErrNoCertificate) {
		t.Fatalf("BuildCertificate error = %v, want ErrNoCertificate", err)
	}
	// The full host archive is still secure; only disclosure is impossible.
	for _, off := range []int64{0, 10} {
		ok, err := ar.SecureAt(Clocking{"clock-a": 0, "clock-b": off})
		if err != nil || !ok {
			t.Fatalf("host-side chain at %d: ok=%v err=%v", off, ok, err)
		}
	}
}

func TestThousandsShareOneEarlyCommitment(t *testing.T) {
	const n = 5000
	hidden := make([]Node, 0, n)
	covers := make([]NodeID, 0, n)
	for i := 0; i < n; i++ {
		id := NodeID("early-" + itoa(i))
		hidden = append(hidden, Node{
			ID:       id,
			Service:  "early-service",
			Source:   "early-clock",
			Time:     50,
			Content:  dig("early-content-" + itoa(i)),
			Offset:   Exact(0),
			Approved: false,
		})
		covers = append(covers, id)
	}
	batch := cmt("5000-early-records")
	acc := mkNode("acc", "current-service", 100, "current", Exact(0))
	acc.Source = "early-clock"
	acc.Commits = []CommitmentLink{{Commitment: batch, Gap: Exact(50)}}
	ar := &Archive{
		Nodes:      append(hidden, acc),
		Genesis:    []Reference{RefNode("early-0")},
		Acceptance: []Reference{RefNode("acc")},
		Shared: []SharedEvidence{{
			Commitment: batch, Time: 50, Source: "early-clock", Offset: Exact(0),
			Covers: covers, LicensedBy: "acc", LicenseProof: []byte("batch-signature"),
		}},
	}
	c := mustBuild(t, ar)
	if c.EvidenceCount != 2 {
		t.Fatalf("evidence count = %d, want 2", c.EvidenceCount)
	}
	if c.Genesis[0].Commitment != batch {
		t.Fatalf("genesis = %+v, want batch commitment", c.Genesis[0])
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, secret := range []string{"early-0", "early-4999", dig("early-content-4999").String()} {
		if strings.Contains(text, secret) {
			t.Fatalf("certificate leaked hidden record data %q", secret)
		}
	}
	p := mustVerify(t, c, Clocking{"early-clock": 0})
	if len(p.Chains[0].Path) != 2 {
		t.Fatalf("path = %+v", p.Chains[0].Path)
	}
}

func TestIrrelevantApprovedRecordsArePrunedBeforeExactSearch(t *testing.T) {
	g := mkNode("g", "svc", 0, "genesis", Exact(0))
	a := mkNode("a", "svc", 10, "record", Exact(0))
	a.Renews = []Renewal{renew("g", g.Content, Exact(10))}
	nodes := []Node{g}
	for i := 0; i < 5000; i++ {
		nodes = append(nodes, mkNode("unrelated-"+itoa(i), "svc", int64(i+1), "unrelated-"+itoa(i), Exact(0)))
	}
	nodes = append(nodes, a)
	ar := &Archive{Nodes: nodes, Genesis: []Reference{RefNode("g")}, Acceptance: []Reference{RefNode("a")}}
	c := mustBuild(t, ar)
	if c.EvidenceCount != 2 {
		t.Fatalf("evidence count = %d, want 2 after pruning", c.EvidenceCount)
	}
	if err := c.ValidateAgainst(ar); err != nil {
		t.Fatalf("ValidateAgainst: %v", err)
	}
}

func TestGenesisMayBeChosenFromAnAlternativeSet(t *testing.T) {
	g1 := mkNode("g1", "svc", 0, "genesis-1", Exact(0))
	g2 := mkNode("g2", "svc", 1, "genesis-2", Exact(0))
	a := mkNode("a", "svc", 10, "record", Exact(0))
	a.Renews = []Renewal{renew("g1", g1.Content, Exact(10))}
	ar := &Archive{
		Nodes:      []Node{g1, g2, a},
		Genesis:    []Reference{RefNode("g1"), RefNode("g2")},
		Acceptance: []Reference{RefNode("a")},
	}
	c := mustBuild(t, ar)
	if c.EvidenceCount != 2 {
		t.Fatalf("evidence count = %d, want 2", c.EvidenceCount)
	}
	if len(c.Genesis) != 1 || c.Genesis[0].Node != "g1" {
		t.Fatalf("genesis = %+v, want g1", c.Genesis)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	a := combinedArchive()
	c1, err := BuildCertificate(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := BuildCertificate(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	b1, err := json.Marshal(c1)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := json.Marshal(c2)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("certificate is not deterministic:\n%s\n%s", b1, b2)
	}
}

func TestExplicitSharedClockSourceAcrossServices(t *testing.T) {
	g := mkNode("g", "service-1", 0, "genesis", Between(0, 5))
	g.Source = "common-clock"
	a := mkNode("a", "service-2", 10, "record", Between(2, 7))
	a.Source = "common-clock"
	a.Renews = []Renewal{renew("g", g.Content, Exact(10))}
	ar := &Archive{Nodes: []Node{g, a}, Genesis: []Reference{RefNode("g")}, Acceptance: []Reference{RefNode("a")}}
	c := mustBuild(t, ar)
	if got := c.Sources["common-clock"]; got != Between(2, 5) {
		t.Fatalf("domain = %v, want [2,5]", got)
	}
	if _, err := c.Verify(Clocking{"common-clock": 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Verify(Clocking{"common-clock": 1}); !errors.Is(err, ErrClockOutOfRange) {
		t.Fatalf("error = %v", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
