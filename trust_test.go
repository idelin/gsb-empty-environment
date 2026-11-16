package netsec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

const (
	testCurrentUID = uint32(1000)
	testForeignUID = uint32(4242)
)

type ownerMap map[string]uint32

func (m ownerMap) owner(path string) (uint32, error) {
	canonical := filepath.Clean(path)
	if resolved, err := canonicalExisting(canonical); err == nil {
		canonical = resolved
	}
	if uid, ok := m[canonical]; ok {
		return uid, nil
	}
	return 0, fmt.Errorf("test owner map has no entry for %s", canonical)
}

func makeRepository(t *testing.T) (root, gitDir string) {
	t.Helper()
	root = t.TempDir()
	gitDir = filepath.Join(root, ".git")
	mustMkdir(t, filepath.Join(gitDir, "objects"))
	mustWrite(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/main\n")
	mustWrite(t, filepath.Join(gitDir, "config"), "[core]\n\trepositoryformatversion = 0\n")
	return root, gitDir
}

func setRepositoryOwner(owners ownerMap, root, gitDir string, uid uint32) {
	owners[filepath.Clean(root)] = uid
	owners[filepath.Clean(gitDir)] = uid
}

func newTestChecker(owners ownerMap) *Checker {
	return NewChecker(
		WithCurrentUID(testCurrentUID),
		WithOwner(owners.owner),
		WithEnvironment(func(string) (string, bool) { return "", false }),
		WithHomeDir(tTempHome()),
		WithSystemConfig(""),
		WithGlobalConfigs(),
	)
}

func tTempHome() string {
	return filepath.Join(os.TempDir(), "netsec-test-home")
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOtherOwnerLocalSafeDirectoryIsIgnored(t *testing.T) {
	root, gitDir := makeRepository(t)
	mustWrite(t, filepath.Join(gitDir, "config"), `[safe]
	directory = *
	directory = `+root+`
`)

	owners := ownerMap{}
	setRepositoryOwner(owners, root, gitDir, testForeignUID)
	checker := newTestChecker(owners)

	repo, err := checker.Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Trusted {
		t.Fatalf("repository trusted from its own local safe.directory: %+v", repo)
	}
	if repo.CurrentUID != testCurrentUID {
		t.Fatalf("CurrentUID = %d, want %d", repo.CurrentUID, testCurrentUID)
	}

	if _, err := checker.ReadConfig(root); !errors.Is(err, ErrUntrustedRepository) {
		t.Fatalf("ReadConfig error = %v, want ErrUntrustedRepository", err)
	}
}

func TestGlobalSafeDirectoryExactMatch(t *testing.T) {
	root, gitDir := makeRepository(t)
	config := "[core]\n\trepositoryformatversion = 0\n"
	mustWrite(t, filepath.Join(gitDir, "config"), config)

	global := filepath.Join(t.TempDir(), "gitconfig")
	mustWrite(t, global, "[safe]\n\tdirectory = "+root+"\n")

	owners := ownerMap{}
	setRepositoryOwner(owners, root, gitDir, testForeignUID)
	checker := NewChecker(
		WithCurrentUID(testCurrentUID),
		WithOwner(owners.owner),
		WithEnvironment(func(string) (string, bool) { return "", false }),
		WithHomeDir(tTempHome()),
		WithSystemConfig(""),
		WithGlobalConfigs(global),
	)

	repo, err := checker.Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.Trusted {
		t.Fatalf("repository with exact global safe.directory was rejected: %+v", repo)
	}
	if repo.Reason != TrustReasonSafeDirectory || repo.SafeDirectory != root {
		t.Fatalf("unexpected trust reason: %+v", repo)
	}

	got, err := checker.ReadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != config {
		t.Fatalf("ReadConfig = %q, want %q", got, config)
	}
}

func TestSystemSafeDirectoryPrefixMatch(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	mustMkdir(t, filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, ".git", "config"), "[core]\n")

	system := filepath.Join(t.TempDir(), "gitconfig")
	mustWrite(t, system, "[safe]\n\tdirectory = "+base+"/*\n")

	owners := ownerMap{}
	setRepositoryOwner(owners, root, filepath.Join(root, ".git"), testForeignUID)
	checker := NewChecker(
		WithCurrentUID(testCurrentUID),
		WithOwner(owners.owner),
		WithEnvironment(func(string) (string, bool) { return "", false }),
		WithHomeDir(tTempHome()),
		WithSystemConfig(system),
		WithGlobalConfigs(),
	)

	repo, err := checker.Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.Trusted || repo.SafeDirectory != base+"/*" {
		t.Fatalf("prefix entry did not match repository: %+v", repo)
	}

	allowed, err := checker.IsSafeDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("repository path should match prefix entry")
	}

	allowed, err = checker.IsSafeDirectory(base)
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("prefix entry must not match its base directory")
	}

	similar := base + "-other"
	mustMkdir(t, similar)
	allowed, err = checker.IsSafeDirectory(similar)
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("prefix entry matched a sibling with the same string prefix")
	}
}

