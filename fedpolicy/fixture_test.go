package fedpolicy

// Small-alphabet complete-model fixture.
//
// Alphabet: {a, b}. Two old directories ("corp.old.example",
// "retail.old.example") feed a target hub; a second target tenant shares the
// retail tenant name but lives on a different issuer to prove authority
// scoping.
const (
	issuerCorp    = "corp.old.example"
	issuerRetail  = "retail.old.example"
	issuerHub     = "hub.new.example"
	issuerPartner = "partner.new.example"

	tenantShared = "team-x"
	tenantHub    = "hub-team"
)

var smallAlphabet = []string{"a", "b"}

// buildFixture assembles the channels, old policy, labeler, public labels and
// candidates used across the model checks.
func buildFixture() (*Mapper, OldPolicy, PrefixLabeler, []Label, []Candidate) {
	// Corp channel: identity transducer that keeps every segment.
	corpFST := NewFST(0)
	corpFST.AddArc(Arc{From: 0, In: "a", Out: "a", To: 0})
	corpFST.AddArc(Arc{From: 0, In: "b", Out: "b", To: 0})
	corpFST.MarkFinal(0)

	// Retail channel: drops an initial a, keeps the rest. Many retail names
	// therefore collapse onto the same hub name as a corp name (many-to-one).
	retailFST := NewFST(0)
	retailFST.AddArc(Arc{From: 0, In: "a", Out: "", To: 1})
	retailFST.AddArc(Arc{From: 0, In: "b", Out: "b", To: 1})
	retailFST.AddArc(Arc{From: 1, In: "a", Out: "a", To: 1})
	retailFST.AddArc(Arc{From: 1, In: "b", Out: "b", To: 1})
	retailFST.MarkFinal(1)

	// Partner channel: only emits b*; an a in the input has no transition,
	// which creates target prefixes with no preimage.
	partnerFST := NewFST(0)
	partnerFST.AddArc(Arc{From: 0, In: "a", Out: "b", To: 0})
	partnerFST.MarkFinal(0)

	corpHeader := HeaderKey{Issuer: issuerCorp, Tenant: tenantShared}
	retailHeader := HeaderKey{Issuer: issuerRetail, Tenant: tenantShared}
	hubHeader := HeaderKey{Issuer: issuerHub, Tenant: tenantHub}
	partnerHeader := HeaderKey{Issuer: issuerPartner, Tenant: tenantShared}

	mapper := &Mapper{Channels: []*Channel{
		{Old: corpHeader, New: hubHeader, Alphabet: smallAlphabet, Transducer: corpFST},
		{Old: retailHeader, New: hubHeader, Alphabet: smallAlphabet, Transducer: retailFST},
		{Old: retailHeader, New: partnerHeader, Alphabet: smallAlphabet, Transducer: partnerFST},
	}}
	mapper.Build()

	// Last-match-wins old policy with explicit exceptions:
	// allow everything under corp, deny the /a subtree, then re-allow the
	// narrow /a/b exception. Retail defaults to allow except /b deny.
	policy := OldPolicy{
		Default: Deny,
		Rules: []OrderedRule{
			{Prefix: NamePrefix{issuerCorp, tenantShared, nil}, Effect: Allow},
			{Prefix: NamePrefix{issuerCorp, tenantShared, []string{"a"}}, Effect: Deny},
			{Prefix: NamePrefix{issuerCorp, tenantShared, []string{"a", "b"}}, Effect: Allow},
			{Prefix: NamePrefix{issuerRetail, tenantShared, nil}, Effect: Allow},
			{Prefix: NamePrefix{issuerRetail, tenantShared, []string{"b"}}, Effect: Deny},
		},
	}

	labeler := PrefixLabeler{Rules: []LabelRule{
		{Prefix: NamePrefix{issuerCorp, tenantShared, []string{"a"}}, Label: "public"},
		{Prefix: NamePrefix{issuerCorp, tenantShared, nil}, Label: "corp-wide"},
		{Prefix: NamePrefix{issuerRetail, tenantShared, nil}, Label: "retail-wide"},
		{Prefix: NamePrefix{issuerRetail, tenantShared, []string{"a"}}, Label: "internal-only"},
	}}

	publicLabels := []Label{"public", "corp-wide", "retail-wide"}

	candidates := []Candidate{
		// C1 empty prefix over the merged hub authority: unsafe because the
		// corp /a deny exception and retail /b deny both feed it.
		{Prefix: NamePrefix{issuerHub, tenantHub, nil}},

		// C2 the narrow corp /a/b exception, which is fully allowed.
		{Prefix: NamePrefix{issuerHub, tenantHub, []string{"a", "b"}}},

		// C3 the /b prefix: corp allows /b, but retail's dropped-a names such
		// as [a b] map here while retail denies /b -> unsafe many-to-one.
		{Prefix: NamePrefix{issuerHub, tenantHub, []string{"b"}}},

		// C4 require "public" on /a/b. Corp preimages carry it, but the
		// retail many-to-one preimages (drop-a names like [a a b]) do not,
		// so it is a missing-label rejection.
		{Prefix: NamePrefix{issuerHub, tenantHub, []string{"a", "b"}}, Require: []Label{"public"}},

		// C5 requires a non-public label: not expressible on the target.
		{Prefix: NamePrefix{issuerHub, tenantHub, []string{"a", "b"}}, Require: []Label{"internal-only"}},

		// C6 partner /b: every preimage is a retail a* name, all allowed and
		// carrying retail-wide, so the plain positive allow is safe.
		{Prefix: NamePrefix{issuerPartner, tenantShared, []string{"b"}}},

		// C7 same path requiring retail-wide: also safe and equivalent to C6.
		{Prefix: NamePrefix{issuerPartner, tenantShared, []string{"b"}}, Require: []Label{"retail-wide"}},

		// C8 label that qualifying preimages lack: corp /a/b principals do
		// not carry retail-wide -> missing-label witness.
		{Prefix: NamePrefix{issuerHub, tenantHub, []string{"a", "b"}}, Require: []Label{"retail-wide"}},

		// C9 gap inside the image language: partner cannot produce /a.
		{Prefix: NamePrefix{issuerPartner, tenantShared, []string{"a"}}},

		// C10 authority with no channel at all: every identity lacks preimage.
		{Prefix: NamePrefix{"ghost.new.example", tenantHub, []string{"a"}}},
	}

	return mapper, policy, labeler, publicLabels, candidates
}
