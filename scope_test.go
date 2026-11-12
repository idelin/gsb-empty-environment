package netsec

import (
	"errors"
	"strings"
	"testing"
)

func plaintext() map[string][]byte {
	return map[string][]byte{"password": []byte("correct horse battery staple")}
}

func strictSecret(namespace, name string) SealedSecret {
	return SealedSecret{
		Namespace:     namespace,
		Name:          name,
		EncryptedData: map[string]string{"password": "ciphertext"},
	}
}

func withAnnotations(secret SealedSecret, annotations map[string]string) SealedSecret {
	secret.Annotations = annotations
	return secret
}

func TestScopeAndLabelDerivation(t *testing.T) {
	tests := []struct {
		name         string
		secret       SealedSecret
		wantScope    Scope
		wantLabel    string
		wantErr      error
		wantLabelErr error
	}{
		{
			name:      "strict by default",
			secret:    strictSecret("team-a", "db-creds"),
			wantScope: StrictScope,
			wantLabel: "team-a/db-creds",
		},
		{
			name: "namespace-wide",
			secret: withAnnotations(strictSecret("team-a", "db-creds"), map[string]string{
				NamespaceWideAnnotation: "true",
			}),
			wantScope: NamespaceWideScope,
			wantLabel: "team-a",
		},
		{
			name: "cluster-wide",
			secret: withAnnotations(strictSecret("team-a", "db-creds"), map[string]string{
				ClusterWideAnnotation: "true",
			}),
			wantScope: ClusterWideScope,
			wantLabel: "",
		},
		{
			name: "explicitly false does not widen",
			secret: withAnnotations(strictSecret("team-a", "db-creds"), map[string]string{
				ClusterWideAnnotation:   "false",
				NamespaceWideAnnotation: "true",
			}),
			wantScope: NamespaceWideScope,
			wantLabel: "team-a",
		},
		{
			name: "both annotations",
			secret: withAnnotations(strictSecret("team-a", "db-creds"), map[string]string{
				ClusterWideAnnotation:   "true",
				NamespaceWideAnnotation: "true",
			}),
			wantErr: ErrConflictingScopeAnnotations,
		},
		{
			name: "invalid annotation value",
			secret: withAnnotations(strictSecret("team-a", "db-creds"), map[string]string{
				ClusterWideAnnotation: "yes",
			}),
			wantErr: ErrInvalidScopeAnnotation,
		},
		{
			name:         "strict requires name",
			secret:       strictSecret("team-a", ""),
			wantScope:    StrictScope,
			wantLabelErr: ErrInvalidObject,
		},
		{
			name: "namespace-wide requires namespace",
			secret: withAnnotations(strictSecret("", "db-creds"), map[string]string{
				NamespaceWideAnnotation: "true",
			}),
			wantScope:    NamespaceWideScope,
			wantLabelErr: ErrInvalidObject,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scope, err := test.secret.Scope()
			label, labelErr := test.secret.ExpectedLabel()
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("Scope() error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Scope() unexpected error: %v", err)
			}
			if scope != test.wantScope {
				t.Fatalf("scope = %v, want %v", scope, test.wantScope)
			}
			if test.wantLabelErr != nil {
				if !errors.Is(labelErr, test.wantLabelErr) {
					t.Fatalf("ExpectedLabel() error = %v, want %v", labelErr, test.wantLabelErr)
				}
				return
			}
			if labelErr != nil {
				t.Fatalf("ExpectedLabel() unexpected error: %v", labelErr)
			}
			if label != test.wantLabel {
				t.Fatalf("label = %q, want %q", label, test.wantLabel)
			}
		})
	}
}

func TestCompliantDecryption(t *testing.T) {
	tests := []struct {
		name   string
		secret SealedSecret
		label  string
	}{
		{
			name:   "strict",
			secret: strictSecret("team-a", "db-creds"),
			label:  "team-a/db-creds",
		},
		{
			name: "namespace-wide",
			secret: withAnnotations(strictSecret("team-a", "db-creds"), map[string]string{
				NamespaceWideAnnotation: "true",
			}),
			label: "team-a",
		},
		{
			name: "cluster-wide",
			secret: withAnnotations(strictSecret("team-a", "db-creds"), map[string]string{
				ClusterWideAnnotation: "true",
			}),
			label: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := DecryptionResult{Plaintext: plaintext()}
			if err := test.secret.ValidateDecryption(test.label, result); err != nil {
				t.Fatalf("ValidateDecryption() = %v, want nil", err)
			}

			attempts := []DecryptionAttempt{{Key: "active", Label: test.label, Result: result}}
			got, err := test.secret.ValidateAttempts(attempts)
			if err != nil {
				t.Fatalf("ValidateAttempts() = %v, want nil", err)
			}
			if string(got["password"]) != "correct horse battery staple" {
				t.Fatalf("plaintext = %q", got["password"])
			}
		})
	}
}

