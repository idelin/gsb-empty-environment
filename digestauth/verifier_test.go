package digestauth

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func md5Hex(parts ...string) string {
	h := md5.Sum([]byte(strings.Join(parts, ":")))
	return hex.EncodeToString(h[:])
}

func TestRFC2617VerifierQOPAuth(t *testing.T) {
	const (
		user   = "alice"
		realm  = "gateway@example.org"
		passwd = "s3cret"
		nonce  = "server-nonce"
		cnonce = "client-random"
		method = "POST"
		uri    = "/api/target?x=1"
	)
	ha1 := md5Hex(user, realm, passwd)
	lookup := HA1Lookup(func(username, gotRealm string) (string, error) {
		if username != user || gotRealm != realm {
			return "", errors.New("unknown user")
		}
		return ha1, nil
	})
	verifier := NewRFC2617Verifier(lookup)

	nc, err := ParseNC("00000007")
	if err != nil {
		t.Fatalf("ParseNC: %v", err)
	}
	ha2 := md5Hex(method, uri)
	response := md5Hex(ha1, nonce, "00000007", cnonce, "auth", ha2)

	good := Evidence{
		Realm:     realm,
		Username:  user,
		Algorithm: "MD5",
		QOP:       "auth",
		Nonce:     nonce,
		CNonce:    cnonce,
		NC:        nc,
		Method:    method,
		URI:       uri,
		Response:  response,
	}
	if err := verifier.Verify(context.Background(), good); err != nil {
		t.Fatalf("valid response rejected: %v", err)
	}

	// Tamper with each security-relevant field: username/realm/method/target
	// are all part of the signed evidence.
	tamper := good
	tamper.Method = "GET"
	if err := verifier.Verify(context.Background(), tamper); err == nil {
		t.Fatal("changed method unexpectedly accepted")
	}
	tamper = good
	tamper.URI = "/api/other"
	if err := verifier.Verify(context.Background(), tamper); err == nil {
		t.Fatal("changed target unexpectedly accepted")
	}
	tamper = good
	tamper.Username = "mallory"
	if err := verifier.Verify(context.Background(), tamper); err == nil {
		t.Fatal("changed username unexpectedly accepted")
	}
	tamper = good
	tamper.Response = response[:len(response)-1] + "0"
	if err := verifier.Verify(context.Background(), tamper); err == nil {
		t.Fatal("forged response unexpectedly accepted")
	}

	if err := verifier.Verify(context.Background(),
		Evidence{Realm: realm, Username: "ghost"}); err == nil {
		t.Fatal("unknown user unexpectedly accepted")
	}
}

func TestRFC2617VerifierAuthIntBindsBodyDigest(t *testing.T) {
	const (
		user    = "bob"
		realm   = "gateway@example.org"
		passwd  = "hunter2"
		nonce   = "n-1"
		cnonce  = "c-1"
		method  = "PUT"
		uri     = "/files/a"
		bodySHA = "9e34f0b47e8e6e91d0a2d3bd0f3e0d2a"
	)
	ha1 := md5Hex(user, realm, passwd)
	lookup := HA1Lookup(func(username, gotRealm string) (string, error) {
		return ha1, nil
	})
	verifier := NewRFC2617Verifier(lookup)

	ha2 := md5Hex(method, uri, bodySHA)
	response := md5Hex(ha1, nonce, "00000001", cnonce, "auth-int", ha2)

	ev := Evidence{
		Realm:    realm,
		Username: user,
		QOP:      "auth-int",
		Nonce:    nonce,
		CNonce:   cnonce,
		NC:       1,
		Method:   method,
		URI:      uri,
		BodySHA:  bodySHA,
		Response: response,
	}
	if err := verifier.Verify(context.Background(), ev); err != nil {
		t.Fatalf("auth-int response rejected: %v", err)
	}

	ev.BodySHA = strings.Repeat("0", len(bodySHA))
	if err := verifier.Verify(context.Background(), ev); err == nil {
		t.Fatal("changed body digest unexpectedly accepted")
	}
}

func TestParseNC(t *testing.T) {
	cases := []struct {
		raw  string
		want uint32
		err  error
	}{
		{"00000001", 1, nil},
		{"ffffffff", 0xffffffff, nil},
		{"ABCDEF01", 0xabcdef01, nil},
		{"00000000", 0, ErrInvalidCount},
		{"0000001", 0, ErrInvalidCount},
		{"0000000g", 0, ErrInvalidCount},
		{"", 0, ErrInvalidCount},
	}
	for _, tc := range cases {
		got, err := ParseNC(tc.raw)
		if !errors.Is(err, tc.err) {
			t.Errorf("ParseNC(%q) err = %v, want %v", tc.raw, err, tc.err)
		}
		if err == nil && got != tc.want {
			t.Errorf("ParseNC(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestEndToEndStateWithRFCVerifier(t *testing.T) {
	st, _ := testState(t)
	policy := testPolicy()
	nonce, err := st.Issue(policy)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	ha1 := md5Hex("alice", policy.Realm, "pw")
	verifier := NewRFC2617Verifier(HA1Lookup(func(string, string) (string, error) {
		return ha1, nil
	}))

	nc, _ := ParseNC("00000001")
	ha2 := md5Hex("GET", "/api/target")
	response := md5Hex(ha1, nonce, "00000001", "cnonce-1", policy.QOP, ha2)

	req := Request{
		Policy:   policy,
		Username: "alice",
		Nonce:    nonce,
		CNonce:   "cnonce-1",
		NC:       nc,
		Method:   "GET",
		URI:      "/api/target",
		Response: response,
	}
	if err := st.Authenticate(context.Background(), verifier, req); err != nil {
		t.Fatalf("end-to-end authenticate: %v", err)
	}
	if err := st.Authenticate(context.Background(), verifier, req); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay = %v, want ErrReplay", err)
	}
}
