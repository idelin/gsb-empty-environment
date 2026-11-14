package netsec

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T, root, workspace, account string, secret []byte, tweak func(*Config)) (*Store, *MemoryKeyring) {
	t.Helper()
	keyring, err := NewMemoryKeyring(secret)
	if err != nil {
		t.Fatalf("NewMemoryKeyring: %v", err)
	}
	cfg := Config{
		Root:        root,
		WorkspaceID: workspace,
		AccountID:   account,
		KeyProvider: keyring,
	}
	if tweak != nil {
		tweak(&cfg)
	}
	store, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, keyring
}

func testCaller(id, workspace, account string) Caller {
	return Caller{ID: id, WorkspaceID: workspace, AccountID: account}
}

func TestLegitimateReadWriteAndEncryptedArtifacts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, _ := newTestStore(t, root, "ws-a", "acct-1", []byte("workspace-a-master-secret"), nil)
	caller := testCaller("svc-a", "ws-a", "acct-1")

	first := []byte(`{"cloud":{"access_key":"AKIA-LEGITIMATE","secret":"first-super-secret"}}`)
	version, err := store.WriteState(ctx, caller, first)
	if err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if version != 1 {
		t.Fatalf("version = %d, want 1", version)
	}
	got, err := store.ReadState(ctx, caller)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if !bytes.Equal(got, first) {
		t.Fatalf("state mismatch")
	}

	second := []byte(`{"cloud":{"access_key":"AKIA-LEGITIMATE","secret":"second-super-secret"}}`)
	version, err = store.WriteState(ctx, caller, second)
	if err != nil {
		t.Fatalf("WriteState second: %v", err)
	}
	if version != 2 {
		t.Fatalf("version = %d, want 2", version)
	}
	got, err = store.ReadState(ctx, caller)
	if err != nil {
		t.Fatalf("ReadState second: %v", err)
	}
	if !bytes.Equal(got, second) {
		t.Fatalf("second state mismatch")
	}

	backups, err := store.Backups(ctx, caller)
	if err != nil {
		t.Fatalf("Backups: %v", err)
	}
	if len(backups) != 1 || backups[0] != 1 {
		t.Fatalf("backups = %v, want [1]", backups)
	}
	backup, err := store.ReadBackup(ctx, caller, 1)
	if err != nil {
		t.Fatalf("ReadBackup: %v", err)
	}
	if !bytes.Equal(backup, first) {
		t.Fatalf("backup mismatch")
	}

	handle, err := store.AcquireLock(ctx, caller, time.Minute)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	assertArtifactsAreOpaque(t, root, [][]byte{
		[]byte("AKIA-LEGITIMATE"),
		[]byte("first-super-secret"),
		[]byte("second-super-secret"),
		[]byte("ws-a"),
		[]byte("acct-1"),
		[]byte(caller.ID),
		[]byte(handle.token),
	})
	if err := handle.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestCrossWorkspaceCannotReadCredentials(t *testing.T) {
	ctx := context.Background()
	rootA := t.TempDir()
	rootB := t.TempDir()
	secretA := []byte("workspace-a-only-secret")
	secretB := []byte("workspace-b-only-secret")
	credential := []byte(`{"cloud":{"access_key":"AKIA-CROSS-WORKSPACE","secret":"do-not-leak"}}`)

	storeA, _ := newTestStore(t, rootA, "ws-a", "acct-a", secretA, nil)
	callerA := testCaller("svc-a", "ws-a", "acct-a")
	if _, err := storeA.WriteState(ctx, callerA, credential); err != nil {
		t.Fatalf("WriteState A: %v", err)
	}

	// Simulate an attacker in workspace B copying A's readable artifacts into
	// its own workspace and opening a normal store there.
	storeB, _ := newTestStore(t, rootB, "ws-b", "acct-b", secretB, nil)
	copyEncryptedArtifacts(t, rootA, rootB)
	callerB := testCaller("svc-b", "ws-b", "acct-b")
	data, err := storeB.ReadState(ctx, callerB)
	if err == nil {
		t.Fatalf("cross-workspace read unexpectedly succeeded: %q", data)
	}
	if bytes.Contains(data, []byte("AKIA-CROSS-WORKSPACE")) {
		t.Fatalf("credential leaked through cross-workspace read")
	}

	// A valid caller from B cannot borrow A's store directly.
	if _, err := storeA.ReadState(ctx, callerB); !errors.Is(err, ErrWorkspaceMismatch) {
		t.Fatalf("caller from B reading store A: err = %v, want ErrWorkspaceMismatch", err)
	}

	// Even an attacker who knows A's workspace and account but not its key
	// cannot decrypt the copied artifacts.
	wrongKeyStore, _ := newTestStore(t, rootB, "ws-a", "acct-a", []byte("attacker-key"), nil)
	if _, err := wrongKeyStore.ReadState(ctx, testCaller("attacker", "ws-a", "acct-a")); err == nil {
		t.Fatalf("read with wrong key unexpectedly succeeded")
	}

	// Legitimate access to workspace A still works.
	data, err = storeA.ReadState(ctx, callerA)
	if err != nil {
		t.Fatalf("legitimate ReadState A: %v", err)
	}
	if !bytes.Equal(data, credential) {
		t.Fatalf("legitimate state mismatch")
	}
}

