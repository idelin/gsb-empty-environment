package netsec

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const fullCommit = "e8f3d6a91b2c4f5061728394a5b6c7d8e9f0a1b2"

type podFixture struct {
	name    string
	version string
	repo    string
	source  map[string]any
}

func mustPodspecJSON(t *testing.T, name, version string, source map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"name":    name,
		"version": version,
		"source":  source,
	})
	if err != nil {
		t.Fatalf("marshal podspec: %v", err)
	}
	return data
}

func (p podFixture) data(t *testing.T) []byte {
	t.Helper()
	return mustPodspecJSON(t, p.name, p.version, p.source)
}

// buildLock renders a Podfile.lock containing the given pods and records the
// SHA-1 of the supplied podspec bytes as SPEC CHECKSUMS.
func buildLock(t *testing.T, pods []podFixture, bytesByName map[string][]byte) *Lockfile {
	t.Helper()
	var b strings.Builder
	b.WriteString("PODS:\n")
	for _, p := range pods {
		b.WriteString("  - " + p.name + " (" + p.version + ")\n")
	}
	b.WriteString("\nDEPENDENCIES:\n")
	for _, p := range pods {
		b.WriteString("  - " + p.name + "\n")
	}
	b.WriteString("\nSPEC REPOS:\n")
	repos := map[string][]podFixture{}
	var repoOrder []string
	for _, p := range pods {
		if _, seen := repos[p.repo]; !seen {
			repoOrder = append(repoOrder, p.repo)
		}
		repos[p.repo] = append(repos[p.repo], p)
	}
	for _, repo := range repoOrder {
		b.WriteString("  " + repo + ":\n")
		for _, p := range repos[repo] {
			b.WriteString("    - " + p.name + " (" + p.version + ")\n")
		}
	}
	b.WriteString("\nSPEC CHECKSUMS:\n")
	for name, data := range bytesByName {
		sum := sha1.Sum(data)
		b.WriteString("  " + name + ": " + hex.EncodeToString(sum[:]) + "\n")
	}
	b.WriteString("\nPODFILE CHECKSUM: 0123456789abcdef0123456789abcdef01234567\n\nCOCOAPODS: 1.15.2\n")

	lock, err := ParseLockfile(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("parse generated lockfile: %v\n%s", err, b.String())
	}
	return lock
}

func gitSource(url, commit string) map[string]any {
	source := map[string]any{"git": url}
	if commit != "" {
		source["commit"] = commit
	}
	return source
}

func TestSpecPathSharding(t *testing.T) {
	// da208d9cbd49253cc75271c6c269ebce is the MD5 of "Alamofire".
	got := SpecPath("Alamofire", "5.6.4")
	want := "Specs/d/a/2/Alamofire/5.6.4/Alamofire.podspec.json"
	if got != want {
		t.Fatalf("SpecPath = %q, want %q", got, want)
	}

	name := "PrivateKit"
	digest := md5.Sum([]byte(name))
	hexDigest := hex.EncodeToString(digest[:])
	got = SpecPath(name, "1.0.0")
	want = "Specs/" + hexDigest[0:1] + "/" + hexDigest[1:2] + "/" + hexDigest[2:3] +
		"/PrivateKit/1.0.0/PrivateKit.podspec.json"
	if got != want {
		t.Fatalf("SpecPath = %q, want %q", got, want)
	}
}

func TestVerifyAcceptsPinnedGitPodspec(t *testing.T) {
	pod := podFixture{
		name:    "Alamofire",
		version: "5.6.4",
		repo:    "trunk",
		source:  gitSource("https://github.com/Alamofire/Alamofire.git", fullCommit),
	}
	data := pod.data(t)
	lock := buildLock(t, []podFixture{pod}, map[string][]byte{pod.name: data})

	verifier := NewVerifier(lock)
	relPath := SpecPath(pod.name, pod.version)
	if _, err := verifier.Verify("trunk", relPath, data); err != nil {
		t.Fatalf("expected valid podspec to pass, got %v", err)
	}

	// Leading "./" and a leading slash must not break path normalization.
	if _, err := verifier.Verify("trunk", "./"+relPath, data); err != nil {
		t.Fatalf("normalized path rejected: %v", err)
	}
	if _, err := verifier.Verify("trunk", "/"+relPath, data); err != nil {
		t.Fatalf("absolute-looking path rejected: %v", err)
	}
}

