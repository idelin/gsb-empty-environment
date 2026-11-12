package netsec

import (
	"errors"
	"testing"
)

var (
	policyA = Policy{
		Version:             2,
		MasterKeyIdentifier: "0123456789abcdef",
		ContentsMode:        ModeAES256XTS,
		FilenamesMode:       ModeAES256CTS,
		Flags:               FlagPad16,
	}
	policyB = Policy{
		Version:             2,
		MasterKeyIdentifier: "fedcba9876543210",
		ContentsMode:        ModeAES256XTS,
		FilenamesMode:       ModeAES256CTS,
		Flags:               FlagPad16,
	}
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

func policyOf(t *testing.T, fs *FS, path string) *Policy {
	t.Helper()
	p, err := fs.GetPolicy(path)
	must(t, err)
	return p
}

func TestCreateInheritsParentPolicy(t *testing.T) {
	fs := NewFS()
	must(t, fs.Mkdir("/enc"))
	must(t, fs.SetPolicy("/enc", policyA))
	must(t, fs.Create("/enc/file"))
	must(t, fs.Mkdir("/enc/sub"))
	must(t, fs.Create("/enc/sub/nested"))

	for _, p := range []string{"/enc/file", "/enc/sub", "/enc/sub/nested"} {
		got := policyOf(t, fs, p)
		if !got.Equal(&policyA) {
			t.Fatalf("%s: policy %+v does not match inherited %+v", p, got, policyA)
		}
	}

	must(t, fs.Mkdir("/plain"))
	must(t, fs.Create("/plain/file"))
	if got := policyOf(t, fs, "/plain/file"); got != nil {
		t.Fatalf("plaintext parent produced encrypted child: %+v", got)
	}
}

func TestSetPolicyConstraints(t *testing.T) {
	fs := NewFS()
	must(t, fs.Mkdir("/nonempty"))
	must(t, fs.Create("/nonempty/file"))
	mustErr(t, fs.SetPolicy("/nonempty", policyA), ErrNotEmpty)

	must(t, fs.Mkdir("/enc"))
	must(t, fs.SetPolicy("/enc", policyA))
	mustErr(t, fs.SetPolicy("/enc", policyB), ErrPolicyAlreadySet)

	must(t, fs.Create("/regular"))
	mustErr(t, fs.SetPolicy("/regular", policyA), ErrNotDir)
}

func newMoveFS(t *testing.T) *FS {
	t.Helper()
	fs := NewFS()
	must(t, fs.Mkdir("/encA"))
	must(t, fs.SetPolicy("/encA", policyA))
	must(t, fs.Mkdir("/encB"))
	must(t, fs.SetPolicy("/encB", policyB))
	must(t, fs.Mkdir("/plain"))
	return fs
}

func TestRenameCrossPolicy(t *testing.T) {
	t.Run("different keys", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Create("/encA/secret"))
		mustErr(t, fs.Rename("/encA/secret", "/encB/secret"), ErrPolicyMismatch)
		if got := policyOf(t, fs, "/encA/secret"); !got.Equal(&policyA) {
			t.Fatalf("source lost after failed rename: %+v", got)
		}
	})

	t.Run("plaintext into encrypted", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Create("/plain/evil"))
		mustErr(t, fs.Rename("/plain/evil", "/encA/evil"), ErrPolicyMismatch)
	})

	t.Run("encrypted into plaintext", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Create("/encA/secret"))
		mustErr(t, fs.Rename("/encA/secret", "/plain/leak"), ErrPolicyMismatch)
	})

	t.Run("same policy allowed", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Mkdir("/encA2"))
		must(t, fs.SetPolicy("/encA2", policyA))
		must(t, fs.Create("/encA/secret"))
		must(t, fs.Rename("/encA/secret", "/encA2/moved"))
		if got := policyOf(t, fs, "/encA2/moved"); !got.Equal(&policyA) {
			t.Fatalf("moved file policy changed: %+v", got)
		}
	})

	t.Run("same directory rename", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Create("/encA/one"))
		must(t, fs.Rename("/encA/one", "/encA/two"))
		if _, err := fs.GetPolicy("/encA/one"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("old name still resolves: %v", err)
		}
	})

	t.Run("directory subtree keeps policy", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Mkdir("/encA2"))
		must(t, fs.SetPolicy("/encA2", policyA))
		must(t, fs.Mkdir("/encA/dir"))
		must(t, fs.Create("/encA/dir/inner"))
		must(t, fs.Rename("/encA/dir", "/encA2/dir"))
		if got := policyOf(t, fs, "/encA2/dir/inner"); !got.Equal(&policyA) {
			t.Fatalf("subtree policy changed: %+v", got)
		}
		mustErr(t, fs.Rename("/encA2/dir", "/encB/dir"), ErrPolicyMismatch)
	})
}

