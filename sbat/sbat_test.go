package sbat

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

const validSBAT = "sbat,1,2021030218\n" +
	"shim,2,acme,https://example.com/shim\n" +
	"grub,3,acme,https://example.com/grub\n"

func sectionTable(rawSize uint32) []PESection {
	return []PESection{
		{Name: ".text", VirtualAddr: 0x1000, RawSize: 0x2000},
		{Name: ".sbat\x00\x00\x00", VirtualAddr: 0x4000, RawSize: rawSize},
	}
}

func TestParseValid(t *testing.T) {
	for _, content := range []string{
		validSBAT,
		strings.ReplaceAll(validSBAT, "\n", "\r\n"),
		strings.TrimSuffix(validSBAT, "\n"),
		"sbat,1,2021030218\nshim,2,acme\n",
	} {
		entries, err := Parse([]byte(content))
		if err != nil {
			t.Fatalf("Parse(%q) unexpected error: %v", content, err)
		}
		want := []Entry{
			{Component: "sbat", Generation: 1},
			{Component: "shim", Generation: 2},
		}
		if len(entries) < len(want) {
			t.Fatalf("Parse(%q) = %v, want at least %v", content, entries, want)
		}
		for i, entry := range want {
			if entries[i] != entry {
				t.Fatalf("Parse(%q) entry %d = %v, want %v", content, i, entries[i], entry)
			}
		}
	}
}

func TestParseRejectsMalformedHeaderAndLines(t *testing.T) {
	cases := map[string]error{
		"":                                                        ErrMalformed,
		"sbat,2,2021030218\nshim,2,acme\n":                        ErrMalformed,
		"shim,2,acme\n":                                           ErrMalformed,
		"sbat,1,2021030218\n\nshim,2,acme\n":                      ErrMalformed,
		"sbat,1,2021030218\nshim,2\n":                             ErrMalformed,
		"sbat,1,2021030218\nshim\n":                               ErrMalformed,
		"sbat,1,2021030218\nshim,,acme\n":                         ErrInvalidGeneration,
		"sbat,1,2021030218\nshim,abc,acme\n":                      ErrInvalidGeneration,
		"sbat,1,2021030218\nshim,1.5,acme\n":                      ErrInvalidGeneration,
		"sbat,1,2021030218\nshim,+2,acme\n":                       ErrInvalidGeneration,
		"sbat,1,2021030218\nshim, 2,acme\n":                       ErrInvalidGeneration,
		"sbat,1,2021030218\nshim,0x2,acme\n":                      ErrInvalidGeneration,
		"sbat,1,2021030218\nshim,999999999999999999999999,acme\n": ErrInvalidGeneration,
		"sbat,1,2021030218\nshim,2,acme\nshim,3,acme\n":           ErrDuplicateComponent,
		"sbat,1,2021030218\nsh im,2,acme\n":                       ErrMalformed,
		"sbat,1,2021030218\nshim\t,2,acme\n":                      ErrMalformed,
	}
	for content, want := range cases {
		_, err := Parse([]byte(content))
		if !errors.Is(err, want) {
			t.Errorf("Parse(%q) error = %v, want error wrapping %v", content, err, want)
		}
	}
}

func TestCheckRevokesLowGeneration(t *testing.T) {
	entries, err := Parse([]byte(validSBAT))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if err := Check(entries, map[string]uint64{"shim": 2, "grub": 3}); err != nil {
		t.Fatalf("Check at matching generations failed: %v", err)
	}

	err = Check(entries, map[string]uint64{"shim": 3})
	var revoked *RevocationError
	if !errors.As(err, &revoked) {
		t.Fatalf("Check rollback error = %v, want *RevocationError", err)
	}
	if revoked.Component != "shim" || revoked.Generation != 2 || revoked.Minimum != 3 {
		t.Fatalf("RevocationError = %+v, want shim 2 below 3", revoked)
	}
}

func TestOldGenerationFallbackRejected(t *testing.T) {
	// An attacker submits an older, still-validly-signed shim (generation 1)
	// after the policy revoked everything below generation 2.
	old := []byte("sbat,1,2020010100\nshim,1,acme,https://example.com/shim\n")
	policy := map[string]uint64{"shim": 2}

	err := CheckSection(sectionTable(uint32(len(old))), old, policy)
	var revoked *RevocationError
	if !errors.As(err, &revoked) {
		t.Fatalf("fallback error = %v, want *RevocationError", err)
	}
	if revoked.Component != "shim" || revoked.Generation != 1 || revoked.Minimum != 2 {
		t.Fatalf("RevocationError = %+v", revoked)
	}
}

func TestExtraFieldsCannotBypassComparison(t *testing.T) {
	// Padding the vulnerable record with extra CSV fields must not hide it.
	padded := []byte("sbat,1,2021030218\nshim,1,acme,https://example.com,extra,padding\n")
	err := CheckSection(sectionTable(uint32(len(padded))), padded,
		map[string]uint64{"shim": 2})
	var revoked *RevocationError
	if !errors.As(err, &revoked) {
		t.Fatalf("padded-line error = %v, want *RevocationError", err)
	}
}

func TestMissingSectionRejected(t *testing.T) {
	table := []PESection{{Name: ".text", VirtualAddr: 0x1000, RawSize: 0x2000}}
	err := CheckSection(table, []byte(validSBAT), map[string]uint64{})
	if !errors.Is(err, ErrSectionMissing) {
		t.Fatalf("missing-section error = %v, want ErrSectionMissing", err)
	}
}