func TestStrictRenameReplayRejected(t *testing.T) {
	original := strictSecret("team-a", "db-creds")
	attempt := DecryptionAttempt{
		Key:    "active",
		Label:  "team-a/db-creds",
		Result: DecryptionResult{Plaintext: plaintext()},
	}

	replays := []SealedSecret{
		strictSecret("team-a", "other"),
		strictSecret("team-b", "db-creds"),
	}
	for _, replay := range replays {
		_, err := replay.ValidateAttempts([]DecryptionAttempt{attempt})
		if !errors.Is(err, ErrDecryptionFailed) {
			t.Fatalf("rename replay to %s/%s: error = %v, want ErrDecryptionFailed", replay.Namespace, replay.Name, err)
		}
		if err != nil && strings.Contains(err.Error(), original.Namespace) {
			t.Fatalf("error leaked the sealed identity: %v", err)
		}
	}
}

func TestScopeAllowsOnlyItsDeclaredMobility(t *testing.T) {
	nsWide := withAnnotations(strictSecret("team-a", "db-creds"), map[string]string{
		NamespaceWideAnnotation: "true",
	})
	nsWideRenamed := strictSecret("team-a", "other")
	nsWideRenamed.Annotations = nsWide.Annotations
	if err := nsWideRenamed.ValidateDecrypted("team-a", plaintext(), nil); err != nil {
		t.Fatalf("namespace-wide rename within namespace should succeed: %v", err)
	}
	nsWideMoved := strictSecret("team-b", "db-creds")
	nsWideMoved.Annotations = nsWide.Annotations
	if err := nsWideMoved.ValidateDecrypted("team-a", plaintext(), nil); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("namespace-wide cross-namespace replay error = %v, want ErrDecryptionFailed", err)
	}

	clusterWide := withAnnotations(strictSecret("team-a", "db-creds"), map[string]string{
		ClusterWideAnnotation: "true",
	})
	clusterMoved := strictSecret("team-b", "other")
	clusterMoved.Annotations = clusterWide.Annotations
	if err := clusterMoved.ValidateDecrypted("", plaintext(), nil); err != nil {
		t.Fatalf("cluster-wide move should succeed: %v", err)
	}
}

func TestAnnotationLooseningRejected(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		usedLabel   string
	}{
		{
			name:        "strict to cluster-wide",
			annotations: map[string]string{ClusterWideAnnotation: "true"},
			usedLabel:   "team-a/db-creds",
		},
		{
			name:        "strict to namespace-wide",
			annotations: map[string]string{NamespaceWideAnnotation: "true"},
			usedLabel:   "team-a/db-creds",
		},
		{
			name:        "namespace-wide to cluster-wide",
			annotations: map[string]string{ClusterWideAnnotation: "true"},
			usedLabel:   "team-a",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := withAnnotations(strictSecret("team-a", "db-creds"), test.annotations)
			err := tampered.ValidateDecrypted(test.usedLabel, plaintext(), nil)
			if !errors.Is(err, ErrDecryptionFailed) {
				t.Fatalf("error = %v, want ErrDecryptionFailed", err)
			}
		})
	}
}

func TestUnsealWithLoosenedAnnotationCannotDecrypt(t *testing.T) {
	tampered := withAnnotations(strictSecret("team-a", "db-creds"), map[string]string{
		ClusterWideAnnotation: "true",
	})
	key := ControllerKey{
		Name: "active",
		Decrypt: func(label string, _ map[string]string) (map[string][]byte, error) {
			if label != "team-a/db-creds" {
				return nil, errors.New("ciphertext is bound to the strict label")
			}
			return plaintext(), nil
		},
	}
	if _, err := tampered.Unseal(key); err != ErrDecryptionFailed {
		t.Fatalf("Unseal() error = %v, want ErrDecryptionFailed", err)
	}
}

func TestPackageLevelValidationAcceptsPointer(t *testing.T) {
	secret := strictSecret("team-a", "db-creds")
	if err := ValidateDecryption(&secret, "team-a/db-creds", DecryptionResult{Plaintext: plaintext()}); err != nil {
		t.Fatalf("ValidateDecryption(*SealedSecret) = %v, want nil", err)
	}
	if err := ValidateDecrypted(&secret, "team-a/db-creds", plaintext(), nil); err != nil {
		t.Fatalf("ValidateDecrypted(*SealedSecret) = %v, want nil", err)
	}
}

func TestBothAnnotationsRejectedEverywhere(t *testing.T) {
	secret := withAnnotations(strictSecret("team-a", "db-creds"), map[string]string{
		ClusterWideAnnotation:   "true",
		NamespaceWideAnnotation: "true",
	})

	if _, err := secret.Scope(); !errors.Is(err, ErrConflictingScopeAnnotations) {
		t.Fatalf("Scope() = %v", err)
	}
	if _, err := secret.ExpectedLabel(); !errors.Is(err, ErrConflictingScopeAnnotations) {
		t.Fatalf("ExpectedLabel() = %v", err)
	}
	if err := secret.ValidateDecrypted("", plaintext(), nil); !errors.Is(err, ErrConflictingScopeAnnotations) {
		t.Fatalf("ValidateDecrypted() = %v", err)
	}
	if _, err := secret.ValidateAttempts([]DecryptionAttempt{
		{Label: "", Result: DecryptionResult{Plaintext: plaintext()}},
	}); !errors.Is(err, ErrConflictingScopeAnnotations) {
		t.Fatalf("ValidateAttempts() = %v", err)
	}
	if _, err := secret.Unseal(ControllerKey{
		Name: "active",
		Decrypt: func(string, map[string]string) (map[string][]byte, error) {
			return plaintext(), nil
		},
	}); !errors.Is(err, ErrConflictingScopeAnnotations) {
		t.Fatalf("Unseal() = %v", err)
	}
}

