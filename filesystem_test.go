package netsec_test

import (
	"errors"
	"testing"

	netsec "example.org/netsec/q0311"
)

func TestPolicyInheritanceCopiesEveryField(t *testing.T) {
	fs := netsec.NewFS()
	want := testPolicy(1)

	must(t, fs.Mkdir("/secure"))
	must(t, fs.SetPolicy("/secure", want))
	must(t, fs.Mkdir("/secure/nested"))
	must(t, fs.Create("/secure/nested/file"))

	for _, name := range []string{"/secure", "/secure/nested", "/secure/nested/file"} {
		got := policyAt(t, fs, name)
		if !got.Equal(want) {
			t.Fatalf("policy at %s = %#v, want %#v", name, got, want)
		}
	}

	// Returned policies are snapshots and must not be a route to mutate the
	// directory's policy.
	got := policyAt(t, fs, "/secure")
	got.Flags = 0
	got.MasterKeyID[0] ^= 0xff
	if current := policyAt(t, fs, "/secure"); !current.Equal(want) {
		t.Fatalf("mutating returned policy changed filesystem policy: %#v", current)
	}
}

func TestSetPolicyRequiresEmptyDirectoryAndIsImmutable(t *testing.T) {
	fs := netsec.NewFS()
	first := testPolicy(2)
	second := testPolicy(3)

	must(t, fs.Mkdir("/plain"))
	must(t, fs.Create("/plain/file"))
	if err := fs.SetPolicy("/plain", first); !errors.Is(err, netsec.ErrNotEmpty) {
		t.Fatalf("SetPolicy(non-empty) error = %v, want ErrNotEmpty", err)
	}
	if got := policyAt(t, fs, "/plain"); got != nil {
		t.Fatalf("non-empty plaintext directory acquired policy %#v", got)
	}

	must(t, fs.Mkdir("/secure"))
	must(t, fs.SetPolicy("/secure", first))
	if err := fs.SetPolicy("/secure", second); !errors.Is(err, netsec.ErrPolicyAlreadySet) {
		t.Fatalf("SetPolicy(existing) error = %v, want ErrPolicyAlreadySet", err)
	}
	if got := policyAt(t, fs, "/secure"); !got.Equal(first) {
		t.Fatalf("existing policy changed from %#v to %#v", first, got)
	}
	if err := fs.SetPolicy("/plain/file", first); !errors.Is(err, netsec.ErrNotDirectory) {
		t.Fatalf("SetPolicy(file) error = %v, want ErrNotDirectory", err)
	}
}

func TestMkdirWithPolicyMustMatchEncryptedParent(t *testing.T) {
	fs := netsec.NewFS()
	parent := testPolicy(4)
	other := testPolicy(5)

	must(t, fs.Mkdir("/secure"))
	must(t, fs.SetPolicy("/secure", parent))
	if err := fs.MkdirWithPolicy("/secure/bad", other); !errors.Is(err, netsec.ErrPolicyMismatch) {
		t.Fatalf("MkdirWithPolicy(different) error = %v, want ErrPolicyMismatch", err)
	}
	if _, err := fs.Stat("/secure/bad"); !errors.Is(err, netsec.ErrNotFound) {
		t.Fatalf("failed MkdirWithPolicy left a directory behind: %v", err)
	}
	must(t, fs.MkdirWithPolicy("/secure/good", parent))
	if got := policyAt(t, fs, "/secure/good"); !got.Equal(parent) {
		t.Fatalf("MkdirWithPolicy policy = %#v, want %#v", got, parent)
	}

	// A new empty directory in a plaintext parent may opt into encryption.
	must(t, fs.MkdirWithPolicy("/new-secure", other))
	if got := policyAt(t, fs, "/new-secure"); !got.Equal(other) {
		t.Fatalf("new protected directory policy = %#v, want %#v", got, other)
	}
}