func TestRawSizeMismatchRejected(t *testing.T) {
	content := []byte(validSBAT)
	err := CheckSection(sectionTable(uint32(len(content))+1), content, map[string]uint64{})
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("size-mismatch error = %v, want ErrSizeMismatch", err)
	}
}

func TestDuplicateComponentRejected(t *testing.T) {
	duplicate := []byte("sbat,1,2021030218\n" +
		"grub,2,acme\n" +
		"grub,3,acme\n")
	err := CheckSection(sectionTable(uint32(len(duplicate))), duplicate, map[string]uint64{})
	if !errors.Is(err, ErrDuplicateComponent) {
		t.Fatalf("duplicate error = %v, want ErrDuplicateComponent", err)
	}
}

func TestNonNumericGenerationRejected(t *testing.T) {
	bad := []byte("sbat,1,2021030218\ngrub,two,acme\n")
	err := CheckSection(sectionTable(uint32(len(bad))), bad, map[string]uint64{})
	if !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("non-numeric error = %v, want ErrInvalidGeneration", err)
	}
}

func TestPolicyUpdateIsMonotonic(t *testing.T) {
	store := NewPolicyStore(map[string]uint64{"shim": 2, "grub": 3})

	if err := store.Update(map[string]uint64{"shim": 1, "grub": 3}); !errors.Is(err, ErrPolicyRollback) {
		t.Fatalf("lower-generation update error = %v, want ErrPolicyRollback", err)
	}
	if min, ok := store.Minimum("shim"); !ok || min != 2 {
		t.Fatalf("store mutated by rejected rollback: shim = %d (%v)", min, ok)
	}

	if err := store.Update(map[string]uint64{"shim": 2}); !errors.Is(err, ErrPolicyRollback) {
		t.Fatalf("component-removal update error = %v, want ErrPolicyRollback", err)
	}
	if min, ok := store.Minimum("grub"); !ok || min != 3 {
		t.Fatalf("store mutated by component removal: grub = %d (%v)", min, ok)
	}

	if err := store.Update(map[string]uint64{"shim": 2, "grub": 3}); err != nil {
		t.Fatalf("equal-generation update failed: %v", err)
	}
	if err := store.Update(map[string]uint64{"shim": 4, "grub": 3, "mokmgr": 1}); err != nil {
		t.Fatalf("raising update with new component failed: %v", err)
	}
	if min, _ := store.Minimum("shim"); min != 4 {
		t.Fatalf("shim minimum = %d, want 4", min)
	}
	if min, _ := store.Minimum("mokmgr"); min != 1 {
		t.Fatalf("mokmgr minimum = %d, want 1", min)
	}

	// A revoked generation cannot be restored by replaying an old revision.
	oldRevision := map[string]uint64{"shim": 2, "grub": 3, "mokmgr": 1}
	if err := store.Update(oldRevision); !errors.Is(err, ErrPolicyRollback) {
		t.Fatalf("policy replay error = %v, want ErrPolicyRollback", err)
	}
	if min, _ := store.Minimum("shim"); min != 4 {
		t.Fatalf("rollback restored revoked generation: shim = %d, want 4", min)
	}
}

func TestValidateImageEndToEnd(t *testing.T) {
	store := NewPolicyStore(map[string]uint64{"shim": 2, "grub": 3})

	content := []byte(validSBAT)
	if err := store.ValidateImage(sectionTable(uint32(len(content))), content); err != nil {
		t.Fatalf("valid image rejected: %v", err)
	}

	if err := store.Update(map[string]uint64{"shim": 3, "grub": 3}); err != nil {
		t.Fatalf("policy raise failed: %v", err)
	}
	if err := store.ValidateImage(sectionTable(uint32(len(content))), content); err == nil {
		t.Fatal("image with shim generation 2 accepted after revocation raised to 3")
	}
}

func TestSnapshotIsIndependent(t *testing.T) {
	store := NewPolicyStore(map[string]uint64{"shim": 2})
	snapshot := store.Snapshot()
	snapshot["shim"] = 1
	snapshot["injected"] = 99

	if min, _ := store.Minimum("shim"); min != 2 {
		t.Fatalf("store affected by mutating snapshot: shim = %d", min)
	}
	if _, ok := store.Minimum("injected"); ok {
		t.Fatal("store affected by component injected into snapshot")
	}
}

func TestConcurrentPolicyAccess(t *testing.T) {
	store := NewPolicyStore(map[string]uint64{"shim": 1})
	content := []byte(validSBAT)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = store.ValidateImage(sectionTable(uint32(len(content))), content)
				_ = store.Snapshot()
				_ = store.Update(map[string]uint64{
					"shim": uint64(i*50 + j + 1),
					"grub": 3,
				})
			}
		}(i)
	}
	wg.Wait()

	min, _ := store.Minimum("shim")
	if min < 1 {
		t.Fatalf("concurrent updates lowered policy: %d", min)
	}
}

func TestRevocationErrorMessage(t *testing.T) {
	err := &RevocationError{Component: "grub", Generation: 1, Minimum: 4}
	if !strings.Contains(err.Error(), "grub") || !strings.Contains(err.Error(), fmt.Sprint(4)) {
		t.Fatalf("unexpected message: %q", err.Error())
	}
}