func TestKeyRotationReencryptsEveryArtifact(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, keyring := newTestStore(t, root, "ws-rotate", "acct-rotate", []byte("rotation-master-secret"), nil)
	caller := testCaller("svc-rotate", "ws-rotate", "acct-rotate")

	var revisions [][]byte
	for i := 0; i < 3; i++ {
		revision := []byte(`{"cloud":{"secret":"rotation-secret-` + string(rune('A'+i)) + `"}}`)
		revisions = append(revisions, revision)
		if _, err := store.WriteState(ctx, caller, revision); err != nil {
			t.Fatalf("WriteState %d: %v", i, err)
		}
	}
	lock, err := store.AcquireLock(ctx, caller, time.Hour)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	generation, err := store.Rotate(ctx, caller)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if generation != 2 {
		t.Fatalf("generation = %d, want 2", generation)
	}

	data, err := store.ReadState(ctx, caller)
	if err != nil {
		t.Fatalf("ReadState after rotation: %v", err)
	}
	if !bytes.Equal(data, revisions[2]) {
		t.Fatalf("state changed during rotation")
	}
	for version := uint64(1); version <= 2; version++ {
		backup, err := store.ReadBackup(ctx, caller, version)
		if err != nil {
			t.Fatalf("ReadBackup(%d) after rotation: %v", version, err)
		}
		if !bytes.Equal(backup, revisions[version-1]) {
			t.Fatalf("backup %d changed during rotation", version)
		}
	}

	assertAllEnvelopeGenerations(t, root, generation)

	// The lock handle survives re-encryption.
	if err := lock.Release(ctx); err != nil {
		t.Fatalf("Release after rotation: %v", err)
	}

	// Old key material is no longer needed once all artifacts were rotated.
	removed := keyring.PruneBefore(2)
	if len(removed) != 1 || removed[0] != 1 {
		t.Fatalf("pruned = %v, want [1]", removed)
	}
	if _, err := store.ReadState(ctx, caller); err != nil {
		t.Fatalf("ReadState after pruning old key: %v", err)
	}
	if _, err := store.ReadBackup(ctx, caller, 1); err != nil {
		t.Fatalf("ReadBackup after pruning old key: %v", err)
	}
}

