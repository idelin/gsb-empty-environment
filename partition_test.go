package bootpolicy

import "testing"

func TestPrefixClassesMatchConstant(t *testing.T) {
	entries := []Entry{
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "", GenLo: 0, GenHi: maxUint32},
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "ab", GenLo: 0, GenHi: maxUint32},
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "abc", GenLo: 0, GenHi: maxUint32},
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "x", GenLo: 0, GenHi: maxUint32},
	}
	part, err := BuildPartition(entries, len(entries), 0)
	if err != nil {
		t.Fatal(err)
	}
	cert := part.Certificate()

	// Every label-prefix class must be match-constant across representative
	// probes at the class representative and one extension of it.
	for _, pc := range cert.PrefixClasses {
		if pc.IsNode && pc.Representative == "" {
			want := matchedPrefixSetByIndex(cert, pc.MatchedPrefixes)
			if _, ok := want[""]; !ok {
				t.Fatalf("empty class %q must match the empty prefix", pc.Prefix)
			}
			continue
		}
		if pc.Representative == "" {
			t.Fatalf("non-node class must have a concrete representative: %+v", pc)
		}
		probes := []string{pc.Representative, pc.Representative + "deeper-label"}
		for _, label := range probes {
			got := matchedPrefixSet(entries, label)
			want := matchedPrefixSetByIndex(cert, pc.MatchedPrefixes)
			if !setsEqual(got, want) {
				t.Fatalf("class %q probe %q: matched prefixes %v want %v", pc.Prefix, label, got, want)
			}
		}
	}

	// The full 32-bit generation axis must include the extreme tail class.
	var sawTail bool
	for _, gc := range cert.GenClasses {
		if gc.Tail {
			sawTail = true
			if gc.Hi != maxUint32 {
				t.Fatalf("tail class hi = %d", gc.Hi)
			}
		}
	}
	if !sawTail {
		t.Fatal("generation partition missing MaxUint32 tail class")
	}
}

func matchedPrefixSet(entries []Entry, label string) map[string]bool {
	out := map[string]bool{}
	for i := range entries {
		if hasPrefix(label, entries[i].LabelPrefix) {
			out[entries[i].LabelPrefix] = true
		}
	}
	return out
}

func matchedPrefixSetByIndex(cert Certificate, indexes []int) map[string]bool {
	out := map[string]bool{}
	for _, i := range indexes {
		out[cert.Prefixes[i]] = true
	}
	return out
}

func setsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func TestGenPartitionCoversAllUint32(t *testing.T) {
	entries := []Entry{
		{Kind: KindDeny, SignerID: "s", GenLo: 5, GenHi: 5},
		{Kind: KindDeny, SignerID: "s", GenLo: maxUint32 - 1, GenHi: maxUint32},
	}
	part, err := BuildPartition(entries, len(entries), 0)
	if err != nil {
		t.Fatal(err)
	}
	classes := part.Certificate().GenClasses

	// Classes must be contiguous, ordered and together cover [0, MaxUint32].
	if classes[0].Lo != 0 {
		t.Fatalf("first class starts at %d", classes[0].Lo)
	}
	for i := 1; i < len(classes); i++ {
		if uint64(classes[i].Lo) != uint64(classes[i-1].Hi)+1 {
			t.Fatalf("gap between classes %d and %d", i-1, i)
		}
	}
	if classes[len(classes)-1].Hi != maxUint32 {
		t.Fatal("last class does not reach MaxUint32")
	}

	// Endpoint generations 4,5,6 and the 32-bit extremes must each behave as
	// the membership definition requires, exercised on representative events.
	p := &Policy{Entries: entries}
	for _, gen := range []uint32{0, 4, 5, 6, maxUint32 - 1, maxUint32} {
		ev := LoadEvent{Child: component("s", "any", gen, "", "", digestOf(1))}
		denied := (gen == 5 || gen >= maxUint32-1)
		if got := p.EventAdmit(ev); got == denied {
			t.Fatalf("gen %d admit=%v want denied=%v", gen, got, denied)
		}
	}
}

func TestVerifyCertificateDetectsTampering(t *testing.T) {
	original := &Policy{Entries: []Entry{
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: 100},
	}}
	candidates := []Entry{
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: 100},
	}
	res, err := Compress(original, candidates)
	if err != nil {
		t.Fatal(err)
	}

	compressed := &Policy{Entries: res.Selected}
	if err := VerifyCertificate(original, compressed, res.Certificate); err != nil {
		t.Fatalf("valid certificate rejected: %v", err)
	}

	// Tampered group mask: claim a group is not denied.
	tampered := res.Certificate
	if len(tampered.GroupOrigDenied) == 0 {
		t.Fatal("expected at least one group")
	}
	tampered.GroupOrigDenied[0] = !tampered.GroupOrigDenied[0]
	if err := VerifyCertificate(original, compressed, tampered); err == nil {
		t.Fatal("tampered certificate must be rejected")
	}

	// A compressed-only exemption must be rejected even with a fresh rebuild.
	rogue := Entry{
		Kind: KindExempt, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: 100,
		Digest: digestOf(77),
	}
	roguePolicy := &Policy{Entries: append(append([]Entry(nil), res.Selected...), rogue)}
	part, err := BuildPartition(append(append([]Entry(nil), original.Entries...), roguePolicy.Entries...),
		len(original.Entries), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCertificate(original, roguePolicy, part.Certificate()); err != ErrNewExemption {
		t.Fatalf("new exemption error = %v", err)
	}
}