func TestVerifyTamperedSourceFailsChecksum(t *testing.T) {
	pod := podFixture{
		name:    "Alamofire",
		version: "5.6.4",
		repo:    "trunk",
		source:  gitSource("https://github.com/Alamofire/Alamofire.git", fullCommit),
	}
	original := pod.data(t)
	lock := buildLock(t, []podFixture{pod}, map[string][]byte{pod.name: original})

	// Attacker swaps the source URL for a malicious repository.
	tampered := mustPodspecJSON(t, pod.name, pod.version,
		gitSource("https://evil.example.com/attacker/Alamofire.git", fullCommit))

	_, err := NewVerifier(lock).Verify("trunk", SpecPath(pod.name, pod.version), tampered)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("tampered source: want ErrChecksumMismatch, got %v", err)
	}
}

func TestVerifySameNamePodFromWrongRepoRejected(t *testing.T) {
	// PrivateKit exists in both the public trunk and a private spec repo.
	// The lock records the private repo; Podfile source order is irrelevant.
	private := podFixture{
		name:    "PrivateKit",
		version: "1.0.0",
		repo:    "git@github.com:acme/private-specs.git",
		source:  gitSource("https://github.com/acme/PrivateKit.git", fullCommit),
	}
	data := private.data(t)
	lock := buildLock(t, []podFixture{private}, map[string][]byte{private.name: data})
	verifier := NewVerifier(lock)
	relPath := SpecPath(private.name, private.version)

	// Same bytes, but served from the public source hosting the same name.
	if _, err := verifier.Verify("trunk", relPath, data); !errors.Is(err, ErrSpecRepoMismatch) {
		t.Fatalf("cross-source same-name pod: want ErrSpecRepoMismatch, got %v", err)
	}

	// The repo named in the lock is accepted.
	if _, err := verifier.Verify(private.repo, relPath, data); err != nil {
		t.Fatalf("lock-recorded repo rejected: %v", err)
	}

	// A trailing slash on the repo key must not cause a false mismatch.
	if _, err := verifier.Verify(private.repo+"/", relPath, data); err != nil {
		t.Fatalf("repo key with trailing slash rejected: %v", err)
	}
}

func TestVerifyRejectsWrongShardedPath(t *testing.T) {
	pod := podFixture{
		name:    "Alamofire",
		version: "5.6.4",
		repo:    "trunk",
		source:  gitSource("https://github.com/Alamofire/Alamofire.git", fullCommit),
	}
	data := pod.data(t)
	lock := buildLock(t, []podFixture{pod}, map[string][]byte{pod.name: data})
	verifier := NewVerifier(lock)

	cases := map[string]string{
		"wrong shard":     "Specs/0/0/0/Alamofire/5.6.4/Alamofire.podspec.json",
		"wrong version":   "Specs/d/a/2/Alamofire/9.9.9/Alamofire.podspec.json",
		"wrong file name": "Specs/d/a/2/Alamofire/5.6.4/Other.podspec.json",
	}
	for name, wrongPath := range cases {
		if _, err := verifier.Verify("trunk", wrongPath, data); !errors.Is(err, ErrSpecPathMismatch) {
			t.Fatalf("%s: want ErrSpecPathMismatch, got %v", name, err)
		}
	}
}