func TestSafeDirectoryStarMatchesEveryRepository(t *testing.T) {
	root, gitDir := makeRepository(t)
	global := filepath.Join(t.TempDir(), "gitconfig")
	mustWrite(t, global, "[safe]\n\tdirectory = *\n")

	owners := ownerMap{}
	setRepositoryOwner(owners, root, gitDir, testForeignUID)
	checker := NewChecker(
		WithCurrentUID(testCurrentUID),
		WithOwner(owners.owner),
		WithEnvironment(func(string) (string, bool) { return "", false }),
		WithHomeDir(tTempHome()),
		WithSystemConfig(""),
		WithGlobalConfigs(global),
	)

	repo, err := checker.Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.Trusted || repo.SafeDirectory != "*" {
		t.Fatalf("* safe.directory did not trust repository: %+v", repo)
	}
}

func TestLocalConfigAsGlobalOverrideIsStillIgnored(t *testing.T) {
	root, gitDir := makeRepository(t)
	localConfig := filepath.Join(gitDir, "config")
	mustWrite(t, localConfig, "[safe]\n\tdirectory = *\n")

	owners := ownerMap{}
	setRepositoryOwner(owners, root, gitDir, testForeignUID)
	checker := NewChecker(
		WithCurrentUID(testCurrentUID),
		WithOwner(owners.owner),
		WithEnvironment(func(string) (string, bool) { return "", false }),
		WithHomeDir(tTempHome()),
		WithSystemConfig(""),
		WithGlobalConfigs(localConfig),
	)

	repo, err := checker.Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Trusted {
		t.Fatalf("repository trusted through its local config path: %+v", repo)
	}
}

func TestSudoUIDIsThePrincipalUID(t *testing.T) {
	root, gitDir := makeRepository(t)
	owners := ownerMap{}
	setRepositoryOwner(owners, root, gitDir, testForeignUID)

	checker := NewChecker(
		WithCurrentUID(0),
		WithOwner(owners.owner),
		WithEnvironment(func(name string) (string, bool) {
			if name == "SUDO_UID" {
				return strconv.FormatUint(uint64(testForeignUID), 10), true
			}
			return "", false
		}),
		WithHomeDir(tTempHome()),
		WithSystemConfig(""),
		WithGlobalConfigs(),
	)

	uid, err := checker.PrincipalUID()
	if err != nil {
		t.Fatal(err)
	}
	if uid != testForeignUID {
		t.Fatalf("PrincipalUID = %d, want SUDO_UID %d", uid, testForeignUID)
	}

	repo, err := checker.Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.Trusted || repo.Reason != TrustReasonOwner || repo.CurrentUID != testForeignUID {
		t.Fatalf("sudo-owned repository not trusted by SUDO_UID: %+v", repo)
	}
}

func TestInvalidSudoUIDFailsClosed(t *testing.T) {
	root, gitDir := makeRepository(t)
	owners := ownerMap{}
	setRepositoryOwner(owners, root, gitDir, testCurrentUID)

	checker := NewChecker(
		WithCurrentUID(0),
		WithOwner(owners.owner),
		WithEnvironment(func(name string) (string, bool) {
			if name == "SUDO_UID" {
				return "not-a-uid", true
			}
			return "", false
		}),
		WithSystemConfig(""),
		WithGlobalConfigs(),
	)

	if _, err := checker.Inspect(root); err == nil {
		t.Fatal("Inspect succeeded with an invalid SUDO_UID")
	}
}

