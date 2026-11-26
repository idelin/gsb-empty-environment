package devpolicy

import "testing"

func TestParseErrorPaths(t *testing.T) {
	if _, err := ParsePerm(""); err == nil {
		t.Fatal("empty perm should error")
	}
	invalidIntervals := []string{"8..3", "abc", "99999", "5..99999"}
	for _, in := range invalidIntervals {
		if _, _, err := ParseInterval(in); err == nil {
			t.Fatalf("interval %q should error", in)
		}
	}
	if lo, hi, err := ParseInterval("7"); err != nil || lo != 7 || hi != 7 {
		t.Fatalf("singleton interval broken: %d..%d %v", lo, hi, err)
	}
	if lo, hi, err := ParseInterval("0..*"); err != nil || lo != 0 || hi != MaxDeviceNo {
		t.Fatalf("wildcard upper bound broken: %d..%d %v", lo, hi, err)
	}

	invalidRules := []string{
		"allow c * r",      // too few fields
		"allow c * * r x",  // too many fields
		"maybe c * * r",    // bad effect
		"allow z * * r",    // bad type
		"allow c * * rwx",  // bad permission
		"allow c 9..1 * r", // inverted interval
	}
	for _, line := range invalidRules {
		if _, err := ParseRule(line); err == nil {
			t.Fatalf("rule %q should error", line)
		}
	}
}

func TestSpecCompileErrorPropagation(t *testing.T) {
	spec := &Spec{}
	spec.Entries = append(spec.Entries, SpecEntry{
		Allow: true, Type: TypeChar,
		MajorMin: 1, MajorMax: 0, // inverted
		MinorMin: 0, MinorMax: 1, Perm: BitR,
	})
	if _, err := spec.Compile(); err == nil {
		t.Fatal("spec compile should propagate invalid entry")
	}
	if _, err := (&Policy{}).CompileDomain(MaxDeviceNo + 1); err == nil {
		t.Fatal("domain above 4095 should be rejected")
	}
}