func TestRenameAcrossPolicyDomains(t *testing.T) {
	fs := netsec.NewFS()
	shared := testPolicy(6)
	different := testPolicy(7)

	must(t, fs.Mkdir("/x"))
	must(t, fs.SetPolicy("/x", shared))
	must(t, fs.Mkdir("/y"))
	must(t, fs.SetPolicy("/y", shared))
	must(t, fs.Create("/x/file"))
	must(t, fs.Rename("/x/file", "/y/file"))
	assertMoved(t, fs, "/y/file", shared, 1)
	if _, err := fs.Stat("/x/file"); !errors.Is(err, netsec.ErrNotFound) {
		t.Fatalf("source still exists after rename: %v", err)
	}

	// A move within one policy domain carries the complete directory subtree.
	must(t, fs.Mkdir("/x/tree"))
	must(t, fs.Create("/x/tree/leaf"))
	must(t, fs.Rename("/x/tree", "/y/tree"))
	assertMoved(t, fs, "/y/tree/leaf", shared, 1)

	must(t, fs.Mkdir("/other"))
	must(t, fs.SetPolicy("/other", different))
	must(t, fs.Create("/other/file"))
	if err := fs.Rename("/other/file", "/y/other-file"); !errors.Is(err, netsec.ErrPolicyMismatch) {
		t.Fatalf("rename across different keys error = %v, want ErrPolicyMismatch", err)
	}
	if _, err := fs.Stat("/other/file"); err != nil {
		t.Fatalf("failed cross-policy rename removed source: %v", err)
	}
	if _, err := fs.Stat("/y/other-file"); !errors.Is(err, netsec.ErrNotFound) {
		t.Fatalf("failed cross-policy rename created destination: %v", err)
	}

	must(t, fs.Mkdir("/plain"))
	must(t, fs.Create("/plain/plaintext"))
	err := fs.Rename("/plain/plaintext", "/y/plaintext")
	if !errors.Is(err, netsec.ErrPolicyMismatch) || !errors.Is(err, netsec.ErrPlaintextIntoEncrypted) {
		t.Fatalf("plaintext -> encrypted error = %v, want policy mismatch and plaintext placement errors", err)
	}
	if _, err := fs.Stat("/plain/plaintext"); err != nil {
		t.Fatalf("blocked plaintext move removed source: %v", err)
	}

	err = fs.Rename("/y/file", "/plain/encrypted")
	if !errors.Is(err, netsec.ErrPolicyMismatch) || !errors.Is(err, netsec.ErrEncryptedIntoPlaintext) {
		t.Fatalf("encrypted -> plaintext error = %v, want policy mismatch and encrypted placement errors", err)
	}
	if _, err := fs.Stat("/y/file"); err != nil {
		t.Fatalf("blocked encrypted move removed source: %v", err)
	}
}

func TestHardLinkAcrossPolicyDomains(t *testing.T) {
	fs := netsec.NewFS()
	shared := testPolicy(8)
	different := testPolicy(9)

	must(t, fs.Mkdir("/x"))
	must(t, fs.SetPolicy("/x", shared))
	must(t, fs.Mkdir("/y"))
	must(t, fs.SetPolicy("/y", shared))
	must(t, fs.Create("/x/file"))
	must(t, fs.Link("/x/file", "/y/linked"))

	source := stat(t, fs, "/x/file")
	target := stat(t, fs, "/y/linked")
	if source.Inode != target.Inode {
		t.Fatalf("hard link inode = %d, want %d", target.Inode, source.Inode)
	}
	if source.Links != 2 || target.Links != 2 {
		t.Fatalf("hard link counts = %d and %d, want 2", source.Links, target.Links)
	}
	assertMoved(t, fs, "/y/linked", shared, 2)

	must(t, fs.Mkdir("/other"))
	must(t, fs.SetPolicy("/other", different))
	if err := fs.Link("/x/file", "/other/linked"); !errors.Is(err, netsec.ErrPolicyMismatch) {
		t.Fatalf("link across different keys error = %v, want ErrPolicyMismatch", err)
	}
	if _, err := fs.Stat("/other/linked"); !errors.Is(err, netsec.ErrNotFound) {
		t.Fatalf("failed cross-policy link created destination: %v", err)
	}

	must(t, fs.Mkdir("/plain"))
	must(t, fs.Create("/plain/plaintext"))
	err := fs.Link("/plain/plaintext", "/y/plaintext")
	if !errors.Is(err, netsec.ErrPolicyMismatch) || !errors.Is(err, netsec.ErrPlaintextIntoEncrypted) {
		t.Fatalf("plaintext link into encrypted directory error = %v", err)
	}
	err = fs.Link("/x/file", "/plain/encrypted")
	if !errors.Is(err, netsec.ErrPolicyMismatch) || !errors.Is(err, netsec.ErrEncryptedIntoPlaintext) {
		t.Fatalf("encrypted link into plaintext directory error = %v", err)
	}
	if err := fs.Link("/x", "/y/dirlink"); !errors.Is(err, netsec.ErrHardLinkToDirectory) {
		t.Fatalf("directory hard link error = %v, want ErrHardLinkToDirectory", err)
	}
}

