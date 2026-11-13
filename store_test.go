package netsec

import (
	"errors"
	"testing"
	"time"
)

const (
	testFrom   = "Alice <alice@example.org>"
	testSender = "alice@example.org"
)

func testStore(t *testing.T) *CapabilityStore {
	t.Helper()
	store, err := NewCapabilityStore(Config{
		AllowedAlgorithms: []Algorithm{
			AES256GCM, AES192GCM, AES128GCM,
			AES256CBC, AES192CBC, AES128CBC,
		},
		LocalPreferences: []Algorithm{
			AES256GCM, AES192GCM, AES128GCM,
			AES256CBC, AES192CBC, AES128CBC,
		},
		Baseline: AES256GCM,
	})
	if err != nil {
		t.Fatalf("NewCapabilityStore: %v", err)
	}
	return store
}

func recordAt(t *testing.T, store *CapabilityStore, verified bool, signer string, signingTime time.Time, algorithms ...Algorithm) RecordResult {
	t.Helper()
	result, err := store.Record(SignedMessage{
		From:              testFrom,
		SignatureVerified: verified,
		SignerEmail:       signer,
		SigningTime:       signingTime,
		SMIMECapabilities: mustCapabilities(t, algorithms...),
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	return result
}

func requireSelection(t *testing.T, store *CapabilityStore, want Algorithm) {
	t.Helper()
	got, err := store.Select(testSender)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got != want {
		t.Fatalf("Select = %q, want %q", got, want)
	}
}

func TestUnsignedMessageCannotSeedCapabilities(t *testing.T) {
	store := testStore(t)
	result := recordAt(t, store, false, testSender, time.Unix(100, 0), AES128GCM)
	if result.Status != RecordStatusIgnoredUnverified {
		t.Fatalf("status = %q, want %q", result.Status, RecordStatusIgnoredUnverified)
	}
	if _, _, ok := store.Capabilities(testSender); ok {
		t.Fatal("unsigned message created a capability record")
	}
	requireSelection(t, store, AES256GCM)
}

func TestSignerCertificateMustMatchFrom(t *testing.T) {
	store := testStore(t)
	result := recordAt(t, store, true, "mallory@example.org", time.Unix(100, 0), AES128GCM)
	if result.Status != RecordStatusIgnoredFromMismatch {
		t.Fatalf("status = %q, want %q", result.Status, RecordStatusIgnoredFromMismatch)
	}
	if _, _, ok := store.Capabilities(testSender); ok {
		t.Fatal("mismatched signer created a capability record")
	}
	requireSelection(t, store, AES256GCM)
}

func TestSignerAndFromMatchingIsCaseInsensitive(t *testing.T) {
	store := testStore(t)
	message := SignedMessage{
		From:              "Alice <ALICE@EXAMPLE.ORG>",
		SignatureVerified: true,
		SignerEmail:       "alice@Example.Org",
		SigningTime:       time.Unix(100, 0),
		SMIMECapabilities: mustCapabilities(t, AES128GCM),
	}
	result, err := store.Record(message)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if result.Status != RecordStatusRecorded {
		t.Fatalf("status = %q, want recorded", result.Status)
	}
}

func TestOldAndEqualSigningTimeReplayIsIgnored(t *testing.T) {
	store := testStore(t)
	seedTime := time.Unix(200, 0)
	if result := recordAt(t, store, true, testSender, seedTime, AES256GCM); result.Status != RecordStatusRecorded {
		t.Fatalf("seed status = %q", result.Status)
	}

	for _, replayTime := range []time.Time{seedTime.Add(-time.Hour), seedTime} {
		result := recordAt(t, store, true, testSender, replayTime, AES128CBC)
		if result.Status != RecordStatusIgnoredStaleTime {
			t.Fatalf("replay at %s: status = %q, want stale", replayTime, result.Status)
		}
	}

	capabilities, recordedAt, ok := store.Capabilities(testSender)
	if !ok || !recordedAt.Equal(seedTime) {
		t.Fatalf("record changed: ok=%v time=%s", ok, recordedAt)
	}
	if len(capabilities) != 1 || capabilities[0] != AES256GCM {
		t.Fatalf("capabilities changed: %v", capabilities)
	}
	requireSelection(t, store, AES256GCM)
}

func TestWeakOnlyCapabilitiesAreIgnoredWithoutDowngrade(t *testing.T) {
	store := testStore(t)
	weakOnly := []Algorithm{TripleDESCBC, RC2CBC, RC4, DESCBC}

	result := recordAt(t, store, true, testSender, time.Unix(100, 0), weakOnly...)
	if result.Status != RecordStatusIgnoredNoAllowedAlg {
		t.Fatalf("weak-only status = %q", result.Status)
	}
	if _, _, ok := store.Capabilities(testSender); ok {
		t.Fatal("weak-only message created a capability record")
	}
	requireSelection(t, store, AES256GCM)

	if result := recordAt(t, store, true, testSender, time.Unix(200, 0), AES256GCM); result.Status != RecordStatusRecorded {
		t.Fatalf("strong seed status = %q", result.Status)
	}
	weakReplay := recordAt(t, store, true, testSender, time.Unix(300, 0), weakOnly...)
	if weakReplay.Status != RecordStatusIgnoredNoAllowedAlg {
		t.Fatalf("newer weak-only status = %q", weakReplay.Status)
	}
	requireSelection(t, store, AES256GCM)
}

func TestSelectionUsesEarliestLocalPreferenceInIntersection(t *testing.T) {
	store, err := NewCapabilityStore(Config{
		AllowedAlgorithms: []Algorithm{AES256GCM, AES128GCM, AES256CBC, AES128CBC},
		LocalPreferences:  []Algorithm{AES256CBC, AES256GCM, AES128CBC, AES128GCM},
		Baseline:          AES256GCM,
	})
	if err != nil {
		t.Fatalf("NewCapabilityStore: %v", err)
	}
	result := recordAt(t, store, true, testSender, time.Unix(100, 0), AES128GCM, AES256GCM)
	if result.Status != RecordStatusRecorded {
		t.Fatalf("status = %q, want recorded", result.Status)
	}
	requireSelection(t, store, AES256GCM)
}

func TestEmptyIntersectionFailsInsteadOfUsingBaseline(t *testing.T) {
	store, err := NewCapabilityStore(Config{
		AllowedAlgorithms: []Algorithm{AES256GCM, AES128CBC},
		LocalPreferences:  []Algorithm{AES256GCM},
		Baseline:          AES256GCM,
	})
	if err != nil {
		t.Fatalf("NewCapabilityStore: %v", err)
	}
	result := recordAt(t, store, true, testSender, time.Unix(100, 0), AES128CBC)
	if result.Status != RecordStatusRecorded {
		t.Fatalf("status = %q, want recorded", result.Status)
	}

	got, err := store.Select(testSender)
	if !errors.Is(err, ErrNoCompatibleAlgorithm) {
		t.Fatalf("Select = %q, err = %v; want incompatible error", got, err)
	}
	if got == store.baseline {
		t.Fatal("empty intersection fell back to the baseline")
	}
}

func TestNoRecordUsesBaselineAndNonWhitelistedPreferencesAreFiltered(t *testing.T) {
	store, err := NewCapabilityStore(Config{
		AllowedAlgorithms: []Algorithm{AES256GCM, AES128GCM},
		LocalPreferences:  []Algorithm{RC2CBC, AES256GCM, AES128GCM},
		Baseline:          AES256GCM,
	})
	if err != nil {
		t.Fatalf("NewCapabilityStore: %v", err)
	}
	requireSelection(t, store, AES256GCM)

	recordAt(t, store, true, testSender, time.Unix(100, 0), AES128GCM)
	requireSelection(t, store, AES128GCM)
}

func TestBaselineMustBeWhitelisted(t *testing.T) {
	_, err := NewCapabilityStore(Config{
		AllowedAlgorithms: []Algorithm{AES256GCM},
		LocalPreferences:  []Algorithm{AES256GCM},
		Baseline:          RC2CBC,
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestRecordReplacesNewerRecordWithFilteredAlgorithms(t *testing.T) {
	store := testStore(t)
	recordAt(t, store, true, testSender, time.Unix(100, 0), AES256GCM)
	result := recordAt(t, store, true, testSender, time.Unix(200, 0), AES128GCM, RC2CBC)
	if result.Status != RecordStatusRecorded {
		t.Fatalf("status = %q, want recorded", result.Status)
	}
	if len(result.Algorithms) != 1 || result.Algorithms[0] != AES128GCM {
		t.Fatalf("filtered algorithms = %v", result.Algorithms)
	}
	requireSelection(t, store, AES128GCM)
}

func TestRecordAcceptsAlreadyParsedCapabilities(t *testing.T) {
	store := testStore(t)
	message := SignedMessage{
		From:              testFrom,
		SignatureVerified: true,
		SignerEmail:       testSender,
		SigningTime:       time.Unix(100, 0),
		Capabilities:      []Algorithm{AES256GCM, RC2CBC},
	}
	result, err := store.Record(message)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if result.Status != RecordStatusRecorded {
		t.Fatalf("status = %q", result.Status)
	}
	requireSelection(t, store, AES256GCM)
}

func TestRecordRejectsAmbiguousCapabilityInputs(t *testing.T) {
	store := testStore(t)
	message := SignedMessage{
		From:              testFrom,
		SignatureVerified: true,
		SignerEmail:       testSender,
		SigningTime:       time.Unix(100, 0),
		SMIMECapabilities: mustCapabilities(t, AES256GCM),
		Capabilities:      []Algorithm{AES128GCM},
	}
	if _, err := store.Record(message); !errors.Is(err, ErrInvalidCapabilities) {
		t.Fatalf("error = %v, want ErrInvalidCapabilities", err)
	}
}

func TestMalformedCapabilitiesDoNotChangeRecord(t *testing.T) {
	store := testStore(t)
	recordAt(t, store, true, testSender, time.Unix(100, 0), AES256GCM)

	message := SignedMessage{
		From:              testFrom,
		SignatureVerified: true,
		SignerEmail:       testSender,
		SigningTime:       time.Unix(200, 0),
		SMIMECapabilities: []byte{0x30, 0x01},
	}
	if _, err := store.Record(message); !errors.Is(err, ErrInvalidCapabilities) {
		t.Fatalf("error = %v, want ErrInvalidCapabilities", err)
	}
	requireSelection(t, store, AES256GCM)
}

func TestStaleMessageIsIgnoredBeforeParsing(t *testing.T) {
	store := testStore(t)
	recordAt(t, store, true, testSender, time.Unix(200, 0), AES256GCM)
	message := SignedMessage{
		From:              testFrom,
		SignatureVerified: true,
		SignerEmail:       testSender,
		SigningTime:       time.Unix(100, 0),
		SMIMECapabilities: []byte{0x30, 0x01},
	}
	result, err := store.Record(message)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if result.Status != RecordStatusIgnoredStaleTime {
		t.Fatalf("status = %q, want stale", result.Status)
	}
	requireSelection(t, store, AES256GCM)
}

func TestNewerUnusableCapabilitiesStillAdvanceReplayWindow(t *testing.T) {
	store := testStore(t)
	recordAt(t, store, true, testSender, time.Unix(100, 0), AES256GCM)

	result := recordAt(t, store, true, testSender, time.Unix(300, 0), RC2CBC)
	if result.Status != RecordStatusIgnoredNoAllowedAlg {
		t.Fatalf("newer weak-only status = %q", result.Status)
	}

	replay := recordAt(t, store, true, testSender, time.Unix(200, 0), AES128CBC)
	if replay.Status != RecordStatusIgnoredStaleTime {
		t.Fatalf("older replay status = %q, want stale", replay.Status)
	}
	requireSelection(t, store, AES256GCM)
}

func TestNewerMessageWithoutCapabilitiesAdvancesReplayWindow(t *testing.T) {
	store := testStore(t)
	recordAt(t, store, true, testSender, time.Unix(100, 0), AES256GCM)

	message := SignedMessage{
		From:              testFrom,
		SignatureVerified: true,
		SignerEmail:       testSender,
		SigningTime:       time.Unix(300, 0),
	}
	result, err := store.Record(message)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if result.Status != RecordStatusIgnoredNoCapabilities {
		t.Fatalf("status = %q", result.Status)
	}

	replay := recordAt(t, store, true, testSender, time.Unix(200, 0), AES128CBC)
	if replay.Status != RecordStatusIgnoredStaleTime {
		t.Fatalf("older replay status = %q, want stale", replay.Status)
	}
	requireSelection(t, store, AES256GCM)
}

func TestInvalidFromOrSignerAddressIsRejected(t *testing.T) {
	store := testStore(t)
	message := SignedMessage{
		From:              "Alice <alice@example.org>, Bob <bob@example.org>",
		SignatureVerified: true,
		SignerEmail:       testSender,
		SigningTime:       time.Unix(100, 0),
		SMIMECapabilities: mustCapabilities(t, AES256GCM),
	}
	if _, err := store.Record(message); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("error = %v, want ErrInvalidAddress", err)
	}
}

func TestUnknownOIDCanBeExplicitlyWhitelisted(t *testing.T) {
	const futureOID = Algorithm("1.2.3.4.5")
	store, err := NewCapabilityStore(Config{
		AllowedAlgorithms: []Algorithm{futureOID},
		LocalPreferences:  []Algorithm{futureOID},
		Baseline:          futureOID,
	})
	if err != nil {
		t.Fatalf("NewCapabilityStore: %v", err)
	}
	recordAt(t, store, true, testSender, time.Unix(100, 0), futureOID)
	requireSelection(t, store, futureOID)
}

func TestWeakClassifier(t *testing.T) {
	for _, algorithm := range []Algorithm{TripleDESCBC, RC2CBC, RC4, RC5CBC, DESCBC} {
		if !algorithm.IsWeak() {
			t.Errorf("%s not classified weak", algorithm)
		}
	}
	for _, algorithm := range []Algorithm{AES128GCM, AES256GCM, AES256CBC} {
		if algorithm.IsWeak() {
			t.Errorf("%s incorrectly classified weak", algorithm)
		}
	}
}

func TestAlgorithmNameAliases(t *testing.T) {
	aliases := []Algorithm{"AES-256-GCM", "aes_256_gcm", "Triple-DES", "DES-EDE3-CBC"}
	wants := []Algorithm{AES256GCM, AES256GCM, TripleDESCBC, TripleDESCBC}
	for i, alias := range aliases {
		normalized, err := normalizeAlgorithm(alias)
		if err != nil {
			t.Fatalf("normalize %q: %v", alias, err)
		}
		if normalized != wants[i] {
			t.Errorf("normalize %q = %q, want %q", alias, normalized, wants[i])
		}
	}
}
