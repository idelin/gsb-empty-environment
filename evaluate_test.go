package bootpolicy

import "testing"

func digestOf(h byte) Digest {
	return Digest{Algorithm: "sha256", Hash: []byte{h}}
}

func component(signer, label string, gen uint32, loc string, root string, d Digest) Component {
	return Component{SignerID: signer, Label: label, Generation: gen, Location: loc, RootID: root, Digest: d}
}

func TestPrefixAndGenerationBoundaries(t *testing.T) {
	deny := Entry{
		Kind: KindDeny, SignerID: "vendor-a", LabelPrefix: "bootloader",
		GenLo: 10, GenHi: 20,
	}
	p := &Policy{Entries: []Entry{deny}}

	cases := []struct {
		name string
		comp Component
		want bool
	}{
		{"inside range", component("vendor-a", "bootloader.elf", 15, "", "", digestOf(1)), false},
		{"lower boundary", component("vendor-a", "bootloader.elf", 10, "", "", digestOf(1)), false},
		{"upper boundary", component("vendor-a", "bootloader.elf", 20, "", "", digestOf(1)), false},
		{"below range", component("vendor-a", "bootloader.elf", 9, "", "", digestOf(1)), true},
		{"above range", component("vendor-a", "bootloader.elf", 21, "", "", digestOf(1)), true},
		{"label outside prefix", component("vendor-a", "kernel.elf", 15, "", "", digestOf(1)), true},
		{"signer mismatch", component("vendor-b", "bootloader.elf", 15, "", "", digestOf(1)), true},
		{"prefix is exact label", component("vendor-a", "bootloader", 10, "", "", digestOf(1)), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := LoadEvent{Child: tc.comp}
			if got := p.EventAdmit(ev); got != tc.want {
				t.Fatalf("EventAdmit = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLocationSelector(t *testing.T) {
	p := &Policy{Entries: []Entry{{
		Kind: KindDeny, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: maxUint32,
		Location: "spi-flash",
	}}}
	if p.EventAdmit(LoadEvent{Child: component("s", "c", 0, "spi-flash", "", digestOf(1))}) {
		t.Fatal("scoped location must be denied")
	}
	if !p.EventAdmit(LoadEvent{Child: component("s", "c", 0, "emmc", "", digestOf(1))}) {
		t.Fatal("a different location must be admitted")
	}
}

func TestRecoveryExemptionExactDigestAndExpiry(t *testing.T) {
	deny := Entry{Kind: KindDeny, SignerID: "s", LabelPrefix: "fw", GenLo: 0, GenHi: maxUint32}
	exempt := Entry{
		Kind: KindExempt, SignerID: "s", LabelPrefix: "fw",
		GenLo: 0, GenHi: maxUint32, RootID: "root-1",
		Digest: digestOf(0xAA), NotAfter: 100,
	}
	p := &Policy{Entries: []Entry{deny, exempt}}

	good := component("s", "fw", 5, "", "root-1", digestOf(0xAA))
	wrongDigest := component("s", "fw", 5, "", "root-1", digestOf(0xBB))
	wrongRoot := component("s", "fw", 5, "", "root-2", digestOf(0xAA))

	if !p.EventAdmit(LoadEvent{BootTime: 100, Child: good}) {
		t.Fatal("valid recovery image at NotAfter must be admitted")
	}
	if p.EventAdmit(LoadEvent{BootTime: 101, Child: good}) {
		t.Fatal("expired recovery image must be rejected")
	}
	if p.EventAdmit(LoadEvent{BootTime: 0, Child: wrongDigest}) {
		t.Fatal("exemption must not cover a different digest")
	}
	if p.EventAdmit(LoadEvent{BootTime: 0, Child: wrongRoot}) {
		t.Fatal("exemption must not apply under a different trusted root")
	}
}

func TestParentScopesDoNotShareExemptions(t *testing.T) {
	deny := Entry{Kind: KindDeny, SignerID: "s", LabelPrefix: "lib", GenLo: 0, GenHi: maxUint32}
	exempt := Entry{
		Kind: KindExempt, SignerID: "s", LabelPrefix: "lib",
		GenLo: 0, GenHi: maxUint32, Digest: digestOf(9),
		Parent: &ParentScope{SignerID: "s", Label: "trusted-bl"},
	}
	p := &Policy{Entries: []Entry{deny, exempt}}

	child := component("s", "lib", 1, "", "", digestOf(9))
	trustedParent := Component{SignerID: "s", Label: "trusted-bl", Digest: digestOf(2)}
	otherParent := Component{SignerID: "s", Label: "other-bl", Digest: digestOf(3)}

	if !p.EventAdmit(LoadEvent{Child: child, Parent: &trustedParent}) {
		t.Fatal("scoped exemption must apply under its named parent")
	}
	if p.EventAdmit(LoadEvent{Child: child, Parent: &otherParent}) {
		t.Fatal("identically labeled component under another parent must not share the exemption")
	}
	if p.EventAdmit(LoadEvent{Child: child}) {
		t.Fatal("root load must not match a parent-scoped exemption")
	}
}

func TestExemptionDoesNotTransferToDependents(t *testing.T) {
	denyLib := Entry{Kind: KindDeny, SignerID: "s", LabelPrefix: "lib", GenLo: 0, GenHi: maxUint32}
	exemptLib := Entry{
		Kind: KindExempt, SignerID: "s", LabelPrefix: "lib",
		GenLo: 0, GenHi: maxUint32, Digest: digestOf(9),
	}
	denyApp := Entry{Kind: KindDeny, SignerID: "s", LabelPrefix: "app", GenLo: 0, GenHi: maxUint32}
	p := &Policy{Entries: []Entry{denyLib, exemptLib, denyApp}}

	lib := component("s", "lib", 1, "", "", digestOf(9))
	app := component("s", "app", 1, "", "", digestOf(4))
	in := BootInput{
		Components: []Component{lib, app},
		Edges:      []LoadEdge{{ParentIndex: 0, ChildIndex: 1}},
	}
	admit, err := p.Admit(in)
	if err != nil {
		t.Fatal(err)
	}
	if admit {
		t.Fatal("an exempted parent must not transfer its exemption to a denied dependent")
	}
}

func TestBootInputValidationAndAdmission(t *testing.T) {
	p := &Policy{Entries: []Entry{
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "bad", GenLo: 0, GenHi: maxUint32},
	}}
	good := component("s", "good", 1, "", "", digestOf(1))
	bad := component("s", "bad", 1, "", "", digestOf(2))

	in := BootInput{Components: []Component{good, bad}, Edges: []LoadEdge{{ParentIndex: 0, ChildIndex: 1}}}
	admit, err := p.Admit(in)
	if err != nil {
		t.Fatal(err)
	}
	if admit {
		t.Fatal("boot with a denied child must be rejected")
	}

	okIn := BootInput{Components: []Component{good}}
	admit, err = p.Admit(okIn)
	if err != nil || !admit {
		t.Fatalf("clean boot: admit=%v err=%v", admit, err)
	}

	if _, err := p.Admit(BootInput{}); err != ErrInvalidInput {
		t.Fatalf("empty input error = %v", err)
	}
	big := BootInput{Components: make([]Component, MaxComponents+1)}
	for i := range big.Components {
		big.Components[i] = good
	}
	if _, err := p.Admit(big); err != ErrInvalidInput {
		t.Fatalf("oversized input error = %v", err)
	}
}