func TestVerifySourcePolicy(t *testing.T) {
	base := podFixture{name: "Lib", version: "1.0", repo: "trunk"}

	cases := []struct {
		name    string
		source  map[string]any
		wantErr error
	}{
		{
			name:    "https archive rejected",
			source:  map[string]any{"http": "https://cdn.example.com/lib-1.0.zip"},
			wantErr: ErrHTTPSource,
		},
		{
			name:    "plain http archive rejected",
			source:  map[string]any{"http": "http://cdn.example.com/lib-1.0.zip"},
			wantErr: ErrHTTPSource,
		},
		{
			name:    "git without commit rejected",
			source:  gitSource("https://github.com/acme/Lib.git", ""),
			wantErr: ErrGitCommitNotPinned,
		},
		{
			name:    "git tag instead of commit rejected",
			source:  map[string]any{"git": "https://github.com/acme/Lib.git", "tag": "v1.0"},
			wantErr: ErrGitCommitNotPinned,
		},
		{
			name:    "abbreviated commit rejected",
			source:  gitSource("https://github.com/acme/Lib.git", "e8f3d6a"),
			wantErr: ErrGitCommitNotPinned,
		},
		{
			name:    "non hex commit rejected",
			source:  gitSource("https://github.com/acme/Lib.git", strings.Repeat("z", 40)),
			wantErr: ErrGitCommitNotPinned,
		},
		{
			name:    "git over plain http rejected",
			source:  gitSource("http://github.com/acme/Lib.git", fullCommit),
			wantErr: ErrUnsupportedSource,
		},
		{
			name:    "full 40 char commit accepted",
			source:  gitSource("https://github.com/acme/Lib.git", fullCommit),
			wantErr: nil,
		},
		{
			name:    "uppercase commit accepted",
			source:  gitSource("ssh://git@github.com/acme/Lib.git", strings.ToUpper(fullCommit)),
			wantErr: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pod := base
			pod.source = tc.source
			data := pod.data(t)
			// Checksum and repo entries are valid so only source policy can fail.
			lock := buildLock(t, []podFixture{pod}, map[string][]byte{pod.name: data})

			_, err := NewVerifier(lock).Verify("trunk", SpecPath(pod.name, pod.version), data)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("expected acceptance, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestVerifyMissingChecksum(t *testing.T) {
	pod := podFixture{
		name:    "Alamofire",
		version: "5.6.4",
		repo:    "trunk",
		source:  gitSource("https://github.com/Alamofire/Alamofire.git", fullCommit),
	}
	data := pod.data(t)

	// Lock contains a different pod, so Alamofire has no checksum entry.
	other := podFixture{
		name:    "Other",
		version: "2.0",
		repo:    "trunk",
		source:  gitSource("https://github.com/acme/Other.git", fullCommit),
	}
	otherData := other.data(t)
	lock := buildLock(t, []podFixture{other}, map[string][]byte{other.name: otherData})

	_, err := NewVerifier(lock).Verify("trunk", SpecPath(pod.name, pod.version), data)
	if !errors.Is(err, ErrChecksumMissing) {
		t.Fatalf("missing checksum: want ErrChecksumMissing, got %v", err)
	}
}

func TestVerifyPodAbsentFromSpecRepos(t *testing.T) {
	pod := podFixture{
		name:    "Orphan",
		version: "1.0",
		repo:    "trunk",
		source:  gitSource("https://github.com/acme/Orphan.git", fullCommit),
	}
	data := pod.data(t)
	sum := sha1.Sum(data)

	// Checksum is recorded, but SPEC REPOS has no entry for the pod at all.
	raw := "SPEC CHECKSUMS:\n  Orphan: " + hex.EncodeToString(sum[:]) + "\n"
	lock, err := ParseLockfile(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse lock: %v", err)
	}

	_, err = NewVerifier(lock).Verify("trunk", SpecPath(pod.name, pod.version), data)
	if !errors.Is(err, ErrSpecRepoMismatch) {
		t.Fatalf("pod missing from SPEC REPOS: want ErrSpecRepoMismatch, got %v", err)
	}
}