func TestExportRequiresExplicitAuthorization(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	secret := []byte(`{"cloud":{"secret":"export-super-secret"}}`)
	caller := testCaller("svc-export", "ws-export", "acct-export")

	deniedStore, _ := newTestStore(t, root, "ws-export", "acct-export", []byte("export-master-secret"), nil)
	if _, err := deniedStore.WriteState(ctx, caller, secret); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if _, err := deniedStore.ExportState(ctx, caller, ExportRequest{Reason: "incident"}); !errors.Is(err, ErrExportDenied) {
		t.Fatalf("export without authorizer: err = %v, want ErrExportDenied", err)
	}

	authority, err := NewExportAuthority([]byte("separate-export-authority-secret"))
	if err != nil {
		t.Fatalf("NewExportAuthority: %v", err)
	}
	authorizedStore, _ := newTestStore(t, root, "ws-export", "acct-export", []byte("export-master-secret"), func(cfg *Config) {
		cfg.ExportAuthorizer = authority
	})

	request := ExportRequest{
		Reason:      "incident-123",
		Destination: filepath.Join(t.TempDir(), "export.json"),
	}
	if _, err := authorizedStore.ExportState(ctx, caller, request); !errors.Is(err, ErrExportDenied) {
		t.Fatalf("export without grant: err = %v, want ErrExportDenied", err)
	}

	token, err := authority.Grant(caller, request.Reason, request.Destination, time.Minute)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	request.Authorization = token
	data, err := authorizedStore.ExportState(ctx, caller, request)
	if err != nil {
		t.Fatalf("authorized export: %v", err)
	}
	if !bytes.Equal(data, secret) {
		t.Fatalf("exported data mismatch")
	}

	wrongReason := request
	wrongReason.Reason = "different-reason"
	if _, err := authorizedStore.ExportState(ctx, caller, wrongReason); !errors.Is(err, ErrExportDenied) {
		t.Fatalf("wrong reason: err = %v, want ErrExportDenied", err)
	}
	otherCaller := testCaller("svc-other", "ws-export", "acct-export")
	if _, err := authorizedStore.ExportState(ctx, otherCaller, request); !errors.Is(err, ErrExportDenied) {
		t.Fatalf("grant replay by another caller: err = %v, want ErrExportDenied", err)
	}

	if err := authorizedStore.ExportTo(ctx, caller, request); err != nil {
		t.Fatalf("ExportTo: %v", err)
	}
	exported, err := os.ReadFile(request.Destination)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	if !bytes.Equal(exported, secret) {
		t.Fatalf("exported file mismatch")
	}
	if info, err := os.Stat(request.Destination); err != nil || info.Mode().Perm() != stateFileMode {
		t.Fatalf("export file mode = %v, err = %v", info.Mode().Perm(), err)
	}

	insideRoot := request
	insideRoot.Destination = filepath.Join(root, "plaintext-export.json")
	grantForInside, err := authority.Grant(caller, insideRoot.Reason, insideRoot.Destination, time.Minute)
	if err != nil {
		t.Fatalf("Grant inside root: %v", err)
	}
	insideRoot.Authorization = grantForInside
	if err := authorizedStore.ExportTo(ctx, caller, insideRoot); !errors.Is(err, ErrExportDenied) {
		t.Fatalf("export inside state root: err = %v, want ErrExportDenied", err)
	}
	if _, err := os.Stat(insideRoot.Destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plaintext export was created inside state root")
	}
}

func TestExpiredExportGrantIsRejected(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	caller := testCaller("svc-expiry", "ws-expiry", "acct-expiry")
	now := time.Unix(1_700_000_000, 0)
	authority, err := NewExportAuthority([]byte("expiry-authority-secret"))
	if err != nil {
		t.Fatalf("NewExportAuthority: %v", err)
	}
	authority.WithClock(func() time.Time { return now })
	store, _ := newTestStore(t, root, "ws-expiry", "acct-expiry", []byte("expiry-master-secret"), func(cfg *Config) {
		cfg.ExportAuthorizer = authority
		cfg.Clock = func() time.Time { return now }
	})
	if _, err := store.WriteState(ctx, caller, []byte(`{"secret":"expiry-secret"}`)); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	request := ExportRequest{Reason: "short-lived", Destination: filepath.Join(root, "..", "export.json")}
	token, err := authority.Grant(caller, request.Reason, request.Destination, time.Second)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	request.Authorization = token
	now = now.Add(2 * time.Second)
	if _, err := store.ExportState(ctx, caller, request); !errors.Is(err, ErrExportDenied) {
		t.Fatalf("expired grant: err = %v, want ErrExportDenied", err)
	}
}

