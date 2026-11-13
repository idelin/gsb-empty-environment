package netsec

import (
	"errors"
	"testing"
)

func TestNameValidation(t *testing.T) {
	valid := []Name{
		MustName(SessionScope("s1"), `svc\mutex`, ObjectTypeMutex),
		MustName(GlobalScope(), `svc\state\leaf`, ObjectTypeSection),
		MustName(SessionScope("s1"), "Ünïcode-名", ObjectTypeMutex),
	}
	for _, name := range valid {
		if err := name.Validate(); err != nil {
			t.Errorf("%s should be valid: %v", name, err)
		}
	}

	tests := []struct {
		name Name
		want error
	}{
		{Name{Scope: SessionScope("s1"), Text: "", Type: ObjectTypeMutex}, ErrInvalidName},
		{Name{Scope: SessionScope("s1"), Text: "a\x00b", Type: ObjectTypeMutex}, ErrNULInName},
		{Name{Scope: SessionScope("s1"), Text: `a\.\b`, Type: ObjectTypeMutex}, ErrDotSegment},
		{Name{Scope: SessionScope("s1"), Text: `a\..\b`, Type: ObjectTypeMutex}, ErrDotSegment},
		{Name{Scope: SessionScope("s1"), Text: `.`, Type: ObjectTypeMutex}, ErrDotSegment},
		{Name{Scope: SessionScope("s1"), Text: `a\b `, Type: ObjectTypeMutex}, ErrTrailingSpaceOrDot},
		{Name{Scope: SessionScope("s1"), Text: `a\b.`, Type: ObjectTypeMutex}, ErrTrailingSpaceOrDot},
		{Name{Scope: SessionScope("s1"), Text: `a\`, Type: ObjectTypeMutex}, ErrInvalidName},
		{Name{Scope: SessionScope("s1"), Text: `\a`, Type: ObjectTypeMutex}, ErrInvalidName},
		{Name{Scope: SessionScope("s1"), Text: `a\\b`, Type: ObjectTypeMutex}, ErrInvalidName},
		{Name{Scope: SessionScope("s1"), Text: `a/b`, Type: ObjectTypeMutex}, ErrInvalidName},
		{Name{Scope: Scope{}, Text: `a`, Type: ObjectTypeMutex}, ErrInvalidScope},
		{Name{Scope: Scope{Global: true, SessionID: "s1"}, Text: `a`, Type: ObjectTypeMutex}, ErrInvalidScope},
		{Name{Scope: SessionScope("s1"), Text: `a`, Type: ObjectTypeUnknown}, ErrInvalidObjectType},
	}
	for _, tc := range tests {
		err := tc.name.Validate()
		if !errors.Is(err, tc.want) {
			t.Errorf("%q: got %v, want %v", tc.name.Text, err, tc.want)
		}
	}
}

func TestCaseFoldCollisionAcrossCreates(t *testing.T) {
	ns := NewNamespace()
	first := MustName(SessionScope("s1"), `svc\gate`, ObjectTypeMutex)
	second := MustName(SessionScope("s1"), `SVC\GATE`, ObjectTypeMutex)
	creator := Creator{SID: "attacker", Integrity: IntegrityLow}

	one, err := ns.CreateIfAbsent(CreateRequest{Name: first, Creator: creator, SecurityDescriptor: "attacker"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ns.CreateIfAbsent(CreateRequest{Name: second, Creator: creator, SecurityDescriptor: "attacker"})
	if !errors.Is(err, ErrAlreadyExists) || !errors.Is(err, ErrCaseCollision) {
		t.Fatalf("case collision error = %v", err)
	}
	current, ok := ns.Lookup(first.Scope, first.Text)
	if !ok {
		t.Fatal("first object disappeared")
	}
	if current.ObjectID != one.ObjectID || current.Name.Text != first.Text {
		t.Fatalf("case variant overwrote original: %#v", current)
	}

	// Full Unicode case folding must produce the same collision behavior.
	strasse := MustName(SessionScope("s2"), "Straße", ObjectTypeSection)
	upper := MustName(SessionScope("s2"), "STRASSE", ObjectTypeSection)
	if _, err := ns.CreateIfAbsent(CreateRequest{Name: strasse, Creator: creator, SecurityDescriptor: "sd"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ns.CreateIfAbsent(CreateRequest{Name: upper, Creator: creator, SecurityDescriptor: "sd"}); !errors.Is(err, ErrCaseCollision) {
		t.Fatalf("full-fold collision = %v", err)
	}
}

func TestDigestParsing(t *testing.T) {
	d := HashDescriptor("D:(A;;GA;;;WD)")
	parsed, err := ParseDigest(d.String())
	if err != nil {
		t.Fatal(err)
	}
	if parsed != d {
		t.Fatalf("digest round trip: %v != %v", parsed, d)
	}
	if _, err := ParseDigest("not-a-digest"); !errors.Is(err, ErrInvalidDigest) {
		t.Fatalf("bad digest error = %v", err)
	}
}
