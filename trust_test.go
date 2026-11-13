package netsec

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	testSelfUID  = 1000
	testOtherUID = 2000
)

type fakeOwners map[string]int

func (f fakeOwners) owner(path string) (int, error) {
	if uid, ok := f[path]; ok {
		return uid, nil
	}
	return os.Getuid(), nil
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func newTestEvaluator(t *testing.T, owners fakeOwners, systemConfig, globalConfig string) *Evaluator {
	t.Helper()
	return &Evaluator{
		owner:        owners.owner,
		systemConfig: systemConfig,
		globalConfig: globalConfig,
	}
}

func makeRepo(t *testing.T, root string, extraFiles map[string]string) string {
	t.Helper()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "hooks"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	writeFile(t, filepath.Join(gitDir, "config"), "[core]\n\trepositoryformatversion = 0\n")
	for rel, content := range extraFiles {
		writeFile(t, filepath.Join(root, rel), content)
	}
	return root
}

func TestForeignOwnerRejected(t *testing.T) {
	root := t.TempDir()
	repo := makeRepo(t, filepath.Join(root, "shared-repo"), nil)

	ev := newTestEvaluator(t, fakeOwners{repo: testOtherUID}, "", "")
	decision, err := ev.IsTrusted(repo)
	if err != nil {
		t.Fatalf("IsTrusted: %v", err)
	}
	if decision.Trusted {
		t.Fatalf("foreign-owned repository trusted: %+v", decision)
	}
	if decision.Reason != ReasonOtherOwner {
		t.Fatalf("reason = %q, want %q", decision.Reason, ReasonOtherOwner)
	}
}

func TestLocalSelfAuthorizationIgnored(t *testing.T) {
	root := t.TempDir()
	repo := makeRepo(t, filepath.Join(root, "shared-repo"), map[string]string{
		filepath.Join(".git", "config"): "[safe]\n\tdirectory = " + filepath.Join(root, "shared-repo") + "\n",
	})

	ev := newTestEvaluator(t, fakeOwners{repo: testOtherUID}, "", "")
	decision, err := ev.IsTrusted(repo)
	if err != nil {
		t.Fatalf("IsTrusted: %v", err)
	}
	if decision.Trusted || decision.Reason != ReasonOtherOwner {
		t.Fatalf("local self-authorization honored: %+v", decision)
	}
}

func TestGlobalSafeDirectoryExactMatch(t *testing.T) {
	root := t.TempDir()
	repo := makeRepo(t, filepath.Join(root, "shared-repo"), nil)
	global := filepath.Join(root, "global.gitconfig")
	writeFile(t, global, "[safe]\n\tdirectory = \""+repo+"\"\n")

	ev := newTestEvaluator(t, fakeOwners{repo: testOtherUID}, "", global)
	decision, err := ev.IsTrusted(repo)
	if err != nil {
		t.Fatalf("IsTrusted: %v", err)
	}
	if !decision.Trusted || decision.Reason != ReasonSafeDirectory {
		t.Fatalf("exact safe.directory not honored: %+v", decision)
	}
	if decision.SafeEntry != repo || decision.RepoDir != repo {
		t.Fatalf("decision metadata wrong: %+v", decision)
	}
}

func TestSafeDirectoryWildcard(t *testing.T) {
	root := t.TempDir()
	repo := makeRepo(t, filepath.Join(root, "any-repo"), nil)
	system := filepath.Join(root, "system.gitconfig")
	writeFile(t, system, "[safe]\n\tdirectory = *\n")

	ev := newTestEvaluator(t, fakeOwners{repo: testOtherUID}, system, "")
	decision, err := ev.IsTrusted(repo)
	if err != nil {
		t.Fatalf("IsTrusted: %v", err)
	}
	if !decision.Trusted || decision.Reason != ReasonSafeDirectory || decision.SafeEntry != "*" {
		t.Fatalf("wildcard safe.directory not honored: %+v", decision)
	}
}

func TestSafeDirectoryPrefixMatch(t *testing.T) {
	root := t.TempDir()
	system := filepath.Join(root, "system.gitconfig")
	prefix := filepath.Join(root, "shared")
	writeFile(t, system, "[safe]\n\tdirectory = "+prefix+"/*\n")

	repoA := makeRepo(t, filepath.Join(prefix, "team-a", "repo"), nil)
	repoB := makeRepo(t, filepath.Join(root, "elsewhere", "repo"), nil)

	owners := fakeOwners{
		repoA:                            testOtherUID,
		filepath.Join(prefix, "team-a"):  testOtherUID,
		prefix:                           testOtherUID,
		filepath.Join(root, "elsewhere"): testOtherUID,
		repoB:                            testOtherUID,
	}

	ev := newTestEvaluator(t, owners, system, "")

	decision, err := ev.IsTrusted(repoA)
	if err != nil {
		t.Fatalf("IsTrusted repoA: %v", err)
	}
	if !decision.Trusted || decision.Reason != ReasonSafeDirectory {
		t.Fatalf("prefix match failed for %s: %+v", repoA, decision)
	}

	decision, err = ev.IsTrusted(repoB)
	if err != nil {
		t.Fatalf("IsTrusted repoB: %v", err)
	}
	if decision.Trusted || decision.Reason != ReasonOtherOwner {
		t.Fatalf("prefix incorrectly matched sibling %s: %+v", repoB, decision)
	}
}

func TestSudoUIDComparison(t *testing.T) {
	t.Setenv("SUDO_UID", "1000")

	root := t.TempDir()
	repo := makeRepo(t, filepath.Join(root, "repo"), nil)

	ev := newTestEvaluator(t, fakeOwners{repo: testSelfUID}, "", "")
	decision, err := ev.IsTrusted(repo)
	if err != nil {
		t.Fatalf("IsTrusted: %v", err)
	}
	if !decision.Trusted || decision.Reason != ReasonOwner {
		t.Fatalf("SUDO_UID ownership not honored: %+v", decision)
	}

	ev = newTestEvaluator(t, fakeOwners{repo: testOtherUID}, "", "")
	decision, err = ev.IsTrusted(repo)
	if err != nil {
		t.Fatalf("IsTrusted foreign: %v", err)
	}
	if decision.Trusted || decision.Reason != ReasonOtherOwner {
		t.Fatalf("foreign owner trusted under sudo: %+v", decision)
	}
}

func TestEffectiveUID(t *testing.T) {
	t.Setenv("SUDO_UID", "1234")
	if got := effectiveUID(); got != 1234 {
		t.Fatalf("effectiveUID with SUDO_UID = %d, want 1234", got)
	}

	t.Setenv("SUDO_UID", "")
	if got := effectiveUID(); got != os.Getuid() {
		t.Fatalf("effectiveUID without SUDO_UID = %d, want %d", got, os.Getuid())
	}

	t.Setenv("SUDO_UID", "not-a-number")
	if got := effectiveUID(); got != os.Getuid() {
		t.Fatalf("effectiveUID with bad SUDO_UID = %d, want %d", got, os.Getuid())
	}
}

func TestWalkStopsAtForeignParent(t *testing.T) {
	root := t.TempDir()
	foreignParent := filepath.Join(root, "foreign")
	nested := makeRepo(t, filepath.Join(foreignParent, "inner", "repo"), nil)

	ev := newTestEvaluator(t, fakeOwners{
		foreignParent:                         testOtherUID,
		filepath.Join(foreignParent, "inner"): testOtherUID,
		nested:                                testOtherUID,
	}, "", "")

	decision, err := ev.IsTrusted(nested)
	if err != nil {
		t.Fatalf("IsTrusted: %v", err)
	}
	if decision.Trusted || decision.Reason != ReasonOtherOwner || decision.RepoDir != "" {
		t.Fatalf("walk did not stop at foreign parent: %+v", decision)
	}
}

func TestOwnedRepositoryTrusted(t *testing.T) {
	root := t.TempDir()
	repo := makeRepo(t, filepath.Join(root, "own-repo"), map[string]string{
		filepath.Join("cmd", "tool", "main.go"): "package main\n",
	})

	ev := newTestEvaluator(t, fakeOwners{}, "", "")

	for _, path := range []string{repo, filepath.Join(repo, "cmd", "tool")} {
		decision, err := ev.IsTrusted(path)
		if err != nil {
			t.Fatalf("IsTrusted(%s): %v", path, err)
		}
		if !decision.Trusted || decision.Reason != ReasonOwner || decision.RepoDir != repo {
			t.Fatalf("owned repo not trusted for %s: %+v", path, decision)
		}
	}
}

func TestNoRepository(t *testing.T) {
	ev := newTestEvaluator(t, fakeOwners{}, "", "")

	// A synthetic self-owned path with no .git anywhere on the walk, so the
	// real workspace .git above t.TempDir() cannot be reached.
	decision, err := ev.IsTrusted("/synthetic/no-repo-here")
	if err != nil {
		t.Fatalf("IsTrusted: %v", err)
	}
	if decision.Trusted || decision.Reason != ReasonNoRepository {
		t.Fatalf("path without repo: %+v", decision)
	}
}

func TestParseSafeDirectories(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "gitconfig")
	writeFile(t, config, `
# leading comment
[core]
	autocrlf = false
[safe]
	directory = /exact/path
	directory = "/quoted path"
	directory = /prefix/*
	other = ignored
[safe "subsection"]
	directory = /subsection/ignored
`)

	entries, err := parseSafeDirectories(config)
	if err != nil {
		t.Fatalf("parseSafeDirectories: %v", err)
	}
	want := []string{"/exact/path", "/quoted path", "/prefix/*"}
	if len(entries) != len(want) {
		t.Fatalf("entries = %v, want %v", entries, want)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Fatalf("entry[%d] = %q, want %q", i, entries[i], want[i])
		}
	}

	if _, err := parseSafeDirectories(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing config err = %v, want os.ErrNotExist", err)
	}
}