func TestLinkPolicyEnforcement(t *testing.T) {
	t.Run("plaintext linked into encrypted dir rejected", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Create("/plain/evil"))
		mustErr(t, fs.Link("/plain/evil", "/encA/evil"), ErrPolicyMismatch)
	})

	t.Run("ciphertext linked into plaintext dir rejected", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Create("/encA/secret"))
		mustErr(t, fs.Link("/encA/secret", "/plain/leak"), ErrPolicyMismatch)
	})

	t.Run("different key linked rejected", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Create("/encA/secret"))
		mustErr(t, fs.Link("/encA/secret", "/encB/secret"), ErrPolicyMismatch)
	})

	t.Run("same policy link allowed and shared", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Mkdir("/encA2"))
		must(t, fs.SetPolicy("/encA2", policyA))
		must(t, fs.Create("/encA/secret"))
		must(t, fs.Link("/encA/secret", "/encA2/alias"))
		must(t, fs.Rename("/encA/secret", "/encA/gone"))
		if got := policyOf(t, fs, "/encA2/alias"); !got.Equal(&policyA) {
			t.Fatalf("hard link target lost policy: %+v", got)
		}
	})

	t.Run("directories cannot be hard linked", func(t *testing.T) {
		fs := newMoveFS(t)
		mustErr(t, fs.Link("/encA", "/encA2"), ErrIsDir)
	})
}

func TestExchangePolicyEnforcement(t *testing.T) {
	t.Run("cross policy exchange rejected", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Create("/encA/a"))
		must(t, fs.Create("/encB/b"))
		mustErr(t, fs.Exchange("/encA/a", "/encB/b"), ErrPolicyMismatch)
	})

	t.Run("plaintext swap with encrypted rejected", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Create("/encA/secret"))
		must(t, fs.Create("/plain/evil"))
		mustErr(t, fs.Exchange("/encA/secret", "/plain/evil"), ErrPolicyMismatch)
	})

	t.Run("same policy exchange swaps entries", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Mkdir("/encA2"))
		must(t, fs.SetPolicy("/encA2", policyA))
		must(t, fs.Create("/encA/first"))
		must(t, fs.Create("/encA2/second"))
		must(t, fs.Exchange("/encA/first", "/encA2/second"))
		if got := policyOf(t, fs, "/encA/first"); !got.Equal(&policyA) {
			t.Fatalf("first: %+v", got)
		}
		if got := policyOf(t, fs, "/encA2/second"); !got.Equal(&policyA) {
			t.Fatalf("second: %+v", got)
		}
	})

	t.Run("same directory exchange", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Create("/encA/x"))
		must(t, fs.Create("/encA/y"))
		must(t, fs.Exchange("/encA/x", "/encA/y"))
	})

	t.Run("missing entry rejected", func(t *testing.T) {
		fs := newMoveFS(t)
		must(t, fs.Create("/encA/x"))
		mustErr(t, fs.Exchange("/encA/x", "/encA/nope"), ErrNotFound)
	})
}

func TestPolicyImmutabilityAfterPopulation(t *testing.T) {
	fs := NewFS()
	must(t, fs.Mkdir("/enc"))
	must(t, fs.SetPolicy("/enc", policyA))
	must(t, fs.Create("/enc/file"))
	p := policyA
	p.MasterKeyIdentifier = "attacker-controlled"
	mustErr(t, fs.SetPolicy("/enc", p), ErrPolicyAlreadySet)
	if got := policyOf(t, fs, "/enc"); got.MasterKeyIdentifier != policyA.MasterKeyIdentifier {
		t.Fatalf("stored policy mutated: %+v", got)
	}
}