func TestTamperingIsDetected(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, _ := newTestStore(t, root, "ws-tamper", "acct-tamper", []byte("tamper-master-secret"), nil)
	caller := testCaller("svc-tamper", "ws-tamper", "acct-tamper")
	if _, err := store.WriteState(ctx, caller, []byte(`{"secret":"tamper-secret"}`)); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	statePath := filepath.Join(root, store.stateName())
	original, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state artifact: %v", err)
	}
	tampered := bytes.Replace(original, []byte(`"ciphertext":"`), []byte(`"ciphertext":"A`), 1)
	if bytes.Equal(tampered, original) {
		t.Fatalf("failed to tamper ciphertext")
	}
	if err := os.WriteFile(statePath, tampered, stateFileMode); err != nil {
		t.Fatalf("write tampered state: %v", err)
	}
	if data, err := store.ReadState(ctx, caller); err == nil {
		t.Fatalf("tampered read succeeded: %q", data)
	} else if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("tampered read error = %v, want ErrIntegrity", err)
	}

	// Restore, then verify a stale payload cannot be substituted.
	if err := os.WriteFile(statePath, original, stateFileMode); err != nil {
		t.Fatalf("restore state: %v", err)
	}
	stale, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read stale: %v", err)
	}
	if _, err := store.WriteState(ctx, caller, []byte(`{"secret":"newer-secret"}`)); err != nil {
		t.Fatalf("WriteState newer: %v", err)
	}
	if err := os.WriteFile(statePath, stale, stateFileMode); err != nil {
		t.Fatalf("substitute stale state: %v", err)
	}
	if _, err := store.ReadState(ctx, caller); err == nil {
		t.Fatalf("stale state substitution succeeded")
	}
}

func TestVersionAndBackupTamperingIsDetected(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, _ := newTestStore(t, root, "ws-artifacts", "acct-artifacts", []byte("artifact-master-secret"), nil)
	caller := testCaller("svc-artifacts", "ws-artifacts", "acct-artifacts")
	for i := 0; i < 3; i++ {
		if _, err := store.WriteState(ctx, caller, []byte{byte(i)}); err != nil {
			t.Fatalf("WriteState %d: %v", i, err)
		}
	}

	versionPath := filepath.Join(root, store.versionName())
	versionOriginal, err := os.ReadFile(versionPath)
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if err := os.WriteFile(versionPath, tamperCiphertext(t, versionOriginal), stateFileMode); err != nil {
		t.Fatalf("tamper version: %v", err)
	}
	if _, err := store.ReadState(ctx, caller); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("tampered version: err = %v, want ErrIntegrity", err)
	}
	if err := os.WriteFile(versionPath, versionOriginal, stateFileMode); err != nil {
		t.Fatalf("restore version: %v", err)
	}

	backupPath := filepath.Join(root, store.backupName(1))
	backupOriginal, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if err := os.WriteFile(backupPath, tamperCiphertext(t, backupOriginal), stateFileMode); err != nil {
		t.Fatalf("tamper backup: %v", err)
	}
	if _, err := store.ReadBackup(ctx, caller, 1); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("tampered backup: err = %v, want ErrIntegrity", err)
	}
	if err := os.WriteFile(backupPath, backupOriginal, stateFileMode); err != nil {
		t.Fatalf("restore backup: %v", err)
	}

	// Backup 1's valid ciphertext is bound to record/backup/1 and sequence 1,
	// so placing it at backup 2 must not authenticate.
	if err := os.WriteFile(filepath.Join(root, store.backupName(2)), backupOriginal, stateFileMode); err != nil {
		t.Fatalf("swap backup: %v", err)
	}
	if _, err := store.ReadBackup(ctx, caller, 2); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("swapped backup: err = %v, want ErrIntegrity", err)
	}
}

func TestRotateToExternalGeneration(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, keyring := newTestStore(t, root, "ws-external", "acct-external", []byte("external-master-secret"), nil)
	caller := testCaller("svc-external", "ws-external", "acct-external")
	if _, err := store.WriteState(ctx, caller, []byte(`{"secret":"external"}`)); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if err := keyring.AddGeneration(4, []byte("external-kms-generation-key")); err != nil {
		t.Fatalf("AddGeneration: %v", err)
	}
	if err := store.RotateTo(ctx, caller, 4); err != nil {
		t.Fatalf("RotateTo: %v", err)
	}
	assertAllEnvelopeGenerations(t, root, 4)
	if current, err := keyring.CurrentGeneration(ctx); err != nil || current != 4 {
		t.Fatalf("current generation = %d, err = %v; want 4", current, err)
	}
	if _, err := store.WriteState(ctx, caller, []byte(`{"secret":"external-next"}`)); err != nil {
		t.Fatalf("WriteState after RotateTo: %v", err)
	}
	info, err := store.Info(ctx, caller)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.CurrentGeneration != 4 || info.Version != 2 {
		t.Fatalf("info = %+v, want generation 4 version 2", info)
	}
}

