package netsec

import (
	"errors"
	"testing"
)

const (
	groupX25519    NamedGroup = 0x001d
	groupSECP256R1 NamedGroup = 0x0017
	groupSECP384R1 NamedGroup = 0x0018
	groupFFDHE2048 NamedGroup = 0x0100
)

func firstClientHello() *ClientHello {
	return &ClientHello{
		SupportedGroups: []NamedGroup{groupX25519, groupSECP256R1, groupSECP384R1},
		KeyShares: []KeyShareEntry{
			{Group: groupX25519, Data: []byte("x25519-public-key")},
		},
	}
}

// The server selects a group that never appeared in the first ClientHello.
// The retry must be rejected before any handshake progress is made.
func TestServerSelectsGroupNotInFirstClientHello(t *testing.T) {
	ch1 := firstClientHello()

	err := ValidateHelloRetryRequest(ch1, groupFFDHE2048)
	if !errors.Is(err, ErrGroupNotOffered) {
		t.Fatalf("expected ErrGroupNotOffered, got %v", err)
	}

	ch2 := &ClientHello{
		SupportedGroups: append([]NamedGroup{groupFFDHE2048}, ch1.SupportedGroups...),
		KeyShares:       BuildRetryKeyShare(groupFFDHE2048, []byte("ffdhe-share")),
	}
	if err := AuthorizeHandshake(ch1, ch2, groupFFDHE2048); !errors.Is(err, ErrGroupNotOffered) {
		t.Fatalf("handshake must not be authorized, got %v", err)
	}
}

// A compliant retry: selected group was offered, key_share replaces only
// that group. The handshake is allowed to proceed.
func TestCompliantRetryIsAuthorized(t *testing.T) {
	ch1 := firstClientHello()
	ch2 := &ClientHello{
		SupportedGroups: ch1.SupportedGroups,
		KeyShares:       BuildRetryKeyShare(groupSECP256R1, []byte("p256-public-key")),
	}
	if err := AuthorizeHandshake(ch1, ch2, groupSECP256R1); err != nil {
		t.Fatalf("expected handshake to be authorized, got %v", err)
	}
}

func TestRetryKeyShareKeepsExtraGroups(t *testing.T) {
	ch1 := firstClientHello()
	ch2 := &ClientHello{
		SupportedGroups: ch1.SupportedGroups,
		KeyShares: []KeyShareEntry{
			{Group: groupSECP256R1, Data: []byte("p256-public-key")},
			{Group: groupX25519, Data: []byte("x25519-public-key")},
		},
	}
	if err := ValidateRetryClientHello(ch1, ch2, groupSECP256R1); !errors.Is(err, ErrKeyShareNotReplaced) {
		t.Fatalf("expected ErrKeyShareNotReplaced, got %v", err)
	}
}

func TestRetryKeyShareForWrongGroup(t *testing.T) {
	ch1 := firstClientHello()
	ch2 := &ClientHello{
		SupportedGroups: ch1.SupportedGroups,
		KeyShares:       BuildRetryKeyShare(groupX25519, []byte("x25519-public-key")),
	}
	if err := ValidateRetryClientHello(ch1, ch2, groupSECP256R1); !errors.Is(err, ErrKeyShareNotReplaced) {
		t.Fatalf("expected ErrKeyShareNotReplaced, got %v", err)
	}
}

func TestRetryKeyShareEmpty(t *testing.T) {
	ch1 := firstClientHello()
	ch2 := &ClientHello{
		SupportedGroups: ch1.SupportedGroups,
		KeyShares:       BuildRetryKeyShare(groupSECP256R1, nil),
	}
	if err := ValidateRetryClientHello(ch1, ch2, groupSECP256R1); !errors.Is(err, ErrKeyShareNotReplaced) {
		t.Fatalf("expected ErrKeyShareNotReplaced, got %v", err)
	}
}

func TestRetryChangesSupportedGroups(t *testing.T) {
	ch1 := firstClientHello()
	ch2 := &ClientHello{
		SupportedGroups: []NamedGroup{groupSECP256R1},
		KeyShares:       BuildRetryKeyShare(groupSECP256R1, []byte("p256-public-key")),
	}
	if err := ValidateRetryClientHello(ch1, ch2, groupSECP256R1); !errors.Is(err, ErrSupportedGroupsChanged) {
		t.Fatalf("expected ErrSupportedGroupsChanged, got %v", err)
	}
}

func TestSelectedGroupOffered(t *testing.T) {
	for _, g := range []NamedGroup{groupX25519, groupSECP256R1, groupSECP384R1} {
		if err := ValidateHelloRetryRequest(firstClientHello(), g); err != nil {
			t.Fatalf("group 0x%04x was offered but rejected: %v", uint16(g), err)
		}
	}
}
