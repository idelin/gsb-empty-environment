package krb5policy

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"
)

func keyFor(principal string, kvno int) Key {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", principal, kvno)))
	return Key{Principal: principal, Version: kvno, Bytes: sum[:]}
}

func baseTicket(now time.Time) *Ticket {
	user := NewPrincipal("CLIENT.ORG", "user")
	service := NewPrincipal("CORE.ORG", "http", "app")
	return &Ticket{
		Client:      user,
		Server:      service,
		IssuerRealm: "CORE.ORG",
		Flags:       FlagForwardable | FlagRenewable,
		AuthTime:    now.Add(-time.Hour),
		StartTime:   now.Add(-time.Hour),
		EndTime:     now.Add(time.Hour),
		RenewTill:   now.Add(5 * time.Hour),
		Transited:   NewTransitedEncoding([]byte("EDGE.ORG")),
		PAC: &PAC{
			User:      user,
			Groups:    []string{"group-a", "group-b"},
			LogonTime: now.Add(-2 * time.Hour),
		},
	}
}

func sealBase(t *testing.T, ticket *Ticket, pacKey Key) {
	t.Helper()
	if err := SealTicket(ticket, keyFor(ticket.Server.String(), 1)); err != nil {
		t.Fatal(err)
	}
	if ticket.PAC != nil {
		if err := SealPAC(ticket.PAC, pacKey); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTicketHMACAndTampering(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	ticket := baseTicket(now)
	pacKey := keyFor("krbtgt/CORE.ORG@CORE.ORG", 2)
	sealBase(t, ticket, pacKey)
	kt, err := NewKeytab(keyFor(ticket.Server.String(), 1), pacKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyTicketIntegrity(ticket, kt, true); err != nil {
		t.Fatalf("integrity: %v", err)
	}
	if err := VerifyPAC(ticket.PAC, kt, []string{pacKey.Principal}); err != nil {
		t.Fatalf("PAC: %v", err)
	}
	if len(ticket.Seal.Value) != sha256.Size {
		t.Fatalf("MAC length = %d", len(ticket.Seal.Value))
	}

	// Confirm this is keyed HMAC-SHA256 rather than a plain digest.
	direct := hmac.New(sha256.New, keyFor(ticket.Server.String(), 1).Bytes)
	direct.Write([]byte("krb5policy/ticket/v1\x00"))
	direct.Write(canonicalTicketBytes(ticket))
	if !hmac.Equal(direct.Sum(nil), ticket.Seal.Value) {
		t.Fatal("ticket seal is not the expected HMAC-SHA256")
	}

	tampered := *ticket
	tampered.EndTime = tampered.EndTime.Add(time.Second)
	if err := VerifyTicketIntegrity(&tampered, kt, true); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("endtime tamper: %v", err)
	}
	tampered = *ticket
	tampered.Flags |= FlagProxiable
	if err := VerifyTicketIntegrity(&tampered, kt, true); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("flags tamper: %v", err)
	}
	tampered = *ticket
	tampered.Client.Name = []string{"admin"}
	if err := VerifyTicketIntegrity(&tampered, kt, true); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("client tamper: %v", err)
	}
	tampered = *ticket
	tampered.Transited.Contents = []byte("OTHER.ORG")
	if err := VerifyTicketIntegrity(&tampered, kt, true); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("transited tamper: %v", err)
	}
}

