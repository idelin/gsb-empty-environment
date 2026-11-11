package canon

import "testing"

// freshUID permutes the concrete UIDs of fresh atoms while keeping every
// public constant, function name and the structural term shape intact.
func freshUID(t Term, rename map[AtomRef]string) Term {
	switch x := t.(type) {
	case Fresh:
		if uid, ok := rename[AtomRef{Session: x.Session, UID: x.UID}]; ok {
			return Fresh{Session: x.Session, UID: uid}
		}
		return x
	case Tuple:
		items := make([]Term, len(x.Items))
		for i, item := range x.Items {
			items[i] = freshUID(item, rename)
		}
		return Tuple{Items: items}
	case SymEnc:
		return SymEnc{Plaintext: freshUID(x.Plaintext, rename), Key: freshUID(x.Key, rename)}
	case PKEEnc:
		return PKEEnc{Plaintext: freshUID(x.Plaintext, rename), PubKey: freshUID(x.PubKey, rename)}
	case FuncApp:
		args := make([]Term, len(x.Args))
		for i, arg := range x.Args {
			args[i] = freshUID(arg, rename)
		}
		return FuncApp{Func: x.Func, Args: args}
	default:
		return t
	}
}

func renameState(s *State, rename map[AtomRef]string) *State {
	out := s.clone()
	for i := range out.Queue {
		out.Queue[i].Payload = freshUID(out.Queue[i].Payload, rename)
	}
	for i := range out.Roles {
		for k, v := range out.Roles[i].Vars {
			out.Roles[i].Vars[k] = freshUID(v, rename)
		}
	}
	for i, term := range out.AttackerKnowledge {
		out.AttackerKnowledge[i] = freshUID(term, rename)
	}
	return out
}

func sampleState() *State {
	na := Fresh{Session: "s1", UID: "nonce-A"}
	nb := Fresh{Session: "s1", UID: "nonce-B"}
	nc := Fresh{Session: "s2", UID: "nonce-C"}
	return &State{
		Queue: []Message{
			{
				From: "init:s1",
				To:   "resp:s1",
				Payload: Tuple{Items: []Term{
					Const{Name: "init"},
					na,
					FuncApp{Func: "hash", Args: []Term{na, Const{Name: "tag1"}}},
				}},
			},
			{
				From: "resp:s1",
				To:   "init:s1",
				Payload: SymEnc{
					Plaintext: Tuple{Items: []Term{na, nb}},
					Key:       FuncApp{Func: "kdf", Args: []Term{nb}},
				},
			},
		},
		Roles: []RoleState{
			{
				Session: "s1",
				Role:    "responder",
				Vars: map[string]Term{
					"peer":  Const{Name: "init"},
					"nonce": nb,
				},
			},
			{
				Session: "s1",
				Role:    "initiator",
				Vars: map[string]Term{
					"nonce": na,
					"peer":  Const{Name: "resp"},
				},
			},
			{
				Session: "s2",
				Role:    "initiator",
				Vars: map[string]Term{
					"nonce": nc,
				},
			},
		},
		AttackerKnowledge: []Term{
			Const{Name: "pub"},
			na,
			FuncApp{Func: "hash", Args: []Term{na}},
			nc,
		},
	}
}

// Random atom renaming must produce the same canonical state, and the
// certificate must itself reproduce that state and be a reversible bijection.
func TestCanonicalizeStableUnderAtomRenaming(t *testing.T) {
	original := sampleState()
	canon1, cert1 := Canonicalize(original)

	if !cert1.IsBijection() {
		t.Fatal("canonicalization certificate is not a bijection")
	}

	renamed := renameState(original, map[AtomRef]string{
		{Session: "s1", UID: "nonce-A"}: "alpha-9",
		{Session: "s1", UID: "nonce-B"}: "beta-7",
		{Session: "s2", UID: "nonce-C"}: "gamma-3",
	})
	canon2, cert2 := Canonicalize(renamed)

	if !StatesEqual(canon1, canon2) {
		t.Fatalf("canonical states differ after atom renaming:\n%#v\n%#v", canon1, canon2)
	}

	// The certificate independently reproduces every canonical term; set-valued
	// components (roles, knowledge) are then laid out by the canonical ordering.
	renamedByCert := cert1.RenameState(original)
	for i := range canon1.Queue {
		if !Equal(canon1.Queue[i].Payload, renamedByCert.Queue[i].Payload) {
			t.Fatal("certificate does not reproduce canonical queue terms")
		}
	}
	canonTerms := map[string]bool{}
	for _, term := range canon1.AttackerKnowledge {
		canonTerms[termKey(term)] = true
	}
	for _, term := range renamedByCert.AttackerKnowledge {
		if !canonTerms[termKey(term)] {
			t.Fatal("certificate does not reproduce canonical attacker knowledge")
		}
	}

	// Forward then reverse round-trips every term of the input. Queue order is
	// preserved; set-valued components are compared by content.
	roundTrip := cert1.ReverseRenameState(canon1)
	if len(roundTrip.Queue) != len(original.Queue) {
		t.Fatal("reverse renaming changed queue length")
	}
	for i := range original.Queue {
		if !Equal(roundTrip.Queue[i].Payload, original.Queue[i].Payload) {
			t.Fatal("reverse renaming did not reconstruct queue terms")
		}
	}
	originalTerms := map[string]bool{}
	for _, term := range original.AttackerKnowledge {
		originalTerms[termKey(term)] = true
	}
	for _, term := range roundTrip.AttackerKnowledge {
		if !originalTerms[termKey(term)] {
			t.Fatal("reverse renaming did not reconstruct attacker knowledge")
		}
	}
	originalRoles := map[string]map[string]Term{}
	for _, role := range original.Roles {
		key := role.Session + "/" + role.Role
		originalRoles[key] = role.Vars
	}
	for _, role := range roundTrip.Roles {
		want, ok := originalRoles[role.Session+"/"+role.Role]
		if !ok || len(want) != len(role.Vars) {
			t.Fatal("reverse renaming did not reconstruct role variables")
		}
		for k, v := range role.Vars {
			if !Equal(v, want[k]) {
				t.Fatal("reverse renaming reconstructed a wrong role variable")
			}
		}
	}

	// Both runs must agree on the canonical names of equal atoms.
	refA := AtomRef{Session: "s1", UID: "nonce-A"}
	nameA, ok := cert1.CanonicalName(refA)
	if !ok {
		t.Fatal("atom missing from certificate")
	}
	nameARenamed, ok := cert2.CanonicalName(AtomRef{Session: "s1", UID: "alpha-9"})
	if !ok || nameA != nameARenamed {
		t.Fatalf("equal atoms received different canonical names: %v vs %v", nameA, nameARenamed)
	}
}