func TestDiscoveryStopsAtForeignParent(t *testing.T) {
	outer := t.TempDir()
	shared := filepath.Join(outer, "shared")
	owned := filepath.Join(shared, "owned")
	mustMkdir(t, filepath.Join(owned, "sub"))
	mustMkdir(t, filepath.Join(shared, ".git"))
	mustWrite(t, filepath.Join(shared, ".git", "config"), "[safe]\n\tdirectory = *\n")

	owners := ownerMap{
		filepath.Clean(outer):                         testCurrentUID,
		filepath.Clean(shared):                        testForeignUID,
		filepath.Clean(filepath.Join(shared, ".git")): testForeignUID,
		filepath.Clean(owned):                         testCurrentUID,
		filepath.Clean(filepath.Join(owned, "sub")):   testCurrentUID,
	}
	checker := newTestChecker(owners)

	repo, err := checker.Discover(filepath.Join(owned, "sub"))
	if !errors.Is(err, ErrUntrustedParent) {
		t.Fatalf("Discover error = %v, want ErrUntrustedParent", err)
	}
	if repo != nil {
		t.Fatalf("Discover returned repository above ownership boundary: %+v", repo)
	}
}

func TestDiscoveryFindsOwnedRepository(t *testing.T) {
	root, gitDir := makeRepository(t)
	sub := filepath.Join(root, "a", "b")
	mustMkdir(t, sub)

	owners := ownerMap{}
	setRepositoryOwner(owners, root, gitDir, testCurrentUID)
	owners[filepath.Clean(filepath.Join(root, "a"))] = testCurrentUID
	owners[filepath.Clean(sub)] = testCurrentUID
	checker := newTestChecker(owners)

	repo, err := checker.Discover(sub)
	if err != nil {
		t.Fatal(err)
	}
	if repo.WorkTree != root || !repo.Trusted {
		t.Fatalf("unexpected discovered repository: %+v", repo)
	}
}

func TestRealOwnershipIntegration(t *testing.T) {
	root, _ := makeRepository(t)
	t.Setenv("SUDO_UID", strconv.FormatUint(uint64(os.Geteuid()), 10))
	checker := NewChecker(
		WithSystemConfig(""),
		WithGlobalConfigs(),
	)

	repo, err := checker.Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.Trusted || repo.Reason != TrustReasonOwner {
		t.Fatalf("repository owned by current user was not trusted: %+v", repo)
	}
}

func TestDefaultConfigPathsAndSudoMismatch(t *testing.T) {
	root, gitDir := makeRepository(t)
	mustWrite(t, filepath.Join(gitDir, "config"), "[safe]\n\tdirectory = *\n")

	global := filepath.Join(t.TempDir(), "gitconfig")
	mustWrite(t, global, "[core]\n\trepositoryformatversion = 0\n")

	otherUID := uint64(os.Geteuid() + 1)
	t.Setenv("SUDO_UID", strconv.FormatUint(otherUID, 10))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("HOME", t.TempDir())

	trusted, err := IsTrusted(root)
	if err != nil {
		t.Fatal(err)
	}
	if trusted {
		t.Fatal("repository trusted through its local safe.directory")
	}
	if _, err := ReadConfig(root); !errors.Is(err, ErrUntrustedRepository) {
		t.Fatalf("ReadConfig error = %v, want ErrUntrustedRepository", err)
	}

	mustWrite(t, global, "[safe]\n\tdirectory = "+root+"\n")
	trusted, err = IsTrusted(root)
	if err != nil {
		t.Fatal(err)
	}
	if !trusted {
		t.Fatal("repository not trusted by GIT_CONFIG_GLOBAL safe.directory")
	}
	if _, err := ReadConfig(root); err != nil {
		t.Fatalf("ReadConfig after protected authorization: %v", err)
	}
}