func TestTicketKeyBinding(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	ticket := baseTicket(now)
	serverKey := keyFor(ticket.Server.String(), 1)
	otherKey := keyFor("other@CORE.ORG", 1)
	if err := SealTicket(ticket, otherKey); err != nil {
		t.Fatal(err)
	}
	if err := SealPAC(ticket.PAC, keyFor("krbtgt/CORE.ORG@CORE.ORG", 1)); err != nil {
		t.Fatal(err)
	}
	kt, err := NewKeytab(serverKey, otherKey, keyFor("krbtgt/CORE.ORG@CORE.ORG", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyTicketMAC(ticket, kt); err != nil {
		t.Fatalf("raw MAC should verify: %v", err)
	}
	if err := VerifyTicketIntegrity(ticket, kt, true); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("strict key binding: %v", err)
	}
	if err := VerifyTicketIntegrity(ticket, kt, false); err != nil {
		t.Fatalf("lenient binding: %v", err)
	}
}

func TestWrongKeyVersion(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	ticket := baseTicket(now)
	k1 := keyFor(ticket.Server.String(), 1)
	k2 := keyFor(ticket.Server.String(), 2)
	if err := SealTicket(ticket, k1); err != nil {
		t.Fatal(err)
	}
	if err := SealPAC(ticket.PAC, keyFor("krbtgt/CORE.ORG@CORE.ORG", 1)); err != nil {
		t.Fatal(err)
	}
	kt, err := NewKeytab(k1, k2, keyFor("krbtgt/CORE.ORG@CORE.ORG", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyTicketIntegrity(ticket, kt, true); err != nil {
		t.Fatalf("kvno 1: %v", err)
	}
	ticket.Seal.KeyVersion = 2
	if err := VerifyTicketIntegrity(ticket, kt, true); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("wrong kvno: %v", err)
	}
}

func TestPACIntegrityAndMultipleSignatures(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	pac := &PAC{
		User:      NewPrincipal("CLIENT.ORG", "user"),
		UserFlags: 0,
		Groups:    []string{"g1", "g2"},
		LogonTime: now.Add(-time.Hour),
	}
	kdc := keyFor("krbtgt/CORE.ORG@CORE.ORG", 1)
	svc := keyFor("http/app@CORE.ORG", 1)
	if err := SealPAC(pac, kdc); err != nil {
		t.Fatal(err)
	}
	if err := SealPAC(pac, svc); err != nil {
		t.Fatal(err)
	}
	kt, err := NewKeytab(kdc, svc)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPAC(pac, kt, []string{kdc.Principal, svc.Principal}); err != nil {
		t.Fatalf("two valid signatures: %v", err)
	}
	if err := VerifyPAC(pac, kt, nil); err != nil {
		t.Fatalf("any signer: %v", err)
	}
	if err := VerifyPAC(pac, kt, []string{"unrelated@CORE.ORG"}); !errors.Is(err, ErrPACIntegrity) {
		t.Fatalf("untrusted signer list should fail: %v", err)
	}

	pac.Groups[0] = "admin"
	if err := VerifyPAC(pac, kt, []string{kdc.Principal, svc.Principal}); !errors.Is(err, ErrPACIntegrity) {
		t.Fatalf("PAC tamper: %v", err)
	}

	pac.Groups[0] = "g1"
	pac.UserFlags = AccountNotDelegated
	if err := VerifyPAC(pac, kt, []string{kdc.Principal, svc.Principal}); !errors.Is(err, ErrPACIntegrity) {
		t.Fatalf("PAC flags tamper: %v", err)
	}
	pac.UserFlags = 0

	// A trusted signature that is invalid must poison the PAC even if another
	// signature still verifies.
	bad := *pac
	bad.Signatures = append([]PACSignature(nil), pac.Signatures...)
	bad.Signatures[1].Value = bytes.Repeat([]byte{0}, sha256.Size)
	if err := VerifyPAC(&bad, kt, []string{kdc.Principal, svc.Principal}); !errors.Is(err, ErrPACIntegrity) {
		t.Fatalf("bad trusted signature: %v", err)
	}
}

func TestPACNoSignaturesAndReplace(t *testing.T) {
	pac := &PAC{User: NewPrincipal("CORE.ORG", "user")}
	kt, _ := NewKeytab(keyFor("krbtgt/CORE.ORG@CORE.ORG", 1))
	if err := VerifyPAC(pac, kt, nil); !errors.Is(err, ErrPACIntegrity) {
		t.Fatalf("no signatures: %v", err)
	}
	k := keyFor("krbtgt/CORE.ORG@CORE.ORG", 1)
	if err := SealPAC(pac, k); err != nil {
		t.Fatal(err)
	}
	if err := ReplacePACSignature(pac, k); err != nil {
		t.Fatal(err)
	}
	if len(pac.Signatures) != 1 {
		t.Fatalf("replace appended a duplicate: %d", len(pac.Signatures))
	}
	if err := VerifyPAC(pac, kt, []string{k.Principal}); err != nil {
		t.Fatal(err)
	}
}

func TestKeytabCopyAndValidation(t *testing.T) {
	k := Key{Principal: "svc@REALM", Version: 1, Bytes: []byte("0123456789abcdef")}
	kt, err := NewKeytab(k)
	if err != nil {
		t.Fatal(err)
	}
	got, err := kt.Lookup("SVC@realm", 1)
	if err != nil {
		t.Fatal(err)
	}
	k.Bytes[0] = 'X'
	if got[0] != '0' {
		t.Fatal("lookup returned mutable key storage")
	}
	if _, err := NewKeytab(k, k); err == nil {
		t.Fatal("duplicate key accepted")
	}
	if _, err := NewKeytab(Key{Principal: "x", Bytes: nil}); err == nil {
		t.Fatal("empty key accepted")
	}
	v2 := keyFor("svc@REALM", 2)
	if err := kt.Add(v2); err != nil {
		t.Fatal(err)
	}
	got, err = kt.Lookup("svc@realm", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, v2.Bytes) {
		t.Fatal("kvno 0 did not select the highest version")
	}
}

func TestMACAlgorithmRejected(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	ticket := baseTicket(now)
	sealBase(t, ticket, keyFor("krbtgt/CORE.ORG@CORE.ORG", 1))
	ticket.Seal.Algorithm = "HMAC-SHA1"
	kt, _ := NewKeytab(keyFor(ticket.Server.String(), 1), keyFor("krbtgt/CORE.ORG@CORE.ORG", 1))
	if err := VerifyTicketIntegrity(ticket, kt, true); !errors.Is(err, ErrUnsupportedMAC) {
		t.Fatalf("algorithm: %v", err)
	}
}

func TestSealKnownVectorShape(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	ticket := baseTicket(now)
	sealBase(t, ticket, keyFor("krbtgt/CORE.ORG@CORE.ORG", 1))
	if len(ticket.Seal.Value) != 32 {
		t.Fatal("not a SHA-256 sized HMAC")
	}
	// Hex conversion is only to keep failures printable and to prove the value
	// is stable across repeated canonicalization.
	first := hex.EncodeToString(ticket.Seal.Value)
	if err := SealTicket(ticket, keyFor(ticket.Server.String(), 1)); err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(ticket.Seal.Value) != first {
		t.Fatal("seal is not stable")
	}
}
