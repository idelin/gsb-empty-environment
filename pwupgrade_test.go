package pwupgrade

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

const testUser = "user-1"

func legacyHash(t *testing.T, password string) string {
	t.Helper()
	return EncodeLegacySHA1([]byte("legacy-salt-0001"), password)
}

func weakPBKDF2(t *testing.T, password string, iterations int) string {
	t.Helper()
	salt := []byte("weak-salt-12345")
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		t.Fatalf("pbkdf2 setup: %v", err)
	}
	return encodePBKDF2(iterations, salt, key)
}

func TestVerifyLegacySHA1(t *testing.T) {
	stored := legacyHash(t, "correct horse")

	ok, err := Verify("correct horse", stored)
	if err != nil || !ok {
		t.Fatalf("expected successful verify, got ok=%v err=%v", ok, err)
	}
	ok, err = Verify("wrong horse", stored)
	if err != nil || ok {
		t.Fatalf("expected mismatch without error, got ok=%v err=%v", ok, err)
	}
}

func TestVerifyPBKDF2(t *testing.T) {
	stored, err := Hash("correct horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(stored, "pbkdf2_sha256$600000$") {
		t.Fatalf("unexpected current-format hash: %q", stored)
	}

	ok, err := Verify("correct horse", stored)
	if err != nil || !ok {
		t.Fatalf("expected successful verify, got ok=%v err=%v", ok, err)
	}
	ok, err = Verify("correct horse!", stored)
	if err != nil || ok {
		t.Fatalf("expected mismatch without error, got ok=%v err=%v", ok, err)
	}

	other, _ := Hash("correct horse")
	if other == stored {
		t.Fatal("expected distinct salts to produce distinct hashes")
	}
}

func TestRejectUnknownAndMalformed(t *testing.T) {
	bigSalt := make([]byte, maxSaltLen+1)
	bigKey := make([]byte, maxKeyLen+1)

	cases := map[string]error{
		"":          ErrMalformedHash,
		"plaintext": ErrMalformedHash,
		"md5$" + b64.EncodeToString([]byte("s")) + "$" + b64.EncodeToString([]byte("k")): ErrUnknownFormat,
		"sha1$":        ErrMalformedHash,
		"sha1$@@@$xxx": ErrMalformedHash,
		"sha1$" + b64.EncodeToString(bigSalt[:16]) + "$" + b64.EncodeToString(bigKey[:10]): ErrInvalidParameters,
		"pbkdf2_sha256": ErrMalformedHash,
		"pbkdf2_sha256$abc$" + b64.EncodeToString([]byte("s")) + "$" + b64.EncodeToString(bigKey[:32]):                  ErrInvalidParameters,
		"pbkdf2_sha256$0$" + b64.EncodeToString([]byte("s")) + "$" + b64.EncodeToString(bigKey[:32]):                    ErrInvalidParameters,
		"pbkdf2_sha256$-5$" + b64.EncodeToString([]byte("s")) + "$" + b64.EncodeToString(bigKey[:32]):                   ErrInvalidParameters,
		"pbkdf2_sha256$99999999999999999999$" + b64.EncodeToString([]byte("s")) + "$" + b64.EncodeToString(bigKey[:32]): ErrInvalidParameters,
		"pbkdf2_sha256$1000$" + b64.EncodeToString(bigSalt) + "$" + b64.EncodeToString(bigKey[:32]):                     ErrInvalidParameters,
		"pbkdf2_sha256$1000$" + b64.EncodeToString(bigSalt[:16]) + "$" + b64.EncodeToString(bigKey):                     ErrInvalidParameters,
		"pbkdf2_sha256$1000$" + b64.EncodeToString(bigSalt[:16]) + "$":                                                  ErrMalformedHash,
	}

	for stored, wantErr := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Verify(%q) panicked: %v", stored, r)
				}
			}()
			ok, err := Verify("whatever", stored)
			if !errors.Is(err, wantErr) {
				t.Errorf("Verify(%q): want %v, got ok=%v err=%v", stored, wantErr, ok, err)
			}
			if ok {
				t.Errorf("Verify(%q): must not succeed on invalid hash", stored)
			}
			if _, err := NeedsUpgrade(stored); !errors.Is(err, wantErr) {
				t.Errorf("NeedsUpgrade(%q): want %v, got err=%v", stored, wantErr, err)
			}
		}()
	}
}

func TestUpgradeLegacyAfterLogin(t *testing.T) {
	store := NewMemoryStore()
	store.Put(testUser, legacyHash(t, "old-pw"))

	ok, err := VerifyAndUpgrade(store, testUser, "old-pw")
	if err != nil || !ok {
		t.Fatalf("login: ok=%v err=%v", ok, err)
	}

	upgraded, err := store.GetHash(testUser)
	if err != nil {
		t.Fatalf("get hash: %v", err)
	}
	if strings.HasPrefix(upgraded, "sha1$") {
		t.Fatalf("legacy hash was not upgraded: %q", upgraded)
	}
	parts := strings.Split(upgraded, "$")
	if parts[0] != algPBKDF2 || parts[1] != fmt.Sprint(currentIterations) {
		t.Fatalf("upgraded hash uses wrong parameters: %q", upgraded)
	}
	if good, err := Verify("old-pw", upgraded); err != nil || !good {
		t.Fatalf("upgraded hash must still verify the original password: ok=%v err=%v", good, err)
	}

	// A second login with current parameters must leave the hash untouched.
	ok, err = VerifyAndUpgrade(store, testUser, "old-pw")
	if err != nil || !ok {
		t.Fatalf("second login: ok=%v err=%v", ok, err)
	}
	again, _ := store.GetHash(testUser)
	if again != upgraded {
		t.Fatal("current-format hash was rewritten unnecessarily")
	}
}

