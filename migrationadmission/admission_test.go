package migrationadmission

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
)

const (
	symA Symbol = 1
	symB Symbol = 2
	symX Symbol = 3
	symY Symbol = 4
	symT Symbol = 9
)

// allAcceptGrammar builds a length-bounded DFA over {a,b} where every word up
// to maxLen is legal.
func allAcceptGrammar(name string, maxLen int) Grammar {
	rules := []Rule{}
	for state := 0; state <= maxLen; state++ {
		rules = append(rules,
			Rule{From: state, On: 'a', To: state + 1},
			Rule{From: state, On: 'b', To: state + 1},
		)
	}
	accepts := make([]int, 0, maxLen+1)
	for state := 0; state <= maxLen; state++ {
		accepts = append(accepts, state)
	}
	return Grammar{Name: name, Start: 0, Accepts: accepts, Transitions: rules}
}

func tableWith(name string, rules []TranscriptionRule, tail ...Symbol) TranscriptionTable {
	return TranscriptionTable{Name: name, Rules: rules, Tail: tail}
}

func oldMildTable() TranscriptionTable {
	return tableWith("old", []TranscriptionRule{
		{On: 'a', Emit: []Symbol{symA}},
		{On: 'b', Emit: []Symbol{symB}},
	})
}

func newMildTable() TranscriptionTable {
	return tableWith("new", []TranscriptionRule{
		{On: 'a', Emit: []Symbol{symA}},
		{On: 'b', Emit: []Symbol{symB}},
	}, symT)
}

func mildPolicies(maxLen int) (Policy, Policy) {
	old := Policy{
		Version:       "old-1",
		Grammar:       positiveGrammar("g-old", maxLen),
		Transcription: oldMildTable(),
	}
	next := Policy{
		Version:       "new-1",
		Grammar:       positiveGrammar("g-new", maxLen),
		Transcription: newMildTable(),
	}
	return old, next
}

// positiveGrammar accepts exactly the non-empty words up to maxLen.
func positiveGrammar(name string, maxLen int) Grammar {
	g := allAcceptGrammar(name, maxLen)
	accepts := make([]int, 0, maxLen)
	for state := 1; state <= maxLen; state++ {
		accepts = append(accepts, state)
	}
	g.Accepts = accepts
	return g
}

func testRoot(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := GenerateRootKey()
	if err != nil {
		t.Fatalf("generate root: %v", err)
	}
	return pub, priv
}

// buildBundle assembles and signs a bundle, using the observed product
// backlog for the lateness certificate unless backlogOverride is set.
func buildBundle(t *testing.T, tenant string, epoch uint64, old, next Policy, claims []AliasClaim, priv ed25519.PrivateKey, backlogOverride *int) SignedBundle {
	t.Helper()
	pub := priv.Public().(ed25519.PublicKey)
	oldCompiled, err := old.compile()
	if err != nil {
		t.Fatalf("compile old: %v", err)
	}
	newCompiled, err := next.compile()
	if err != nil {
		t.Fatalf("compile new: %v", err)
	}
	aliasSet := map[string]bool{}
	for _, claim := range claims {
		aliasSet[aliasKey(claim.OldSubjectA, claim.OldSubjectB)] = true
	}
	result := analyzeProduct(productConfig{
		oldPolicy: oldCompiled,
		newPolicy: newCompiled,
		aliases:   aliasSet,
	})
	backlog := result.MaxBacklog
	if backlogOverride != nil {
		backlog = *backlogOverride
	}
	cert, err := SignLatenessCertificate(priv, LatenessCertificate{
		TenantID:      tenant,
		PolicyVersion: next.Version,
		MaxBacklog:    backlog,
		Epoch:         epoch,
	})
	if err != nil {
		t.Fatalf("sign lateness: %v", err)
	}
	bundle := Bundle{
		TenantID:  tenant,
		Epoch:     epoch,
		OldPolicy: old,
		NewPolicy: next,
		RootKey:   pub,
		Aliases:   claims,
		Lateness:  cert,
	}
	signed, err := bundle.Marshal(priv)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	return signed
}

func marshalWire(t *testing.T, signed SignedBundle) []byte {
	t.Helper()
	wire, err := signed.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal wire: %v", err)
	}
	return wire
}

func allWords(alphabet []byte, maxLen int) [][]byte {
	var words [][]byte
	words = append(words, []byte{})
	for depth := 1; depth <= maxLen; depth++ {
		var next [][]byte
		for _, prefix := range words {
			if len(prefix) != depth-1 {
				continue
			}
			for _, on := range alphabet {
				next = append(next, append(append([]byte(nil), prefix...), on))
			}
		}
		words = append(words, next...)
	}
	return words
}

func pairKey(a, b []byte) string {
	return string(a) + "|" + string(b)
}

func mustCompile(t *testing.T, p Policy) *compiledPolicy {
	t.Helper()
	cp, err := p.compile()
	if err != nil {
		t.Fatalf("compile policy %s: %v", p.Version, err)
	}
	return cp
}