func TestSystemAndGlobalConfigLoaded(t *testing.T) {
	root := t.TempDir()
	system := filepath.Join(root, "system")
	global := filepath.Join(root, "global")
	writeFile(t, system, "[safe]\n\tdirectory = /from/system\n")
	writeFile(t, global, "[safe]\n\tdirectory = /from/global\n")

	ev := newTestEvaluator(t, fakeOwners{}, system, global)
	entries, err := ev.loadSafeDirectories()
	if err != nil {
		t.Fatalf("loadSafeDirectories: %v", err)
	}
	want := map[string]bool{"/from/system": true, "/from/global": true}
	if len(entries) != len(want) {
		t.Fatalf("entries = %v", entries)
	}
	for _, entry := range entries {
		if !want[entry] {
			t.Fatalf("unexpected entry %q in %v", entry, entries)
		}
	}
}

// A config path that cannot be read must surface an error so the caller fails
// closed rather than silently treating the repository as trusted.
func TestUnreadableConfigFailsClosed(t *testing.T) {
	root := t.TempDir()
	repo := makeRepo(t, filepath.Join(root, "repo"), nil)

	// Opening a directory succeeds but scanning it fails on most platforms.
	ev := newTestEvaluator(t, fakeOwners{repo: testOtherUID}, root, "")
	if _, err := ev.IsTrusted(repo); err == nil {
		t.Fatalf("expected error when config cannot be read, got trust decision")
	}
}

func TestMatchSafeDirectory(t *testing.T) {
	restricted := []string{"/srv/repo", "/srv/shared/*"}
	cases := []struct {
		path string
		want bool
	}{
		{"/srv/repo", true},
		{"/srv/shared", true},
		{"/srv/shared/team/repo", true},
		{"/srv/other", false},
		{"/srv/repository", false},
	}
	for _, tc := range cases {
		_, got := matchSafeDirectory(tc.path, restricted)
		if got != tc.want {
			t.Fatalf("matchSafeDirectory(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}

	if _, ok := matchSafeDirectory("/anything", []string{"*"}); !ok {
		t.Fatalf("literal * must match every path")
	}
}

func TestIsTrustedSmoke(t *testing.T) {
	root := t.TempDir()
	repo := makeRepo(t, filepath.Join(root, "repo"), nil)
	global := filepath.Join(root, "global")
	writeFile(t, global, "[safe]\n\tdirectory = "+repo+"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(root, "no-such-system-config"))

	decision, err := IsTrusted(repo)
	if err != nil {
		t.Fatalf("IsTrusted: %v", err)
	}
	if !decision.Trusted || decision.RepoDir != repo {
		t.Fatalf("smoke decision wrong: %+v", decision)
	}
}
