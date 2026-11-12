package netsec

import (
	"errors"
	"testing"
)

func strongPolicy() Policy {
	return Policy{
		Hashes:      []HashID{HashSHA512, HashSHA256},
		DHGroups:    []DHGroupID{DHGroup2048, DHGroup1024},
		AllowNullDH: false,
	}
}

func TestPolicyValidate(t *testing.T) {
	good := strongPolicy()
	if err := good.Validate(); err != nil {
		t.Fatalf("expected valid policy, got %v", err)
	}

	cases := map[string]Policy{
		"empty hashes":         {Hashes: nil, DHGroups: []DHGroupID{DHGroup2048}},
		"no groups no null":    {Hashes: []HashID{HashSHA256}},
		"unsorted hashes":      {Hashes: []HashID{HashSHA256, HashSHA512}, DHGroups: []DHGroupID{DHGroup2048}},
		"unsorted groups":      {Hashes: []HashID{HashSHA256}, DHGroups: []DHGroupID{DHGroup1024, DHGroup2048}},
		"md5 forbidden":        {Hashes: []HashID{HashMD5}, DHGroups: []DHGroupID{DHGroup2048}},
		"null in group list":   {Hashes: []HashID{HashSHA256}, DHGroups: []DHGroupID{DHGroupNull}},
		"unknown group id":     {Hashes: []HashID{HashSHA256}, DHGroups: []DHGroupID{DHGroupID(0x77)}},
		"unprovisioned group":  {Hashes: []HashID{HashSHA256}, DHGroups: []DHGroupID{DHGroup3072}},
		"duplicate hash":       {Hashes: []HashID{HashSHA256, HashSHA256}, DHGroups: []DHGroupID{DHGroup2048}},
		"null-only needs flag": {Hashes: []HashID{HashSHA256}, AllowNullDH: false},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(p.Validate(), ErrInvalidPolicy) {
				t.Fatalf("expected ErrInvalidPolicy, got %v", p.Validate())
			}
		})
	}

	nullOnly := Policy{Hashes: []HashID{HashSHA256}, AllowNullDH: true}
	if err := nullOnly.Validate(); err != nil {
		t.Fatalf("null-only policy with explicit flag should validate: %v", err)
	}
}

func TestNegotiateHashDowngrade(t *testing.T) {
	policy := strongPolicy()

	// Attacker strips strong offers and only leaves the weakest configured
	// hash; the strongest surviving mutually supported hash is chosen.
	got, err := NegotiateHash(policy, []HashID{HashSHA1, HashSHA256})
	if err != nil || got != HashSHA256 {
		t.Fatalf("expected SHA-256, got %v (%v)", got, err)
	}

	// Preference follows local policy order even when peer prefers weak first.
	got, err = NegotiateHash(policy, []HashID{HashSHA256, HashSHA512})
	if err != nil || got != HashSHA512 {
		t.Fatalf("expected SHA-512 preference, got %v (%v)", got, err)
	}

	// Offered-only weak/unknown hashes must never be accepted below the floor.
	_, err = NegotiateHash(policy, []HashID{HashMD5, HashSHA1, HashID(0x66)})
	if !errors.Is(err, ErrNoCommonHash) {
		t.Fatalf("expected ErrNoCommonHash, got %v", err)
	}
}

func TestNegotiateDHGroupDowngrade(t *testing.T) {
	policy := strongPolicy()

	got, err := NegotiateDHGroup(policy, []DHGroupID{DHGroup1024, DHGroup2048})
	if err != nil || got != DHGroup2048 {
		t.Fatalf("expected MODP-2048 preference, got %v (%v)", got, err)
	}

	got, err = NegotiateDHGroup(policy, []DHGroupID{DHGroup1024})
	if err != nil || got != DHGroup1024 {
		t.Fatalf("expected MODP-1024 downgrade, got %v (%v)", got, err)
	}

	// Unknown peer groups are ignored.
	_, err = NegotiateDHGroup(policy, []DHGroupID{DHGroup3072, DHGroupID(0x7f)})
	if !errors.Is(err, ErrNoCommonDHGroup) {
		t.Fatalf("expected ErrNoCommonDHGroup, got %v", err)
	}
}

func TestNegotiateNullDHGate(t *testing.T) {
	// Null offer against a policy that forbids it: explicit, distinguishable
	// rejection rather than silent acceptance.
	policy := strongPolicy()
	_, err := NegotiateDHGroup(policy, []DHGroupID{DHGroupNull})
	if !errors.Is(err, ErrNullDHNotAllowed) {
		t.Fatalf("expected ErrNullDHNotAllowed, got %v", err)
	}

	// A real group always wins over null even when null is permitted.
	permissive := Policy{
		Hashes:      []HashID{HashSHA256},
		DHGroups:    []DHGroupID{DHGroup2048},
		AllowNullDH: true,
	}
	got, err := NegotiateDHGroup(permissive, []DHGroupID{DHGroupNull, DHGroup2048})
	if err != nil || got != DHGroup2048 {
		t.Fatalf("expected real group to beat null, got %v (%v)", got, err)
	}

	// Null is selected only when explicitly allowed and nothing else matches.
	got, err = NegotiateDHGroup(permissive, []DHGroupID{DHGroupNull})
	if err != nil || got != DHGroupNull {
		t.Fatalf("expected null DH, got %v (%v)", got, err)
	}
}
