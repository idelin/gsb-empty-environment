package main

import "testing"

func bit(name string) CapMask {
	m, err := parseCapSet(name)
	if err != nil {
		panic(err)
	}
	return m
}

// exec of a privileged file: P' = (I&fI)|(B&fP)|A, fE=1 makes E' = P',
// and the ambient set is cleared.
func TestExecPrivilegedFileCaps(t *testing.T) {
	s := initialState()
	err := s.applyExec(Event{
		Line:   1,
		Kind:   "exec",
		Target: "/bin/x",
		FP:     bit("cap_net_raw") | bit("cap_setpcap"),
		FE:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := bit("cap_net_raw") | bit("cap_setpcap")
	if s.Permitted != want || s.Effective != want {
		t.Fatalf("P=%s E=%s, want %s", renderSet(s.Permitted), renderSet(s.Effective), renderSet(want))
	}
	if s.Ambient != 0 {
		t.Fatalf("ambient must be cleared by privileged exec, got %s", renderSet(s.Ambient))
	}
	if s.firstEffective[13].Step != 1 || s.firstEffective[8].Step != 1 {
		t.Fatalf("missing first-effective evidence: %+v", s.firstEffective)
	}
}

// Bounding set clips file permitted caps: a cap missing from B never reaches P'.
func TestExecBoundingClipsFilePermitted(t *testing.T) {
	s := initialState()
	s.Bounding &^= bit("cap_bpf")
	err := s.applyExec(Event{
		Line: 2, Kind: "exec", Target: "/bin/x",
		FP: bit("cap_net_raw") | bit("cap_bpf"), FE: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Permitted&bit("cap_bpf") != 0 {
		t.Fatalf("cap_bpf must be clipped by bounding set, P=%s", renderSet(s.Permitted))
	}
	if s.Permitted&bit("cap_net_raw") == 0 {
		t.Fatalf("cap_net_raw should survive via B&fP, P=%s", renderSet(s.Permitted))
	}
}

// Inheritable file caps require the thread's own inheritable bit: P' = I & fI.
func TestExecInheritablePath(t *testing.T) {
	s := initialState()
	s.Inheritable = bit("cap_net_raw")
	err := s.applyExec(Event{
		Line: 3, Kind: "exec", Target: "/bin/x",
		FI: bit("cap_net_raw") | bit("cap_chown"), FE: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Permitted != bit("cap_net_raw") {
		t.Fatalf("only I&fI should land in P, got %s", renderSet(s.Permitted))
	}
	// I survives exec unchanged.
	if s.Inheritable != bit("cap_net_raw") {
		t.Fatalf("inheritable changed across exec: %s", renderSet(s.Inheritable))
	}
}

// Unprivileged exec with fE=0: E' = A and ambient survives.
func TestExecUnprivilegedAmbientSurvives(t *testing.T) {
	s := initialState()
	s.Permitted = bit("cap_net_raw")
	s.Inheritable = bit("cap_net_raw")
	s.Ambient = bit("cap_net_raw")
	err := s.applyExec(Event{Line: 4, Kind: "exec", Target: "/bin/plain"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Effective != bit("cap_net_raw") || s.Ambient != bit("cap_net_raw") {
		t.Fatalf("ambient must drive E and survive unprivileged exec, E=%s A=%s",
			renderSet(s.Effective), renderSet(s.Ambient))
	}
}

// nosuid mounts ignore file capabilities and suid/sgid bits.
func TestExecNosuidIgnoresFileCaps(t *testing.T) {
	s := initialState()
	err := s.applyExec(Event{
		Line: 5, Kind: "exec", Target: "/mnt/nosuid/x",
		FP: bit("cap_chown"), FE: true, SUID: true, Nosuid: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Permitted != 0 || s.Effective != 0 || s.Ambient != 0 {
		t.Fatalf("nosuid must suppress file caps: P=%s E=%s A=%s",
			renderSet(s.Permitted), renderSet(s.Effective), renderSet(s.Ambient))
	}
}

// capset rules: P' subset of old P, E' subset of new P, and I gating without
// CAP_SETPCAP.
func TestCapsetPermissionChecks(t *testing.T) {
	s := initialState()
	s.Permitted = bit("cap_chown") | bit("cap_net_raw")
	s.Effective = bit("cap_chown")

	// Raising P is forbidden.
	err := s.applyCapset(Event{Line: 1, P: s.Permitted | bit("cap_kill"), I: 0, E: 0})
	if err == nil {
		t.Fatal("expected EPERM when raising permitted set")
	}
	// E outside new P is forbidden.
	err = s.applyCapset(Event{Line: 2, P: bit("cap_chown"), I: 0, E: bit("cap_net_raw")})
	if err == nil {
		t.Fatal("expected EPERM when effective exceeds new permitted")
	}
	// Without cap_setpcap, I can only use old I|old P.
	err = s.applyCapset(Event{Line: 3, P: s.Permitted, I: bit("cap_bpf"), E: 0})
	if err == nil {
		t.Fatal("expected EPERM adding inheritable cap outside I|P without cap_setpcap")
	}
	// Inheritable from old P is allowed, and dropping bits is allowed.
	err = s.applyCapset(Event{Line: 4, P: bit("cap_chown"), I: bit("cap_net_raw"), E: bit("cap_chown")})
	if err != nil {
		t.Fatalf("valid capset rejected: %v", err)
	}
}

// With CAP_SETPCAP effective, inheritable may be raised from the permitted set.
func TestCapsetWithSetPCAPRaisesInheritable(t *testing.T) {
	s := initialState()
	s.Permitted = bit("cap_setpcap") | bit("cap_net_raw")
	s.Effective = bit("cap_setpcap")
	err := s.applyCapset(Event{
		Line: 1,
		P:    s.Permitted,
		I:    bit("cap_net_raw"),
		E:    bit("cap_setpcap"),
	})
	if err != nil {
		t.Fatalf("cap_setpcap should permit inheritable raise: %v", err)
	}
}

// Ambient raise requires P&I; capset/cap drops automatically pull A back in.
func TestAmbientInvariant(t *testing.T) {
	s := initialState()
	s.Permitted = bit("cap_net_raw")
	s.Inheritable = bit("cap_net_raw")

	if err := s.applyAmbient(Event{Line: 1, Mode: "raise", Caps: bit("cap_chown")}); err == nil {
		t.Fatal("expected EPERM raising ambient cap not in P&I")
	}
	if err := s.applyAmbient(Event{Line: 2, Mode: "raise", Caps: bit("cap_net_raw")}); err != nil {
		t.Fatal(err)
	}
	// Lowering P below A makes the kernel drop the ambient bit too.
	err := s.applyCapset(Event{Line: 3, P: 0, I: bit("cap_net_raw"), E: 0})
	if err != nil {
		t.Fatal(err)
	}
	if s.Ambient != 0 {
		t.Fatalf("ambient must be reined in to P&I, got %s", renderSet(s.Ambient))
	}
}

// PR_CAPBSET_DROP needs cap_setpcap and trims every thread set.
func TestBoundingDrop(t *testing.T) {
	s := initialState()
	err := s.applyBounding(Event{Line: 1, Caps: bit("cap_bpf")})
	if err == nil {
		t.Fatal("expected EPERM dropping bounding cap without cap_setpcap")
	}

	s.Permitted = bit("cap_setpcap") | bit("cap_bpf")
	s.Effective = bit("cap_setpcap") | bit("cap_bpf")
	s.Ambient = bit("cap_bpf")
	s.Inheritable = bit("cap_bpf")
	if err := s.applyBounding(Event{Line: 2, Caps: bit("cap_bpf")}); err != nil {
		t.Fatal(err)
	}
	for name, set := range map[string]CapMask{"P": s.Permitted, "I": s.Inheritable, "E": s.Effective, "A": s.Ambient, "B": s.Bounding} {
		if set&bit("cap_bpf") != 0 {
			t.Fatalf("%s still contains cap_bpf after bounding drop", name)
		}
	}
}

func TestParseCapSetForms(t *testing.T) {
	cases := map[string]CapMask{
		"":                       0,
		"none":                   0,
		"all":                    AllCaps,
		"cap_chown, cap_net_raw": bit("cap_chown") | bit("cap_net_raw"),
		"0,13":                   bit("cap_chown") | bit("cap_net_raw"),
	}
	for in, want := range cases {
		got, err := parseCapSet(in)
		if err != nil || got != want {
			t.Fatalf("parseCapSet(%q) = %s, %v; want %s", in, renderSet(got), err, renderSet(want))
		}
	}
	if _, err := parseCapSet("cap_nope"); err == nil {
		t.Fatal("expected error for unknown capability")
	}
	if _, err := parseCapSet("99"); err == nil {
		t.Fatal("expected error above CAP_LAST_CAP")
	}
}