func tamperCiphertext(t *testing.T, raw []byte) []byte {
	t.Helper()
	tampered := bytes.Replace(raw, []byte(`"ciphertext":"`), []byte(`"ciphertext":"A`), 1)
	if bytes.Equal(tampered, raw) {
		t.Fatalf("failed to tamper ciphertext")
	}
	return tampered
}

func TestCallerVerifierAndContextIdentity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	verifier := CallerVerifierFunc(func(_ context.Context, caller Caller) (Caller, error) {
		if caller.Token != "valid-token" {
			return Caller{}, errors.New("bad token")
		}
		return caller, nil
	})
	store, _ := newTestStore(t, root, "ws-verify", "acct-verify", []byte("verify-master-secret"), func(cfg *Config) {
		cfg.VerifyCaller = verifier
	})
	caller := testCaller("svc-verify", "ws-verify", "acct-verify")

	if _, err := store.WriteState(ctx, caller, []byte(`{"secret":"verify"}`)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("missing token: err = %v, want ErrUnauthenticated", err)
	}
	caller.Token = "valid-token"
	if _, err := store.WriteState(ctx, caller, []byte(`{"secret":"verify"}`)); err != nil {
		t.Fatalf("valid token WriteState: %v", err)
	}

	ctx = ContextWithCaller(ctx, caller)
	data, err := store.ReadState(ctx, Caller{})
	if err != nil {
		t.Fatalf("ReadState from context caller: %v", err)
	}
	if string(data) != `{"secret":"verify"}` {
		t.Fatalf("state mismatch")
	}
	conflicting := testCaller("svc-verify", "ws-other", "acct-verify")
	conflicting.Token = "valid-token"
	if _, err := store.ReadState(ctx, conflicting); err == nil {
		t.Fatalf("explicit caller overrode authenticated context workspace")
	}
}

func TestLockOwnershipAndExpiry(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	store, _ := newTestStore(t, root, "ws-lock", "acct-lock", []byte("lock-master-secret"), func(cfg *Config) {
		cfg.Clock = func() time.Time { return now }
	})
	caller := testCaller("svc-lock", "ws-lock", "acct-lock")
	handle, err := store.AcquireLock(ctx, caller, time.Minute)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if _, err := store.AcquireLock(ctx, caller, time.Minute); !errors.Is(err, ErrLocked) {
		t.Fatalf("second AcquireLock: err = %v, want ErrLocked", err)
	}
	wrong := &LockHandle{store: store, caller: caller, token: "00"}
	if err := wrong.Release(ctx); !errors.Is(err, ErrLockOwnership) {
		t.Fatalf("wrong release: err = %v, want ErrLockOwnership", err)
	}
	if err := handle.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}

	expiring, err := store.AcquireLock(ctx, caller, time.Second)
	if err != nil {
		t.Fatalf("AcquireLock expiring: %v", err)
	}
	_ = expiring
	now = now.Add(2 * time.Second)
	replacement, err := store.AcquireLock(ctx, testCaller("svc-lock-2", "ws-lock", "acct-lock"), time.Minute)
	if err != nil {
		t.Fatalf("AcquireLock after expiry: %v", err)
	}
	if err := replacement.Release(ctx); err != nil {
		t.Fatalf("replacement Release: %v", err)
	}
}

func TestBackupLimitKeepsNewestRevisions(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, _ := newTestStore(t, root, "ws-backup", "acct-backup", []byte("backup-master-secret"), func(cfg *Config) {
		cfg.BackupLimit = 2
	})
	caller := testCaller("svc-backup", "ws-backup", "acct-backup")
	for i := 0; i < 5; i++ {
		if _, err := store.WriteState(ctx, caller, []byte{byte(i)}); err != nil {
			t.Fatalf("WriteState %d: %v", i, err)
		}
	}
	versions, err := store.Backups(ctx, caller)
	if err != nil {
		t.Fatalf("Backups: %v", err)
	}
	if len(versions) != 2 || versions[0] != 3 || versions[1] != 4 {
		t.Fatalf("backups = %v, want [3 4]", versions)
	}
}

