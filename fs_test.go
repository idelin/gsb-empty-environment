package netsec_test

import (
	"errors"
	"testing"

	"example.org/netsec/q0311"
)

func testPolicy(key string) netsec.Policy {
	return netsec.Policy{
		Version:             netsec.PolicyVersionV2,
		MasterKeyIdentifier: key,
		ContentsMode:        netsec.ContentsModeAES256XTS,
		FilenamesMode:       netsec.FilenamesModeAES256CTS,
		Flags:               netsec.FlagDirectKey,
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}

func stat(t *testing.T, fs *netsec.FS, p string) netsec.Info {
	t.Helper()
	info, err := fs.Stat(p)
	if err != nil {
		t.Fatalf("Stat(%q): %v", p, err)
	}
	return info
}

func notExist(t *testing.T, fs *netsec.FS, p string) {
	t.Helper()
	_, err := fs.Stat(p)
	wantErr(t, err, netsec.ErrNotExist)
}

func encryptedDir(t *testing.T, fs *netsec.FS, p string, policy netsec.Policy) {
	t.Helper()
	must(t, fs.Mkdir(p))
	must(t, fs.SetPolicy(p, policy))
}

func TestPolicyInheritance(t *testing.T) {
	fs := netsec.NewFS()
	policy := testPolicy("master-a")

	encryptedDir(t, fs, "/enc", policy)
	must(t, fs.Mkdir("/enc/sub"))
	must(t, fs.CreateFile("/enc/file"))
	must(t, fs.CreateFile("/enc/sub/deep"))

	must(t, fs.Mkdir("/plain"))
	must(t, fs.CreateFile("/plain/file"))

	for _, p := range []string{"/enc", "/enc/sub", "/enc/file", "/enc/sub/deep"} {
		info := stat(t, fs, p)
		if !info.Encrypted {
			t.Fatalf("%s should be encrypted", p)
		}
		if !info.Policy.Equal(policy) {
			t.Fatalf("%s inherited policy %+v, want %+v", p, info.Policy, policy)
		}
	}

	plain := stat(t, fs, "/plain/file")
	if plain.Encrypted {
		t.Fatal("file under a plaintext directory must not be encrypted")
	}

	if stat(t, fs, "/enc/file").ID == stat(t, fs, "/enc/sub/deep").ID {
		t.Fatal("distinct files must have distinct node IDs")
	}
}

func TestSetPolicyRules(t *testing.T) {
	fs := netsec.NewFS()
	policy := testPolicy("master-a")
	other := testPolicy("master-b")

	must(t, fs.Mkdir("/nonempty"))
	must(t, fs.CreateFile("/nonempty/file"))
	wantErr(t, fs.SetPolicy("/nonempty", policy), netsec.ErrNotEmpty)
	if stat(t, fs, "/nonempty").Encrypted {
		t.Fatal("failed SetPolicy must not attach a policy")
	}

	must(t, fs.Mkdir("/already"))
	must(t, fs.SetPolicy("/already", policy))
	wantErr(t, fs.SetPolicy("/already", other), netsec.ErrPolicyAlreadySet)
	if got := stat(t, fs, "/already").Policy; !got.Equal(policy) {
		t.Fatalf("policy changed after failed update: %+v", got)
	}

	must(t, fs.Mkdir("/inherit"))
	must(t, fs.SetPolicy("/inherit", policy))
	must(t, fs.Mkdir("/inherit/child"))
	wantErr(t, fs.SetPolicy("/inherit/child", other), netsec.ErrPolicyAlreadySet)

	must(t, fs.CreateFile("/file"))
	wantErr(t, fs.SetPolicy("/file", policy), netsec.ErrNotDirectory)

	must(t, fs.Mkdir("/cleared"))
	must(t, fs.CreateFile("/cleared/file"))
	must(t, fs.Remove("/cleared/file"))
	must(t, fs.SetPolicy("/cleared", policy))
	if !stat(t, fs, "/cleared").Policy.Equal(policy) {
		t.Fatal("an emptied directory should accept its first policy")
	}

	must(t, fs.Mkdir("/invalid"))
	invalid := policy
	invalid.MasterKeyIdentifier = " "
	wantErr(t, fs.SetPolicy("/invalid", invalid), netsec.ErrInvalidPolicy)
	if stat(t, fs, "/invalid").Encrypted {
		t.Fatal("invalid policy must not be attached")
	}

	must(t, fs.CreateFile("/plain"))
	wantErr(t, fs.SetPolicy("/", policy), netsec.ErrNotEmpty)
}

func TestRenameAcrossDirectories(t *testing.T) {
	fs := netsec.NewFS()
	policy := testPolicy("master-a")
	otherKey := testPolicy("master-b")
	otherFlags := policy
	otherFlags.Flags |= netsec.FlagPad4
	otherContents := policy
	otherContents.ContentsMode = netsec.ContentsModeAdiantum
	otherFilenames := policy
	otherFilenames.FilenamesMode = netsec.FilenamesModeAdiantum
	otherVersion := policy
	otherVersion.Version = netsec.PolicyVersionV1

	encryptedDir(t, fs, "/enc", policy)
	encryptedDir(t, fs, "/enc2", policy)
	encryptedDir(t, fs, "/other-key", otherKey)
	encryptedDir(t, fs, "/other-flags", otherFlags)
	encryptedDir(t, fs, "/other-contents", otherContents)
	encryptedDir(t, fs, "/other-filenames", otherFilenames)
	encryptedDir(t, fs, "/other-version", otherVersion)
	must(t, fs.Mkdir("/plain"))

	must(t, fs.CreateFile("/plain/plain"))
	wantErr(t, fs.Rename("/plain/plain", "/enc/plain"), netsec.ErrPlaintextIntoEncrypted)
	stat(t, fs, "/plain/plain")
	notExist(t, fs, "/enc/plain")

	must(t, fs.CreateFile("/enc/secret"))
	wantErr(t, fs.Rename("/enc/secret", "/plain/secret"), netsec.ErrEncryptedIntoPlaintext)
	stat(t, fs, "/enc/secret")
	notExist(t, fs, "/plain/secret")

	for _, tc := range []struct {
		name string
		dir  string
	}{
		{"key", "/other-key"},
		{"flags", "/other-flags"},
		{"contents", "/other-contents"},
		{"filenames", "/other-filenames"},
		{"version", "/other-version"},
	} {
		t.Run("exact policy/"+tc.name, func(t *testing.T) {
			src := tc.dir + "/file"
			dst := "/enc/" + tc.name
			must(t, fs.CreateFile(src))
			wantErr(t, fs.Rename(src, dst), netsec.ErrPolicyMismatch)
			stat(t, fs, src)
			notExist(t, fs, dst)
		})
	}

	must(t, fs.Rename("/enc/secret", "/enc2/secret"))
	notExist(t, fs, "/enc/secret")
	moved := stat(t, fs, "/enc2/secret")
	if !moved.Encrypted || !moved.Policy.Equal(policy) {
		t.Fatalf("moved file lost its policy: %+v", moved)
	}

	must(t, fs.Rename("/plain/plain", "/plain/renamed"))
	notExist(t, fs, "/plain/plain")
	stat(t, fs, "/plain/renamed")

	must(t, fs.Mkdir("/plain/sub"))
	wantErr(t, fs.Rename("/plain/sub", "/enc/sub"), netsec.ErrPlaintextIntoEncrypted)
	stat(t, fs, "/plain/sub")
	notExist(t, fs, "/enc/sub")

	must(t, fs.Mkdir("/enc/inherited"))
	wantErr(t, fs.Rename("/enc/inherited", "/plain/inherited"), netsec.ErrEncryptedIntoPlaintext)
	stat(t, fs, "/enc/inherited")
	must(t, fs.Rename("/enc/inherited", "/enc2/inherited"))
	stat(t, fs, "/enc2/inherited")
}

func TestRenameOverwriteRules(t *testing.T) {
	fs := netsec.NewFS()
	policy := testPolicy("master-a")
	encryptedDir(t, fs, "/enc", policy)
	encryptedDir(t, fs, "/enc2", policy)

	must(t, fs.CreateFile("/enc/source"))
	must(t, fs.CreateFile("/enc2/target"))
	sourceID := stat(t, fs, "/enc/source").ID
	must(t, fs.Rename("/enc/source", "/enc2/target"))
	notExist(t, fs, "/enc/source")
	if got := stat(t, fs, "/enc2/target").ID; got != sourceID {
		t.Fatal("rename should replace the destination inode")
	}

	must(t, fs.Mkdir("/enc/tree"))
	must(t, fs.CreateFile("/enc/tree/child"))
	must(t, fs.Mkdir("/enc2/empty"))
	must(t, fs.Rename("/enc/tree", "/enc2/empty"))
	notExist(t, fs, "/enc/tree")
	stat(t, fs, "/enc2/empty/child")

	must(t, fs.Mkdir("/enc2/nonempty"))
	must(t, fs.CreateFile("/enc2/nonempty/child"))
	must(t, fs.Mkdir("/enc/other"))
	wantErr(t, fs.Rename("/enc/other", "/enc2/nonempty"), netsec.ErrNotEmpty)

	must(t, fs.CreateFile("/enc/file"))
	must(t, fs.Mkdir("/enc2/dir"))
	wantErr(t, fs.Rename("/enc/file", "/enc2/dir"), netsec.ErrIsDirectory)
	stat(t, fs, "/enc/file")
	wantErr(t, fs.Rename("/enc2/dir", "/enc/file"), netsec.ErrNotDirectory)
	stat(t, fs, "/enc2/dir")
}

func TestHardLinks(t *testing.T) {
	fs := netsec.NewFS()
	policy := testPolicy("master-a")
	other := testPolicy("master-b")

	encryptedDir(t, fs, "/enc", policy)
	encryptedDir(t, fs, "/enc2", policy)
	encryptedDir(t, fs, "/other", other)
	must(t, fs.Mkdir("/plain"))

	must(t, fs.CreateFile("/enc/file"))
	sourceID := stat(t, fs, "/enc/file").ID
	must(t, fs.Link("/enc/file", "/enc2/link"))

	first := stat(t, fs, "/enc/file")
	second := stat(t, fs, "/enc2/link")
	if first.ID != sourceID || second.ID != sourceID {
		t.Fatal("hard links must share the same node ID")
	}
	if first.Links != 2 || second.Links != 2 {
		t.Fatalf("link count = %d/%d, want 2", first.Links, second.Links)
	}
	if !second.Encrypted || !second.Policy.Equal(policy) {
		t.Fatalf("hard link lost its policy: %+v", second)
	}

	wantErr(t, fs.Link("/enc/file", "/other/link"), netsec.ErrPolicyMismatch)
	notExist(t, fs, "/other/link")

	wantErr(t, fs.Link("/enc/file", "/plain/link"), netsec.ErrEncryptedIntoPlaintext)
	notExist(t, fs, "/plain/link")

	must(t, fs.CreateFile("/plain/plain"))
	wantErr(t, fs.Link("/plain/plain", "/enc/plain"), netsec.ErrPlaintextIntoEncrypted)
	notExist(t, fs, "/enc/plain")

	must(t, fs.Mkdir("/enc/dir"))
	wantErr(t, fs.Link("/enc/dir", "/enc2/dir"), netsec.ErrIsDirectory)

	wantErr(t, fs.Link("/enc/file", "/enc2/link"), netsec.ErrExist)

	must(t, fs.Remove("/enc2/link"))
	if got := stat(t, fs, "/enc/file").Links; got != 1 {
		t.Fatalf("link count after unlink = %d, want 1", got)
	}
}

func TestRenameExchange(t *testing.T) {
	t.Run("same policy", func(t *testing.T) {
		fs := netsec.NewFS()
		policy := testPolicy("master-a")
		encryptedDir(t, fs, "/enc", policy)
		encryptedDir(t, fs, "/enc2", policy)

		must(t, fs.CreateFile("/enc/a"))
		must(t, fs.CreateFile("/enc2/b"))
		aID := stat(t, fs, "/enc/a").ID
		bID := stat(t, fs, "/enc2/b").ID

		must(t, fs.RenameExchange("/enc/a", "/enc2/b"))
		if got := stat(t, fs, "/enc/a").ID; got != bID {
			t.Fatalf("/enc/a has ID %d, want %d", got, bID)
		}
		if got := stat(t, fs, "/enc2/b").ID; got != aID {
			t.Fatalf("/enc2/b has ID %d, want %d", got, aID)
		}
		if got := stat(t, fs, "/enc/a").Policy; !got.Equal(policy) {
			t.Fatalf("swapped entry has wrong policy: %+v", got)
		}
	})

	t.Run("different key leaves tree unchanged", func(t *testing.T) {
		fs := netsec.NewFS()
		policy := testPolicy("master-a")
		encryptedDir(t, fs, "/enc", policy)
		encryptedDir(t, fs, "/other", testPolicy("master-b"))
		must(t, fs.CreateFile("/enc/a"))
		must(t, fs.CreateFile("/other/b"))
		aID := stat(t, fs, "/enc/a").ID
		bID := stat(t, fs, "/other/b").ID

		wantErr(t, fs.RenameExchange("/enc/a", "/other/b"), netsec.ErrPolicyMismatch)
		if got := stat(t, fs, "/enc/a").ID; got != aID {
			t.Fatal("failed exchange changed /enc/a")
		}
		if got := stat(t, fs, "/other/b").ID; got != bID {
			t.Fatal("failed exchange changed /other/b")
		}
	})

	t.Run("plaintext and encrypted leaves tree unchanged", func(t *testing.T) {
		fs := netsec.NewFS()
		policy := testPolicy("master-a")
		encryptedDir(t, fs, "/enc", policy)
		must(t, fs.Mkdir("/plain"))
		must(t, fs.CreateFile("/plain/p"))
		must(t, fs.CreateFile("/enc/e"))
		pID := stat(t, fs, "/plain/p").ID
		eID := stat(t, fs, "/enc/e").ID

		wantErr(t, fs.Exchange("/plain/p", "/enc/e"), netsec.ErrPlaintextIntoEncrypted)
		if stat(t, fs, "/plain/p").ID != pID || stat(t, fs, "/enc/e").ID != eID {
			t.Fatal("failed exchange changed the tree")
		}

		wantErr(t, fs.Exchange("/enc/e", "/plain/p"), netsec.ErrEncryptedIntoPlaintext)
		if stat(t, fs, "/plain/p").ID != pID || stat(t, fs, "/enc/e").ID != eID {
			t.Fatal("reverse failed exchange changed the tree")
		}
	})

	t.Run("directory with descendant", func(t *testing.T) {
		fs := netsec.NewFS()
		policy := testPolicy("master-a")
		encryptedDir(t, fs, "/enc", policy)
		must(t, fs.Mkdir("/enc/tree"))
		must(t, fs.CreateFile("/enc/tree/child"))

		wantErr(t, fs.RenameExchange("/enc/tree", "/enc/tree/child"), netsec.ErrInvalidMove)
		stat(t, fs, "/enc/tree/child")
		wantErr(t, fs.RenameExchange("/enc/tree/child", "/enc/tree"), netsec.ErrInvalidMove)
		stat(t, fs, "/enc/tree/child")
	})

	t.Run("same parent", func(t *testing.T) {
		fs := netsec.NewFS()
		policy := testPolicy("master-a")
		encryptedDir(t, fs, "/enc", policy)
		must(t, fs.CreateFile("/enc/x"))
		must(t, fs.CreateFile("/enc/y"))
		xID := stat(t, fs, "/enc/x").ID
		yID := stat(t, fs, "/enc/y").ID

		must(t, fs.Exchange("/enc/x", "/enc/y"))
		if stat(t, fs, "/enc/x").ID != yID || stat(t, fs, "/enc/y").ID != xID {
			t.Fatal("same-directory exchange did not swap the entries")
		}
	})

	t.Run("hard links to one inode", func(t *testing.T) {
		fs := netsec.NewFS()
		policy := testPolicy("master-a")
		encryptedDir(t, fs, "/enc", policy)
		must(t, fs.CreateFile("/enc/a"))
		must(t, fs.Link("/enc/a", "/enc/b"))
		id := stat(t, fs, "/enc/a").ID

		must(t, fs.Exchange("/enc/a", "/enc/b"))
		if stat(t, fs, "/enc/a").ID != id || stat(t, fs, "/enc/b").ID != id {
			t.Fatal("exchanging two links to one inode must be a no-op")
		}
	})

	t.Run("same path", func(t *testing.T) {
		fs := netsec.NewFS()
		policy := testPolicy("master-a")
		encryptedDir(t, fs, "/enc", policy)
		must(t, fs.CreateFile("/enc/a"))
		id := stat(t, fs, "/enc/a").ID

		must(t, fs.Exchange("/enc/a", "/enc/a"))
		if stat(t, fs, "/enc/a").ID != id {
			t.Fatal("same-path exchange changed the entry")
		}
		wantErr(t, fs.Exchange("/enc/missing", "/enc/missing"), netsec.ErrNotExist)
	})
}