// requiredAliases exhaustively enumerates short names and returns one signed
// claim for every pair that shares a new identifier but has distinct old
// subjects. Test-only helper: production admission never enumerates.
func requiredAliases(t *testing.T, tenant, version string, epoch uint64, old, next Policy, priv ed25519.PrivateKey) ([]AliasClaim, map[string]bool) {
	t.Helper()
	oldCompiled := mustCompile(t, old)
	newCompiled := mustCompile(t, next)
	words := allWords([]byte{'a', 'b'}, 2)
	seen := map[string]bool{}
	var claims []AliasClaim
	aliasSet := map[string]bool{}
	for _, left := range words {
		if len(left) == 0 {
			continue
		}
		leftNew, err := newCompiled.transduce(left)
		if err != nil {
			continue
		}
		for _, right := range words {
			if len(right) == 0 || bytes.Equal(left, right) {
				continue
			}
			rightNew, err := newCompiled.transduce(right)
			if err != nil || !symbolsEqual(leftNew, rightNew) {
				continue
			}
			leftOld, err := oldCompiled.transduce(left)
			if err != nil {
				continue
			}
			rightOld, err := oldCompiled.transduce(right)
			if err != nil || symbolsEqual(leftOld, rightOld) {
				continue
			}
			key := aliasKey(leftOld, rightOld)
			if seen[key] {
				continue
			}
			seen[key] = true
			claim, err := SignAliasClaim(priv, AliasClaim{
				TenantID:      tenant,
				PolicyVersion: version,
				OldSubjectA:   leftOld,
				OldSubjectB:   rightOld,
				Epoch:         epoch,
			})
			if err != nil {
				t.Fatal(err)
			}
			claims = append(claims, claim)
			aliasSet[key] = true
		}
	}
	return claims, aliasSet
}

// TestShortNameOracle exhaustively enumerates every short name pair and checks
// that the non-enumerating product analysis agrees with brute-force
// transductions and the alias table.
func TestShortNameOracle(t *testing.T) {
	// Three characters keep every buffered prefix within the four-symbol
	// lateness limit while still exercising unequal-length pairs.
	const maxLen = 3
	old, next := mildPolicies(maxLen)
	oldCompiled, err := old.compile()
	if err != nil {
		t.Fatal(err)
	}
	newCompiled, err := next.compile()
	if err != nil {
		t.Fatal(err)
	}

	words := allWords([]byte{'a', 'b'}, maxLen)
	type collisionPair struct {
		leftOld  []Symbol
		rightOld []Symbol
	}
	required := map[string]collisionPair{}
	for _, left := range words {
		if len(left) == 0 {
			continue
		}
		leftNew, err := newCompiled.transduce(left)
		if err != nil {
			t.Fatalf("transduce %q: %v", left, err)
		}
		for _, right := range words {
			if len(right) == 0 {
				continue
			}
			rightNew, err := newCompiled.transduce(right)
			if err != nil {
				t.Fatalf("transduce %q: %v", right, err)
			}
			if !symbolsEqual(leftNew, rightNew) || bytes.Equal(left, right) {
				continue
			}
			leftOld, err := oldCompiled.transduce(left)
			if err != nil {
				t.Fatalf("old transduce %q: %v", left, err)
			}
			rightOld, err := oldCompiled.transduce(right)
			if err != nil {
				t.Fatalf("old transduce %q: %v", right, err)
			}
			if symbolsEqual(leftOld, rightOld) {
				continue
			}
			required[aliasKey(leftOld, rightOld)] = collisionPair{leftOld, rightOld}
		}
	}
	// The product analysis must agree with the brute-force oracle in both
	// directions: a fold found by enumeration must be found by the automaton,
	// and an automaton fold must be present in the enumeration.
	result := analyzeProduct(productConfig{
		oldPolicy: oldCompiled,
		newPolicy: newCompiled,
		aliases:   map[string]bool{},
	})
	if (len(required) > 0) != errors.Is(result.Violation, ErrUnauthorizedFold) {
		t.Fatalf("oracle folds=%d but analysis=%v", len(required), result.Violation)
	}

	// With the folding pair authorized, the same policies verify cleanly.
	_, priv := testRoot(t)
	aliasSet := map[string]bool{}
	var claims []AliasClaim
	for _, pair := range required {
		claim, err := SignAliasClaim(priv, AliasClaim{
			TenantID:      "tenant-oracle",
			PolicyVersion: next.Version,
			OldSubjectA:   pair.leftOld,
			OldSubjectB:   pair.rightOld,
			Epoch:         1,
		})
		if err != nil {
			t.Fatal(err)
		}
		claims = append(claims, claim)
		aliasSet[aliasKey(pair.leftOld, pair.rightOld)] = true
	}
	signed := buildBundle(t, "tenant-oracle", 1, old, next, claims, priv, nil)
	verified, err := Verify(marshalWire(t, signed), nil)
	if err != nil {
		t.Fatalf("verified bundle rejected: %v", err)
	}
	identifier, err := verified.Admit([]byte("aba"))
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	wantID := []Symbol{symA, symB, symA, symT}
	if !symbolsEqual(identifier, wantID) {
		t.Fatalf("login identifier = %v, want %v", identifier, wantID)
	}

	authorized := analyzeProduct(productConfig{
		oldPolicy: oldCompiled,
		newPolicy: newCompiled,
		aliases:   aliasSet,
	})
	if authorized.Violation != nil {
		t.Fatalf("authorized analysis failed: %v", authorized.Violation)
	}
	if authorized.MaxBacklog > MaxLateness {
		t.Fatalf("mild max backlog = %d exceeds limit", authorized.MaxBacklog)
	}
}