func TestMissingVersionFailsClosed(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, _ := newTestStore(t, root, "ws-orphan", "acct-orphan", []byte("orphan-master-secret"), nil)
	caller := testCaller("svc-orphan", "ws-orphan", "acct-orphan")
	if _, err := store.WriteState(ctx, caller, []byte(`{"secret":"orphan"}`)); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if err := os.Remove(filepath.Join(root, store.versionName())); err != nil {
		t.Fatalf("remove version artifact: %v", err)
	}
	if _, err := store.WriteState(ctx, caller, []byte(`{"secret":"replacement"}`)); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("WriteState without version: err = %v, want ErrIntegrity", err)
	}
}

func copyEncryptedArtifacts(t *testing.T, source, destination string) {
	t.Helper()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatalf("read source root: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".enc") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatalf("read artifact %s: %v", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(destination, entry.Name()), data, stateFileMode); err != nil {
			t.Fatalf("copy artifact %s: %v", entry.Name(), err)
		}
	}
}

func assertArtifactsAreOpaque(t *testing.T, root string, forbidden [][]byte) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	seen := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".enc") {
			continue
		}
		seen++
		path := filepath.Join(root, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		env, err := parseEnvelope(data)
		if err != nil {
			t.Fatalf("%s is not a valid envelope: %v", entry.Name(), err)
		}
		if env.Generation == 0 {
			t.Fatalf("%s has no key generation", entry.Name())
		}
		for _, value := range forbidden {
			if len(value) > 0 && bytes.Contains(data, value) {
				t.Fatalf("%s contains plaintext %q", entry.Name(), value)
			}
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", entry.Name(), err)
		}
		if info.Mode().Perm() != stateFileMode {
			t.Fatalf("%s mode = %o, want %o", entry.Name(), info.Mode().Perm(), stateFileMode)
		}
	}
	if seen < 3 {
		t.Fatalf("found %d encrypted artifacts, want state, version, backup, and lock", seen)
	}
}

func assertAllEnvelopeGenerations(t *testing.T, root string, generation uint64) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	seenState := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".enc") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		env, err := parseEnvelope(data)
		if err != nil {
			t.Fatalf("%s invalid: %v", entry.Name(), err)
		}
		if env.Generation != generation {
			t.Fatalf("%s generation = %d, want %d", entry.Name(), env.Generation, generation)
		}
		if strings.HasSuffix(entry.Name(), ".state.enc") {
			seenState = true
		}
	}
	if !seenState {
		t.Fatalf("state artifact not found")
	}
}

func TestArtifactFilesUseOwnerOnlyMode(t *testing.T) {
	root := t.TempDir()
	store, _ := newTestStore(t, root, "ws-mode", "acct-mode", []byte("mode-master-secret"), nil)
	caller := testCaller("svc-mode", "ws-mode", "acct-mode")
	if _, err := store.WriteState(context.Background(), caller, []byte("mode")); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("info: %v", err)
		}
		if info.Mode().Perm() != fs.FileMode(stateFileMode) {
			t.Fatalf("%s mode = %o", entry.Name(), info.Mode().Perm())
		}
	}
}

func TestBrokenRandomSourceCannotReuseNonce(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repeated := bytes.Repeat([]byte{0x33}, 1<<20)
	store, _ := newTestStore(t, root, "ws-random", "acct-random", []byte("random-master-secret"), func(cfg *Config) {
		cfg.Rand = bytes.NewReader(repeated)
	})
	caller := testCaller("svc-random", "ws-random", "acct-random")

	if _, err := store.WriteState(ctx, caller, []byte("one")); err != nil {
		t.Fatalf("WriteState one: %v", err)
	}
	first := readEnvelope(t, filepath.Join(root, store.versionName()))
	if _, err := store.WriteState(ctx, caller, []byte("two")); err != nil {
		t.Fatalf("WriteState two: %v", err)
	}
	second := readEnvelope(t, filepath.Join(root, store.versionName()))
	if first.Salt == second.Salt {
		t.Fatalf("salt was reused with a repeated random source")
	}
	if first.Nonce == second.Nonce {
		t.Fatalf("nonce was reused with a repeated random source")
	}
}

func readEnvelope(t *testing.T, path string) envelope {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read envelope: %v", err)
	}
	env, err := parseEnvelope(raw)
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	return env
}