func TestUpgradeWeakPBKDF2(t *testing.T) {
	store := NewMemoryStore()
	store.Put(testUser, weakPBKDF2(t, "old-pw", 1_000))

	need, err := NeedsUpgrade(weakPBKDF2(t, "old-pw", 1_000))
	if err != nil || !need {
		t.Fatalf("weak PBKDF2 should require upgrade: need=%v err=%v", need, err)
	}
	if ok, err := VerifyAndUpgrade(store, testUser, "old-pw"); err != nil || !ok {
		t.Fatalf("login: ok=%v err=%v", ok, err)
	}
	stored, _ := store.GetHash(testUser)
	if strings.Split(stored, "$")[1] != fmt.Sprint(currentIterations) {
		t.Fatalf("expected rehash at %d iterations, got %q", currentIterations, stored)
	}
}

func TestWrongPasswordDoesNotTouchHash(t *testing.T) {
	store := NewMemoryStore()
	original := legacyHash(t, "old-pw")
	store.Put(testUser, original)

	ok, err := VerifyAndUpgrade(store, testUser, "guessed-pw")
	if err != nil || ok {
		t.Fatalf("expected failed login, got ok=%v err=%v", ok, err)
	}
	stored, _ := store.GetHash(testUser)
	if stored != original {
		t.Fatal("failed login must not modify the stored hash")
	}
}

func TestUpgradeAbortsWhenPasswordChanged(t *testing.T) {
	store := NewMemoryStore()
	original := legacyHash(t, "old-pw")
	store.Put(testUser, original)

	changed := make(chan string, 1)
	store.BeforeCAS = func() {
		// Simulate the user changing their password in the window between
		// the login's read and its compare-and-swap write.
		newer, err := Hash("brand-new-pw")
		if err != nil {
			t.Errorf("hash new password: %v", err)
			return
		}
		if err := store.SetPassword(testUser, newer); err != nil {
			t.Errorf("concurrent SetPassword: %v", err)
			return
		}
		changed <- newer
	}

	ok, err := VerifyAndUpgrade(store, testUser, "old-pw")
	if err != nil || !ok {
		t.Fatalf("login should still succeed on verification: ok=%v err=%v", ok, err)
	}

	newer := <-changed
	stored, err := store.GetHash(testUser)
	if err != nil {
		t.Fatalf("get hash: %v", err)
	}
	if stored != newer {
		t.Fatal("concurrent password change was overwritten by the stale upgrade")
	}
	if good, err := Verify("brand-new-pw", stored); err != nil || !good {
		t.Fatalf("new password no longer verifies: ok=%v err=%v", good, err)
	}
	if good, _ := Verify("old-pw", stored); good {
		t.Fatal("old password must not verify after the password change")
	}
}

func TestMemoryStoreCompareAndSwap(t *testing.T) {
	store := NewMemoryStore()
	store.Put(testUser, "hash-a")

	swapped, err := store.CompareAndSwapHash(testUser, "hash-a", "hash-b")
	if err != nil || !swapped {
		t.Fatalf("expected CAS success, got swapped=%v err=%v", swapped, err)
	}
	swapped, err = store.CompareAndSwapHash(testUser, "hash-a", "hash-c")
	if err != nil || swapped {
		t.Fatalf("stale CAS must fail, got swapped=%v err=%v", swapped, err)
	}
	stored, _ := store.GetHash(testUser)
	if stored != "hash-b" {
		t.Fatalf("failed CAS must not change the hash, got %q", stored)
	}
	if _, err := store.CompareAndSwapHash("ghost", "x", "y"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestConcurrentLoginAndChange(t *testing.T) {
	store := NewMemoryStore()
	store.Put(testUser, legacyHash(t, "hunter2"))

	const (
		loginWorkers = 4
		changeCount  = 25
	)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for range loginWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ok, err := VerifyAndUpgrade(store, testUser, "hunter2")
				if err != nil {
					t.Errorf("login error: %v", err)
					return
				}
				if ok {
					// A successful hunter2 login is only possible while a
					// hunter2-verifying hash is stored; the CAS must never
					// clobber a concurrently written replacement.
				}
			}
		}()
	}

	var lastHash string
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range changeCount {
			password := fmt.Sprintf("rotated-%d", i)
			hash, err := Hash(password)
			if err != nil {
				t.Errorf("hash: %v", err)
				return
			}
			if err := store.SetPassword(testUser, hash); err != nil {
				t.Errorf("SetPassword: %v", err)
				return
			}
			lastHash = hash
			time.Sleep(time.Millisecond)
		}
		close(stop)
	}()

	wg.Wait()

	final, err := store.GetHash(testUser)
	if err != nil {
		t.Fatalf("final get: %v", err)
	}
	// Once the first rotated hash is committed, no hunter2 login can verify
	// again, so the final hash must be exactly the last password-change hash.
	if final != lastHash {
		t.Fatal("stale login upgrade overwrote a concurrently changed password")
	}
	if ok, err := Verify("rotated-24", final); err != nil || !ok {
		t.Fatalf("final hash does not verify the newest password: ok=%v err=%v", ok, err)
	}
	if ok, _ := Verify("hunter2", final); ok {
		t.Fatal("retired password must not verify against the final hash")
	}
}

func TestUserNotFound(t *testing.T) {
	store := NewMemoryStore()
	if ok, err := VerifyAndUpgrade(store, "missing", "pw"); ok || !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound with ok=false, got ok=%v err=%v", ok, err)
	}
}