func TestRenameExchange(t *testing.T) {
	fs := netsec.NewFS()
	shared := testPolicy(10)
	different := testPolicy(11)

	for _, dir := range []string{"/x", "/y"} {
		must(t, fs.Mkdir(dir))
		must(t, fs.SetPolicy(dir, shared))
	}
	must(t, fs.Create("/x/first"))
	must(t, fs.Create("/y/second"))
	firstBefore := stat(t, fs, "/x/first")
	secondBefore := stat(t, fs, "/y/second")

	must(t, fs.Exchange("/x/first", "/y/second"))
	firstAfter := stat(t, fs, "/y/second")
	secondAfter := stat(t, fs, "/x/first")
	if firstAfter.Inode != firstBefore.Inode || secondAfter.Inode != secondBefore.Inode {
		t.Fatalf("exchange did not swap exact inodes: %d/%d -> %d/%d",
			firstBefore.Inode, secondBefore.Inode, firstAfter.Inode, secondAfter.Inode)
	}
	assertMoved(t, fs, "/x/first", shared, 1)
	assertMoved(t, fs, "/y/second", shared, 1)

	// Exchange within one directory is supported too.
	must(t, fs.Create("/x/alpha"))
	must(t, fs.Create("/x/beta"))
	alpha := stat(t, fs, "/x/alpha")
	beta := stat(t, fs, "/x/beta")
	must(t, fs.RenameExchange("/x/alpha", "/x/beta"))
	if stat(t, fs, "/x/beta").Inode != alpha.Inode || stat(t, fs, "/x/alpha").Inode != beta.Inode {
		t.Fatal("same-directory exchange did not swap entries")
	}

	// A failed cross-policy exchange must leave both entries untouched.
	must(t, fs.Mkdir("/other"))
	must(t, fs.SetPolicy("/other", different))
	must(t, fs.Create("/other/third"))
	thirdBefore := stat(t, fs, "/other/third")
	firstBefore = stat(t, fs, "/x/first")
	err := fs.Exchange("/x/first", "/other/third")
	if !errors.Is(err, netsec.ErrPolicyMismatch) {
		t.Fatalf("cross-policy exchange error = %v, want ErrPolicyMismatch", err)
	}
	if stat(t, fs, "/x/first").Inode != firstBefore.Inode ||
		stat(t, fs, "/other/third").Inode != thirdBefore.Inode {
		t.Fatal("failed cross-policy exchange modified the namespace")
	}

	// Plaintext and encrypted entries cannot be exchanged through either
	// direction; both placement checks must fail atomically.
	must(t, fs.Mkdir("/plain"))
	must(t, fs.Create("/plain/plaintext"))
	encryptedBefore := stat(t, fs, "/x/first")
	plaintextBefore := stat(t, fs, "/plain/plaintext")
	err = fs.Exchange("/x/first", "/plain/plaintext")
	if !errors.Is(err, netsec.ErrPolicyMismatch) ||
		!errors.Is(err, netsec.ErrEncryptedIntoPlaintext) ||
		!errors.Is(err, netsec.ErrPlaintextIntoEncrypted) {
		t.Fatalf("plaintext/encrypted exchange error = %v, want both placement errors", err)
	}
	if stat(t, fs, "/x/first").Inode != encryptedBefore.Inode ||
		stat(t, fs, "/plain/plaintext").Inode != plaintextBefore.Inode {
		t.Fatal("failed plaintext/encrypted exchange modified the namespace")
	}

	// Exchanging a directory with its descendant would create a cycle.
	must(t, fs.Mkdir("/x/parent"))
	must(t, fs.Create("/x/parent/child"))
	if err := fs.Exchange("/x/parent", "/x/parent/child"); !errors.Is(err, netsec.ErrInvalidOperation) {
		t.Fatalf("cycle exchange error = %v, want ErrInvalidOperation", err)
	}
	if _, err := fs.Stat("/x/parent/child"); err != nil {
		t.Fatalf("failed cycle exchange damaged subtree: %v", err)
	}
}

func TestPolicyEqualityIsFieldExact(t *testing.T) {
	base := testPolicy(12)
	tests := []struct {
		name   string
		mutate func(*netsec.Policy)
	}{
		{"version", func(p *netsec.Policy) { p.Version++ }},
		{"master key", func(p *netsec.Policy) { p.MasterKeyID[5] ^= 0x80 }},
		{"contents mode", func(p *netsec.Policy) { p.ContentsMode++ }},
		{"filenames mode", func(p *netsec.Policy) { p.FilenamesMode++ }},
		{"flags", func(p *netsec.Policy) { p.Flags |= 0x40000000 }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := netsec.NewFS()
			other := base
			tc.mutate(&other)

			must(t, fs.Mkdir("/source"))
			must(t, fs.SetPolicy("/source", base))
			must(t, fs.Mkdir("/destination"))
			must(t, fs.SetPolicy("/destination", other))
			must(t, fs.Create("/source/file"))
			must(t, fs.Create("/destination/existing"))

			if err := fs.Rename("/source/file", "/destination/file"); !errors.Is(err, netsec.ErrPolicyMismatch) {
				t.Fatalf("rename error = %v, want ErrPolicyMismatch", err)
			}
			if err := fs.Link("/source/file", "/destination/link"); !errors.Is(err, netsec.ErrPolicyMismatch) {
				t.Fatalf("link error = %v, want ErrPolicyMismatch", err)
			}
			if err := fs.Exchange("/source/file", "/destination/existing"); !errors.Is(err, netsec.ErrPolicyMismatch) {
				t.Fatalf("exchange error = %v, want ErrPolicyMismatch", err)
			}
		})
	}
}

