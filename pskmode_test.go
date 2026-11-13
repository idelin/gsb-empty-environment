package netsec

import (
	"errors"
	"testing"
)

const groupX25519 uint16 = 0x001d

var bothModes = []PSKKeyExchangeMode{PSKKe, PSKDHEKe}

func x25519Share() KeyShare {
	return KeyShare{Group: groupX25519, KeyExchange: make([]byte, 32)}
}

// 场景：模式写 psk_dhe_ke 并删掉 key_share，必须失败，不能降成 psk_ke。
func TestDHESelectedWithoutKeyShareFails(t *testing.T) {
	err := ValidatePSKSelection(PSKDHEKe, groupX25519, bothModes, nil)
	if !errors.Is(err, ErrMissingKeyShare) {
		t.Fatalf("expected ErrMissingKeyShare, got %v", err)
	}
}

func TestDHESelectedWithMismatchedGroupFails(t *testing.T) {
	shares := []KeyShare{{Group: 0x0017, KeyExchange: make([]byte, 65)}} // secp256r1
	err := ValidatePSKSelection(PSKDHEKe, groupX25519, bothModes, shares)
	if !errors.Is(err, ErrMissingKeyShare) {
		t.Fatalf("expected ErrMissingKeyShare, got %v", err)
	}
}

func TestDHESelectedWithEmptyKeyExchangeFails(t *testing.T) {
	shares := []KeyShare{{Group: groupX25519, KeyExchange: nil}}
	err := ValidatePSKSelection(PSKDHEKe, groupX25519, bothModes, shares)
	if !errors.Is(err, ErrMissingKeyShare) {
		t.Fatalf("expected ErrMissingKeyShare, got %v", err)
	}
}

func TestDHESelectedWithMatchingShareSucceeds(t *testing.T) {
	err := ValidatePSKSelection(PSKDHEKe, groupX25519, bothModes, []KeyShare{x25519Share()})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// 模式为 psk_ke 且客户端也提供了该模式时，可以没有共享。
func TestPSKKeWithoutShareSucceeds(t *testing.T) {
	err := ValidatePSKSelection(PSKKe, 0, bothModes, nil)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestModeNotOfferedFails(t *testing.T) {
	err := ValidatePSKSelection(PSKDHEKe, groupX25519, []PSKKeyExchangeMode{PSKKe}, []KeyShare{x25519Share()})
	if !errors.Is(err, ErrModeNotOffered) {
		t.Fatalf("expected ErrModeNotOffered, got %v", err)
	}
	err = ValidatePSKSelection(PSKKe, 0, []PSKKeyExchangeMode{PSKDHEKe}, nil)
	if !errors.Is(err, ErrModeNotOffered) {
		t.Fatalf("expected ErrModeNotOffered, got %v", err)
	}
}

func TestSelectPSKModePrefersDHE(t *testing.T) {
	mode, err := SelectPSKMode(groupX25519, bothModes, []KeyShare{x25519Share()})
	if err != nil || mode != PSKDHEKe {
		t.Fatalf("expected psk_dhe_ke, got %v (err=%v)", mode, err)
	}
}

func TestSelectPSKModeFallsBackOnlyAtSelectionTime(t *testing.T) {
	// 客户端没有匹配共享时，选择阶段只能选 psk_ke（客户端提供了的话）。
	mode, err := SelectPSKMode(groupX25519, bothModes, nil)
	if err != nil || mode != PSKKe {
		t.Fatalf("expected psk_ke, got %v (err=%v)", mode, err)
	}
	// 客户端只提供 psk_dhe_ke 却没有共享：无法选定，报错。
	if _, err = SelectPSKMode(groupX25519, []PSKKeyExchangeMode{PSKDHEKe}, nil); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestModeString(t *testing.T) {
	if PSKKe.String() != "psk_ke" || PSKDHEKe.String() != "psk_dhe_ke" {
		t.Fatalf("unexpected names: %q %q", PSKKe, PSKDHEKe)
	}
}
