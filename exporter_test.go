package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func acvpSecrets(t *testing.T) (psk, dhe, helloClientRandom, helloServerRandom, finishedServerRandom []byte,
	earlyExporterMasterSecret, exporterMasterSecret []byte) {
	t.Helper()
	psk = mustHex(t, "56288B726C73829F7A3E47B103837C8139ACF552E7530C7A710B35ED41191698")
	dhe = mustHex(t, "EFFE9EC26AA29FD750DFA6A10B944D74071595B27EE88887D5E11C84590B5CC3")
	helloClientRandom = mustHex(t, "E9137679E582BA7C1DB41CF725F86C6D09C8C05F297BAD9A65B552EAF524FDE4")
	helloServerRandom = mustHex(t, "23ECCFD030790748C8F8D8A656FD98D717F1B62AF3712F97211D2070B499F98A")
	finishedServerRandom = mustHex(t, "C750EDA6696CD101B142BD79E00E6AC8C5F2C0ABC78DD64F4D991326659E9299")
	earlyExporterMasterSecret = mustHex(t, "88E078F562CDC930219F6A5E98A1CE8C6E5F3DAC5AC516459A96F2EF8F114C66")
	exporterMasterSecret = mustHex(t, "8A43D787EE3804EAD4A2A5B32972F9896B696295645D7222E1FD081DDD939834")
	return
}

func TestACVPEarlyAndPostHandshakeExporterSecrets(t *testing.T) {
	psk, dhe, helloClientRandom, helloServerRandom, finishedServerRandom,
		wantEarly, wantPost := acvpSecrets(t)

	transcript := sha256.New()
	transcript.Write(helloClientRandom)

	es := NewEarlySecret(sha256.New, psk)
	gotEarly := es.EarlyExporterMasterSecret(transcript).Secret()
	if !bytes.Equal(gotEarly, wantEarly) {
		t.Fatalf("early_exporter_master_secret = %x, want %x", gotEarly, wantEarly)
	}

	hs := es.HandshakeSecret(dhe)
	transcript.Write(helloServerRandom)
	ms := hs.MasterSecret()
	transcript.Write(finishedServerRandom)

	gotPost := ms.ExporterMasterSecret(transcript).Secret()
	if !bytes.Equal(gotPost, wantPost) {
		t.Fatalf("exporter_master_secret = %x, want %x", gotPost, wantPost)
	}
}

