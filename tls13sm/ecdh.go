package tls13sm

import (
	"crypto/ecdh"
	"fmt"
)

// NamedGroup identifiers used in supported_groups / key_share (RFC 8446 B.3.1.4).
const (
	GroupX25519 uint16 = 0x001d
	GroupP256   uint16 = 0x0017
	GroupP384   uint16 = 0x0018
)

// SupportedGroups returns the groups offered by default, in preference order.
func SupportedGroups() []uint16 {
	return []uint16{GroupX25519, GroupP256, GroupP384}
}

func ecdhCurve(group uint16) (ecdh.Curve, error) {
	switch group {
	case GroupX25519:
		return ecdh.X25519(), nil
	case GroupP256:
		return ecdh.P256(), nil
	case GroupP384:
		return ecdh.P384(), nil
	default:
		return nil, errProto("unsupported named group 0x%04x", group)
	}
}

// keyShare is one uncompressed public key share together with its group.
type keyShare struct {
	Group      uint16
	PublicKey  []byte
	privateKey *ecdh.PrivateKey
}

// generateKeyShare creates a fresh ephemeral ECDHE key pair for group.
func generateKeyShare(group uint16) (*keyShare, error) {
	curve, err := ecdhCurve(group)
	if err != nil {
		return nil, err
	}
	private, err := curve.GenerateKey(randReader{})
	if err != nil {
		return nil, fmt.Errorf("tls13sm: ECDHE key generation failed: %w", err)
	}
	return &keyShare{
		Group:      group,
		PublicKey:  private.PublicKey().Bytes(),
		privateKey: private,
	}, nil
}

// ecdhShared performs the ECDH operation against a peer's uncompressed public
// key and returns the raw shared secret used as (EC)DHE in the key schedule.
func (ks *keyShare) ecdhShared(group uint16, peerPublic []byte) ([]byte, error) {
	if ks.Group != group {
		return nil, errProto("key share group mismatch: local %d peer %d", ks.Group, group)
	}
	curve, err := ecdhCurve(group)
	if err != nil {
		return nil, err
	}
	peer, err := curve.NewPublicKey(peerPublic)
	if err != nil {
		return nil, errProto("invalid peer ECDHE public key: %v", err)
	}
	secret, err := ks.privateKey.ECDH(peer)
	if err != nil {
		return nil, fmt.Errorf("tls13sm: ECDH operation failed: %w", err)
	}
	return secret, nil
}

// marshalKeyShareList serializes a set of key shares as a KeyShareEntry list,
// including the outer u16 length prefix used by the key_share extension.
func marshalKeyShareList(shares []*keyShare) []byte {
	var entries []byte
	for _, share := range shares {
		entries = appendU16(entries, share.Group)
		entries = appendLength16(entries, share.PublicKey)
	}
	return appendLength16(nil, entries)
}

// parseKeyShareList parses the body of a key_share extension into group/key
// pairs.
func parseKeyShareList(raw []byte) ([]keyShare, error) {
	r := newReader(raw)
	list, err := r.vector16()
	if err != nil {
		return nil, err
	}
	r = newReader(list)
	var out []keyShare
	for r.remaining() > 0 {
		group, err := r.u16()
		if err != nil {
			return nil, err
		}
		key, err := r.vector16()
		if err != nil {
			return nil, err
		}
		out = append(out, keyShare{Group: group, PublicKey: key})
	}
	return out, nil
}