// Canonical names are dense per session and follow first-occurrence order.
func TestCanonicalNamingAndConstants(t *testing.T) {
	canon, cert := Canonicalize(sampleState())

	cases := map[AtomRef]CanonicalAtom{
		{Session: "s1", UID: "nonce-A"}: {Session: "s1", Index: 0},
		{Session: "s1", UID: "nonce-B"}: {Session: "s1", Index: 1},
		{Session: "s2", UID: "nonce-C"}: {Session: "s2", Index: 0},
	}
	for ref, want := range cases {
		got, ok := cert.CanonicalName(ref)
		if !ok {
			t.Fatalf("missing mapping for %v", ref)
		}
		if got != want {
			t.Fatalf("mapping for %v = %v, want %v", ref, got, want)
		}
	}

	// Public constants and function names survive verbatim everywhere.
	queue0 := canon.Queue[0].Payload.(Tuple)
	if got := queue0.Items[0].(Const).Name; got != "init" {
		t.Fatalf("constant renamed in queue: %q", got)
	}
	if got := queue0.Items[2].(FuncApp).Func; got != "hash" {
		t.Fatalf("function name rewritten: %q", got)
	}
	foundPub := false
	for _, term := range canon.AttackerKnowledge {
		if c, ok := term.(Const); ok && c.Name == "pub" {
			foundPub = true
		}
	}
	if !foundPub {
		t.Fatal("public constant disappeared from attacker knowledge")
	}

	// Fresh atoms appear with canonical UIDs in all three state components.
	if got := canon.Queue[0].Payload.(Tuple).Items[1].(Fresh).UID; got != "c0" {
		t.Fatalf("queue atom UID = %q, want c0", got)
	}
	var s2Atom Fresh
	for _, role := range canon.Roles {
		if role.Session == "s2" {
			s2Atom = role.Vars["nonce"].(Fresh)
		}
	}
	if s2Atom.Session != "s2" || s2Atom.UID != "c0" {
		t.Fatalf("sorted role ordering/renaming wrong: %v", s2Atom)
	}
}

// Atoms of different sessions must never be identified, even when their
// per-session indices coincide, and mixing an atom into the wrong session must
// yield a different canonical state.
func TestCrossSessionsStayDistinguishable(t *testing.T) {
	sessionA := Fresh{Session: "s1", UID: "x"}
	sessionB := Fresh{Session: "s2", UID: "x"}

	// Same UID in two sessions: per-session indices both start at 0 but the
	// canonical names differ because the generating session differs.
	canon, cert := Canonicalize(&State{
		AttackerKnowledge: []Term{sessionA, sessionB},
	})
	nameA, _ := cert.CanonicalName(AtomRef{Session: "s1", UID: "x"})
	nameB, _ := cert.CanonicalName(AtomRef{Session: "s2", UID: "x"})
	if nameA.Index != 0 || nameB.Index != 0 {
		t.Fatalf("both sessions should start at index 0, got %v %v", nameA, nameB)
	}
	if nameA == nameB {
		t.Fatal("atoms from different sessions collapsed into one canonical name")
	}
	if len(canon.AttackerKnowledge) != 2 ||
		Equal(canon.AttackerKnowledge[0], canon.AttackerKnowledge[1]) {
		t.Fatalf("cross-session atoms became indistinguishable: %v", canon.AttackerKnowledge)
	}

	// Genuine cross-session mixing: the message expected to carry an s1 atom
	// instead carries an s2 atom. Renaming noise cannot make this state equal
	// to the well-formed one.
	wellFormed := &State{
		Queue: []Message{{
			From:    "resp:s1",
			To:      "init:s1",
			Payload: Tuple{Items: []Term{Fresh{Session: "s1", UID: "n1"}, Fresh{Session: "s1", UID: "n2"}}},
		}},
	}
	mixed := &State{
		Queue: []Message{{
			From:    "resp:s1",
			To:      "init:s1",
			Payload: Tuple{Items: []Term{Fresh{Session: "s1", UID: "n1"}, Fresh{Session: "s2", UID: "n2"}}},
		}},
	}
	canonWellFormed, _ := Canonicalize(wellFormed)
	canonMixed, _ := Canonicalize(mixed)
	if StatesEqual(canonWellFormed, canonMixed) {
		t.Fatal("cross-session mixing was canonicalized into the well-formed state")
	}
}