func TestExporterRepeatableAndPhaseSeparated(t *testing.T) {
	psk, dhe, helloClientRandom, helloServerRandom, finishedServerRandom, _, _ := acvpSecrets(t)

	earlyTranscript := sha256.New()
	earlyTranscript.Write(helloClientRandom)
	early := NewEarlySecret(sha256.New, psk).EarlyExporterMasterSecret(earlyTranscript)

	postTranscript := sha256.New()
	postTranscript.Write(helloClientRandom)
	postTranscript.Write(helloServerRandom)
	postTranscript.Write(finishedServerRandom)
	post := NewEarlySecret(sha256.New, psk).HandshakeSecret(dhe).MasterSecret().ExporterMasterSecret(postTranscript)

	earlyKey1, err := early.Export("test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	earlyKey2, err := early.Export("test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(earlyKey1, earlyKey2) {
		t.Fatal("repeated early exports differ")
	}

	postKey1, err := post.Export("test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	postKey2, err := post.Export("test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(postKey1, postKey2) {
		t.Fatal("repeated post-handshake exports differ")
	}
	if bytes.Equal(earlyKey1, postKey1) {
		t.Fatal("early and post-handshake exports produced the same bytes")
	}
}

func TestCrossPhaseVerificationFails(t *testing.T) {
	psk, dhe, helloClientRandom, helloServerRandom, finishedServerRandom, _, _ := acvpSecrets(t)

	earlyTranscript := sha256.New()
	earlyTranscript.Write(helloClientRandom)
	early := NewEarlySecret(sha256.New, psk).EarlyExporterMasterSecret(earlyTranscript)

	postTranscript := sha256.New()
	postTranscript.Write(helloClientRandom)
	postTranscript.Write(helloServerRandom)
	postTranscript.Write(finishedServerRandom)
	post := NewEarlySecret(sha256.New, psk).HandshakeSecret(dhe).MasterSecret().ExporterMasterSecret(postTranscript)

	earlyKey, err := early.Export("test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	postKey, err := post.Export("test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}

	if post.Verify("test label", nil, earlyKey) {
		t.Fatal("post-handshake exporter verified early output")
	}
	if early.Verify("test label", nil, postKey) {
		t.Fatal("early exporter verified post-handshake output")
	}
	if err := VerifyKeyingMaterial(PhaseEarly, early, "test label", nil, postKey); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("cross-phase verification error = %v, want ErrVerificationFailed", err)
	}
}

func TestExplicitPhaseMismatchRejected(t *testing.T) {
	psk := mustHex(t, "56288B726C73829F7A3E47B103837C8139ACF552E7530C7A710B35ED41191698")
	early := NewEarlySecret(sha256.New, psk).EarlyExporterMasterSecret(sha256.New())

	if _, err := early.Export(PhasePostHandshake, "test label", nil, 32); !errors.Is(err, ErrPhaseMismatch) {
		t.Fatalf("phase mismatch error = %v, want ErrPhaseMismatch", err)
	}
	if err := VerifyKeyingMaterial(PhasePostHandshake, early, "test label", nil, make([]byte, 32)); !errors.Is(err, ErrPhaseMismatch) {
		t.Fatalf("verify phase mismatch error = %v, want ErrPhaseMismatch", err)
	}
}

func TestTopLevelExportDerivesFromBaseSecret(t *testing.T) {
	psk, _, helloClientRandom, _, _, wantEarly, _ := acvpSecrets(t)
	es := NewEarlySecret(sha256.New, psk)
	transcript := sha256.New()
	transcript.Write(helloClientRandom)

	derived, err := DeriveEarlyExporterMasterSecret(sha256.New, es, transcript)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, _, wantEarly, _ := acvpSecrets(t); !bytes.Equal(derived, wantEarly) {
		t.Fatalf("DeriveEarlyExporterMasterSecret = %x, want %x", derived, wantEarly)
	}

	want, err := es.EarlyExporterMasterSecret(transcript).Export("test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ExportKeyingMaterial(PhaseEarly, es.Secret(), transcript, "test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("top-level derived export = %x, want %x", got, want)
	}

	direct := NewEarlyExporterFromMasterSecret(wantEarly, sha256.New)
	gotDirect, err := ExportKeyingMaterial(PhaseEarly, direct, "test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	wantDirect, err := direct.Export("test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotDirect, wantDirect) {
		t.Fatalf("top-level direct export = %x, want %x", gotDirect, wantDirect)
	}
}

func TestRawSecretRequiresPhase(t *testing.T) {
	if _, err := ExportKeyingMaterial([]byte("secret"), "test label", nil, 32); !errors.Is(err, ErrInvalidPhase) {
		t.Fatalf("missing phase error = %v, want ErrInvalidPhase", err)
	}
}

func TestDualSecretExporterRequiresPhase(t *testing.T) {
	_, _, _, _, _, wantEarly, wantPost := acvpSecrets(t)
	late := NewEarlyExporterFromMasterSecret(wantEarly, sha256.New)
	post := NewPostHandshakeExporterFromMasterSecret(wantPost, sha256.New)

	earlyKey, err := late.Export("test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	postKey, err := post.Export("test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}

	dual := NewExporterPair(wantEarly, wantPost, sha256.New)
	if _, err := dual.Export("test label", nil, 32); !errors.Is(err, ErrInvalidPhase) {
		t.Fatalf("missing phase error = %v, want ErrInvalidPhase", err)
	}
	gotEarly, err := dual.Export(PhaseEarly, "test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	gotPost, err := dual.Export(PhasePostHandshake, "test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotEarly, earlyKey) || !bytes.Equal(gotPost, postKey) {
		t.Fatalf("dual exporter outputs = %x / %x, want %x / %x", gotEarly, gotPost, earlyKey, postKey)
	}
	gotTop, err := ExportKeyingMaterial(PhaseEarly, dual, "test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotTop, earlyKey) {
		t.Fatalf("top-level dual export = %x, want %x", gotTop, earlyKey)
	}
	if dual.Verify(PhaseEarly, "test label", nil, postKey) {
		t.Fatal("early phase verified post-handshake output")
	}
	if dual.Verify(PhasePostHandshake, "test label", nil, earlyKey) {
		t.Fatal("post-handshake phase verified early output")
	}

	gotPair, err := ExportWithPhaseSecrets(PhasePostHandshake, wantEarly, wantPost, "test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPair, postKey) {
		t.Fatalf("phase-secret export = %x, want %x", gotPair, postKey)
	}
	if err := VerifyWithPhaseSecrets(PhasePostHandshake, wantEarly, wantPost, "test label", nil, postKey); err != nil {
		t.Fatal(err)
	}
	if err := VerifyWithPhaseSecrets(PhaseEarly, wantEarly, wantPost, "test label", nil, postKey); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("cross-phase phase-secret verification = %v, want ErrVerificationFailed", err)
	}
}

