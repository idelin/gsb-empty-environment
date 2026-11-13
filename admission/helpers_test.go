package admission

import (
	"fmt"
	"testing"
	"time"
)

const (
	testFP      = "FINGERPRINT-PRIMARY-0001"
	goodIssuer  = "ISSUER-GOOD"
	goodIssuer2 = "ISSUER-GOOD-2"
	badIssuer   = "ISSUER-BAD"
)

// fakeVerifier 模拟密码学验证：Raw 以 "forged:" 开头视为验证失败，
// 其余视为有效；同时记录实际回调次数以校验预算控制。
type fakeVerifier struct {
	calls int
}

func (f *fakeVerifier) verify(pk *PublicKeyPacket, uid UserID, sig Signature) bool {
	f.calls++
	return len(sig.Raw) >= 0 && !isForged(sig)
}

func isForged(sig Signature) bool {
	return string(sig.Raw[:min(7, len(sig.Raw))]) == "forged:"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func baseTime() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

func selfSig(index int, forged bool, created time.Time) Signature {
	raw := fmt.Sprintf("self-%d", index)
	if forged {
		raw = "forged:self-" + fmt.Sprint(index)
	}
	return Signature{Raw: []byte(raw), Issuer: testFP, Created: created, Self: true}
}

func thirdSig(issuer string, index int, created time.Time) Signature {
	raw := fmt.Sprintf("sig-%s-%d", issuer, index)
	return Signature{Raw: []byte(raw), Issuer: issuer, Created: created, Self: false}
}

func uidWith(name string, created time.Time, sigs ...Signature) UserID {
	return UserID{Raw: []byte("uid-" + name), Created: created, Signatures: sigs}
}

func packetWith(uids ...UserID) *PublicKeyPacket {
	return &PublicKeyPacket{
		Fingerprint: testFP,
		PrimaryRaw:  []byte("primary-key"),
		UserIDs:     uids,
	}
}

func countThird(uid UserID) int {
	n := 0
	for _, sig := range uid.Signatures {
		if !sig.Self {
			n++
		}
	}
	return n
}

func hasDigest(set map[string]struct{}, sig Signature) bool {
	_, ok := set[Digest(sig)]
	return ok
}

func findUID(pk *PublicKeyPacket, name string) *UserID {
	want := "uid-" + name
	for i := range pk.UserIDs {
		if string(pk.UserIDs[i].Raw) == want {
			return &pk.UserIDs[i]
		}
	}
	return nil
}

func assertf(t *testing.T, cond bool, format string, args ...any) {
	t.Helper()
	if !cond {
		t.Fatalf(format, args...)
	}
}