func TestKeyRotationTriesEachKeyInOrder(t *testing.T) {
	secret := strictSecret("team-a", "db-creds")
	wantLabel := "team-a/db-creds"
	var calls []string

	oldKey := ControllerKey{
		Name: "old",
		Decrypt: func(label string, encryptedData map[string]string) (map[string][]byte, error) {
			calls = append(calls, "old:"+label)
			if label != wantLabel {
				t.Fatalf("old key got label %q, want %q", label, wantLabel)
			}
			if encryptedData["password"] != "ciphertext" {
				t.Fatalf("old key got encrypted data %v", encryptedData)
			}
			return nil, errors.New("old key cannot decrypt")
		},
	}
	newKey := ControllerKey{
		Name: "new",
		Decrypt: func(label string, encryptedData map[string]string) (map[string][]byte, error) {
			calls = append(calls, "new:"+label)
			if label != wantLabel {
				t.Fatalf("new key got label %q, want %q", label, wantLabel)
			}
			return plaintext(), nil
		},
	}

	got, err := secret.Unseal(oldKey, newKey)
	if err != nil {
		t.Fatalf("Unseal() = %v, want nil", err)
	}
	if string(got["password"]) != "correct horse battery staple" {
		t.Fatalf("plaintext = %q", got["password"])
	}
	if len(calls) != 2 || calls[0] != "old:"+wantLabel || calls[1] != "new:"+wantLabel {
		t.Fatalf("key call order = %v", calls)
	}
}

func TestRotationFailuresAreIndistinguishable(t *testing.T) {
	secret := strictSecret("team-a", "db-creds")

	labelFailure := secret.ValidateDecrypted("team-b/other", plaintext(), nil)
	ciphertextFailure := secret.ValidateDecrypted("team-a/db-creds", nil, errors.New("bad ciphertext padding"))
	if !errors.Is(labelFailure, ErrDecryptionFailed) {
		t.Fatalf("label failure = %v", labelFailure)
	}
	if !errors.Is(ciphertextFailure, ErrDecryptionFailed) {
		t.Fatalf("ciphertext failure = %v", ciphertextFailure)
	}
	if labelFailure.Error() != ciphertextFailure.Error() || labelFailure != ciphertextFailure {
		t.Fatalf("failures are distinguishable: %q vs %q", labelFailure, ciphertextFailure)
	}

	keys := []ControllerKey{
		{
			Name: "old",
			Decrypt: func(string, map[string]string) (map[string][]byte, error) {
				return nil, errors.New("old key rejected ciphertext")
			},
		},
		{
			Name: "middle",
			Decrypt: func(string, map[string]string) (map[string][]byte, error) {
				return nil, errors.New("different key rejected ciphertext")
			},
		},
	}
	_, err := secret.Unseal(keys...)
	if err != ErrDecryptionFailed {
		t.Fatalf("Unseal() error = %v, want ErrDecryptionFailed", err)
	}
	for _, detail := range []string{"old key", "different key", "ciphertext", "label"} {
		if strings.Contains(err.Error(), detail) {
			t.Fatalf("Unseal() leaked %q in error: %v", detail, err)
		}
	}
}

func TestAttemptsContinueAfterMismatchAndCiphertextError(t *testing.T) {
	secret := strictSecret("team-a", "db-creds")
	attempts := []DecryptionAttempt{
		{
			Key:    "wrong-label",
			Label:  "team-b/other",
			Result: DecryptionResult{Plaintext: plaintext()},
		},
		{
			Key:    "bad-ciphertext",
			Label:  "team-a/db-creds",
			Result: DecryptionResult{Err: errors.New("bad padding")},
		},
		{
			Key:    "rotated-key",
			Label:  "team-a/db-creds",
			Result: DecryptionResult{Plaintext: plaintext()},
		},
	}
	got, err := secret.ValidateAttempts(attempts)
	if err != nil {
		t.Fatalf("ValidateAttempts() = %v, want nil", err)
	}
	if string(got["password"]) != "correct horse battery staple" {
		t.Fatalf("plaintext = %q", got["password"])
	}
}

func TestNilDecryptorIsSkipped(t *testing.T) {
	secret := strictSecret("team-a", "db-creds")
	good := ControllerKey{
		Name: "good",
		Decrypt: func(string, map[string]string) (map[string][]byte, error) {
			return plaintext(), nil
		},
	}
	got, err := secret.Unseal(ControllerKey{Name: "nil"}, good)
	if err != nil {
		t.Fatalf("Unseal() = %v, want nil", err)
	}
	if string(got["password"]) != "correct horse battery staple" {
		t.Fatalf("plaintext = %q", got["password"])
	}
}