func TestSameBaseSecretStillSeparatesPhases(t *testing.T) {
	base := mustHex(t, "56288B726C73829F7A3E47B103837C8139ACF552E7530C7A710B35ED41191698")
	transcript := mustHex(t, "E9137679E582BA7C1DB41CF725F86C6D09C8C05F297BAD9A65B552EAF524FDE4")

	early, err := ExportKeyingMaterial(PhaseEarly, base, transcript, "test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	post, err := ExportKeyingMaterial(PhasePostHandshake, base, transcript, "test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(early, post) {
		t.Fatal("phase labels did not separate exports from the same base secret")
	}
	if err := VerifyKeyingMaterial(PhaseEarly, base, transcript, "test label", nil, post); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("cross-phase verify error = %v, want ErrVerificationFailed", err)
	}
}

func TestTypedBaseSecretAndVerification(t *testing.T) {
	psk, _, helloClientRandom, _, _, _, _ := acvpSecrets(t)
	es := NewEarlySecret(sha256.New, psk)
	transcript := sha256.New()
	transcript.Write(helloClientRandom)

	want, err := es.EarlyExporterMasterSecret(transcript).Export("test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}

	bound := NewExporter(PhaseEarly, es, transcript)
	got, err := bound.Export("test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("typed-base export = %x, want %x", got, want)
	}

	gotTop, err := ExportKeyingMaterial(PhaseEarly, es, transcript, "test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotTop, want) {
		t.Fatalf("top-level typed-base export = %x, want %x", gotTop, want)
	}

	gotNamed, err := ExportFromBaseSecret(PhaseEarly, es.Secret(), transcript, "test label", 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotNamed, want) {
		t.Fatalf("ExportFromBaseSecret = %x, want %x", gotNamed, want)
	}

	if err := VerifyKeyingMaterial(PhaseEarly, es, transcript, "test label", nil, want); err != nil {
		t.Fatalf("typed-base verification failed: %v", err)
	}
	if err := VerifyKeyingMaterial(PhaseEarly, es.Secret(), transcript, "test label", nil, want); err != nil {
		t.Fatalf("raw-base verification failed: %v", err)
	}
	if err := VerifyKeyingMaterial(PhasePostHandshake, es, "test label", nil, want); !errors.Is(err, ErrPhaseMismatch) {
		t.Fatalf("cross-phase secret error = %v, want ErrPhaseMismatch", err)
	}

	tagged, err := ExportPhaseKeyingMaterial(PhaseEarly, es, transcript, "test label", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	if tagged.Phase != PhaseEarly || !bytes.Equal(tagged.Key, want) || !tagged.Verify(want) {
		t.Fatalf("phase-tagged result = phase %s key %x", tagged.Phase, tagged.Key)
	}
}

func TestLengthAndLabelLimits(t *testing.T) {
	secret := NewEarlyExporterFromMasterSecret(make([]byte, 32), sha256.New)
	if _, err := secret.Export("test label", nil, 255*sha256.Size+1); !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("length error = %v, want ErrInvalidLength", err)
	}
	longLabel := string(make([]byte, 250))
	if _, err := secret.Export(longLabel, nil, 32); !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("label error = %v, want ErrInvalidLabel", err)
	}
	context := make([]byte, 256)
	if _, err := ExpandLabel(sha256.New, []byte("secret"), "test label", context, 32); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("context error = %v, want ErrInvalidContext", err)
	}
}
