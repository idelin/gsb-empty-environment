package krb5policy

import (
	"reflect"
	"testing"
)

func TestParsePrincipalEscapes(t *testing.T) {
	cases := []string{
		"svc\\/name@REALM",
		"a\\@b@REALM",
		"a\\\\b@REALM",
		"service/host@REALM",
	}
	for _, in := range cases {
		p, err := ParsePrincipal(in)
		if err != nil {
			t.Fatalf("ParsePrincipal(%q): %v", in, err)
		}
		round, err := ParsePrincipal(p.String())
		if err != nil {
			t.Fatalf("reparse %q -> %q: %v", in, p.String(), err)
		}
		if !reflect.DeepEqual(round.Name, p.Name) || !sameRealm(round.Realm, p.Realm) {
			t.Fatalf("round trip %q -> %+v -> %+v", in, p, round)
		}
	}
}

func TestPrincipalCanonicalization(t *testing.T) {
	a := NewPrincipal("realm", "HTTP", "HOST")
	b := NewPrincipal("REALM", "http", "host")
	if !a.Equal(b) {
		t.Fatalf("%q != %q", a.Canonical(), b.Canonical())
	}
	tgt := NewPrincipal("ISSUER", "krbtgt", "DEST")
	if tgt.TargetRealm() != "DEST" {
		t.Fatalf("TGT TargetRealm = %q", tgt.TargetRealm())
	}
}