func TestRenameReplacementKeepsLinkCountsConsistent(t *testing.T) {
	fs := netsec.NewFS()
	policy := testPolicy(13)
	must(t, fs.Mkdir("/dir"))
	must(t, fs.SetPolicy("/dir", policy))
	must(t, fs.Create("/dir/new"))
	must(t, fs.Create("/dir/old"))
	must(t, fs.Link("/dir/old", "/dir/old-alias"))

	must(t, fs.Rename("/dir/new", "/dir/old"))
	if _, err := fs.Stat("/dir/new"); !errors.Is(err, netsec.ErrNotFound) {
		t.Fatalf("rename source still exists: %v", err)
	}
	if _, err := fs.Stat("/dir/old-alias"); err != nil {
		t.Fatalf("replacing one hard link removed its alias: %v", err)
	}
	if got := stat(t, fs, "/dir/old-alias").Links; got != 1 {
		t.Fatalf("alias link count after replacement = %d, want 1", got)
	}

	// A directory can replace only an empty directory.
	must(t, fs.Mkdir("/dir/source"))
	must(t, fs.Mkdir("/dir/full-target"))
	must(t, fs.Create("/dir/full-target/child"))
	if err := fs.Rename("/dir/source", "/dir/full-target"); !errors.Is(err, netsec.ErrNotEmpty) {
		t.Fatalf("directory over non-empty directory error = %v, want ErrNotEmpty", err)
	}
	if _, err := fs.Stat("/dir/source"); err != nil {
		t.Fatalf("failed directory replacement removed source: %v", err)
	}
	must(t, fs.Mkdir("/dir/empty-target"))
	must(t, fs.Rename("/dir/source", "/dir/empty-target"))
	if node := stat(t, fs, "/dir/empty-target"); !node.IsDir() {
		t.Fatalf("replacement result is not a directory: %#v", node)
	}
}

func TestUnsafePathsAreRejected(t *testing.T) {
	fs := netsec.NewFS()
	must(t, fs.Mkdir("/safe"))
	for _, name := range []string{"/safe/../escape", "/safe/\x00bad"} {
		if err := fs.Create(name); !errors.Is(err, netsec.ErrInvalidPath) {
			t.Fatalf("Create(%q) error = %v, want ErrInvalidPath", name, err)
		}
	}
}

func testPolicy(seed byte) netsec.Policy {
	var key [16]byte
	for i := range key {
		key[i] = seed + byte(i)
	}
	return netsec.Policy{
		Version:       netsec.PolicyVersion2,
		MasterKeyID:   key,
		ContentsMode:  netsec.ModeAES256XTS,
		FilenamesMode: netsec.ModeAES256HSH,
		Flags:         netsec.PolicyFlagDirectKey | netsec.PolicyFlagIVInoLblk64,
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func stat(t *testing.T, fs *netsec.FileSystem, name string) *netsec.Node {
	t.Helper()
	node, err := fs.Stat(name)
	if err != nil {
		t.Fatalf("Stat(%q): %v", name, err)
	}
	return node
}

func policyAt(t *testing.T, fs *netsec.FileSystem, name string) *netsec.Policy {
	t.Helper()
	policy, err := fs.PolicyAt(name)
	if err != nil {
		t.Fatalf("PolicyAt(%q): %v", name, err)
	}
	return policy
}

func assertMoved(t *testing.T, fs *netsec.FileSystem, name string, policy netsec.Policy, wantLinks uint64) {
	t.Helper()
	node := stat(t, fs, name)
	if node.Policy == nil || !node.Policy.Equal(policy) {
		t.Fatalf("policy at %s = %#v, want %#v", name, node.Policy, policy)
	}
	if node.Links != wantLinks {
		t.Fatalf("link count at %s = %d, want %d", name, node.Links, wantLinks)
	}
}