// The same atom repeated across queue, role variables and attacker knowledge
// receives one canonical name and the input state must remain untouched.
func TestSharedAtomAndInputImmutability(t *testing.T) {
	original := sampleState()
	snapshot := original.clone()

	canon, cert := Canonicalize(original)

	if !StatesEqual(original, snapshot) {
		t.Fatal("Canonicalize mutated its input state")
	}

	atomRef := AtomRef{Session: "s1", UID: "nonce-A"}
	want, ok := cert.CanonicalName(atomRef)
	if !ok {
		t.Fatal("nonce-A missing from certificate")
	}
	expectedUID := canonicalUID(want.Index)

	queueAtom := canon.Queue[0].Payload.(Tuple).Items[1].(Fresh)
	if queueAtom.UID != expectedUID {
		t.Fatalf("queue atom = %q, want %q", queueAtom.UID, expectedUID)
	}
	var roleAtom Fresh
	for _, role := range canon.Roles {
		if role.Session == "s1" && role.Role == "initiator" {
			roleAtom = role.Vars["nonce"].(Fresh)
		}
	}
	if roleAtom.UID != expectedUID || roleAtom.Session != "s1" {
		t.Fatalf("role atom = %v, want s1/%s", roleAtom, expectedUID)
	}

	// Layout independence: shuffle role and knowledge slices, canonical output
	// stays identical.
	shuffled := sampleState()
	shuffled.Roles[0], shuffled.Roles[1] = shuffled.Roles[1], shuffled.Roles[0]
	shuffled.AttackerKnowledge[0], shuffled.AttackerKnowledge[3] =
		shuffled.AttackerKnowledge[3], shuffled.AttackerKnowledge[0]
	canonShuffled, _ := Canonicalize(shuffled)
	if !StatesEqual(canon, canonShuffled) {
		t.Fatal("canonical state depends on slice layout")
	}
}

func TestCertificateBijectionGuards(t *testing.T) {
	good := &Certificate{
		Mappings: []Mapping{
			{
				Original:  AtomRef{Session: "s1", UID: "a"},
				Canonical: CanonicalAtom{Session: "s1", Index: 0},
			},
			{
				Original:  AtomRef{Session: "s1", UID: "b"},
				Canonical: CanonicalAtom{Session: "s1", Index: 1},
			},
		},
	}
	good.forward = map[AtomRef]CanonicalAtom{}
	for _, m := range good.Mappings {
		good.forward[m.Original] = m.Canonical
	}
	if !good.IsBijection() {
		t.Fatal("valid certificate rejected")
	}

	crossSession := &Certificate{Mappings: []Mapping{{
		Original:  AtomRef{Session: "s1", UID: "a"},
		Canonical: CanonicalAtom{Session: "s2", Index: 0},
	}}}
	if crossSession.IsBijection() {
		t.Fatal("session-crossing permutation accepted")
	}

	duplicateTarget := &Certificate{Mappings: []Mapping{
		{Original: AtomRef{Session: "s1", UID: "a"}, Canonical: CanonicalAtom{Session: "s1", Index: 0}},
		{Original: AtomRef{Session: "s1", UID: "b"}, Canonical: CanonicalAtom{Session: "s1", Index: 0}},
	}}
	if duplicateTarget.IsBijection() {
		t.Fatal("non-bijective certificate accepted")
	}

	gappedIndex := &Certificate{Mappings: []Mapping{{
		Original:  AtomRef{Session: "s1", UID: "a"},
		Canonical: CanonicalAtom{Session: "s1", Index: 2},
	}}}
	if gappedIndex.IsBijection() {
		t.Fatal("non-dense index set accepted")
	}
}

func TestNilAndEmptyStates(t *testing.T) {
	if canon, cert := Canonicalize(nil); canon != nil || cert != nil {
		t.Fatal("nil state must yield nil outputs")
	}
	canon, cert := Canonicalize(&State{})
	if canon == nil || cert == nil || len(cert.Mappings) != 0 || !cert.IsBijection() {
		t.Fatal("empty state must produce an empty valid certificate")
	}
}
