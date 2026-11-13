package netsec

import (
	"crypto"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"hash"
	"strings"
)

const tls13LabelPrefix = "tls13 "

// ExpandLabel implements HKDF-Expand-Label from RFC 8446, Section 7.1.
//
// Accepted arguments are the hash constructor, the input secret, the label,
// the context (which may be nil), and the output length. Arguments are matched
// by type, so the common argument orders used by TLS stacks are accepted.
func ExpandLabel(args ...any) ([]byte, error) {
	p, err := parseExpandLabelArgs(args)
	if err != nil {
		return nil, err
	}
	return expandLabel(p.hash, p.secret, p.label, p.context, p.length)
}

func expandLabel(h func() hash.Hash, secret []byte, label string, context []byte, length int) ([]byte, error) {
	if h == nil {
		return nil, fmt.Errorf("netsec: nil hash constructor")
	}
	if len(tls13LabelPrefix)+len(label) > 255 {
		return nil, fmt.Errorf("%w: %q", ErrInvalidLabel, label)
	}
	if len(context) > 255 {
		return nil, fmt.Errorf("%w: %d bytes", ErrInvalidContext, len(context))
	}
	hashLen := h().Size()
	if length < 0 || length > 255*hashLen {
		return nil, fmt.Errorf("%w: %d (hash output %d)", ErrInvalidLength, length, hashLen)
	}
	if length == 0 {
		return []byte{}, nil
	}

	info := make([]byte, 0, 2+1+len(tls13LabelPrefix)+len(label)+1+len(context))
	info = binary.BigEndian.AppendUint16(info, uint16(length))
	info = append(info, byte(len(tls13LabelPrefix)+len(label)))
	info = append(info, tls13LabelPrefix...)
	info = append(info, label...)
	info = append(info, byte(len(context)))
	info = append(info, context...)

	out, err := hkdf.Expand(h, secret, string(info), length)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func hashBytes(h func() hash.Hash, b []byte) []byte {
	hh := h()
	_, _ = hh.Write(b)
	return hh.Sum(nil)
}

// DeriveSecret implements Derive-Secret from RFC 8446, Section 7.1.
//
// The transcript may be supplied as a hash.Hash positioned at the desired
// point, or as raw transcript bytes. A nil transcript means the empty
// transcript.
func DeriveSecret(args ...any) ([]byte, error) {
	p, err := parseDeriveSecretArgs(args)
	if err != nil {
		return nil, err
	}
	if p.transcriptHash != nil {
		return deriveSecretHash(p.hash, p.secret, p.label, p.transcriptHash)
	}
	return deriveSecretBytes(p.hash, p.secret, p.label, p.transcript)
}

func deriveSecretBytes(h func() hash.Hash, secret []byte, label string, transcript []byte) ([]byte, error) {
	if h == nil {
		return nil, fmt.Errorf("netsec: nil hash constructor")
	}
	digest := hashBytes(h, transcript)
	return expandLabel(h, secret, label, digest, h().Size())
}

func deriveSecretHash(h func() hash.Hash, secret []byte, label string, transcript hash.Hash) ([]byte, error) {
	if h == nil {
		return nil, fmt.Errorf("netsec: nil hash constructor")
	}
	var digest []byte
	if transcript == nil {
		digest = hashBytes(h, nil)
	} else {
		digest = transcript.Sum(nil)
	}
	return expandLabel(h, secret, label, digest, h().Size())
}

// DeriveEarlyExporterMasterSecret derives early_exporter_master_secret from
// the early secret and the transcript through ClientHello, as specified by
// RFC 8446, Section 7.1:
//
//	Derive-Secret(early_secret, "e exp master", ClientHello)
//
// The arguments are the hash constructor, the early secret, and the
// transcript. The transcript can be a hash.Hash or raw transcript bytes.
func DeriveEarlyExporterMasterSecret(args ...any) ([]byte, error) {
	p, err := parsePhaseDerivationArgs(args, PhaseEarly)
	if err != nil {
		return nil, err
	}
	return derivePhaseExporterSecret(p)
}

// DeriveEarlyExporterSecret is an alias for
// DeriveEarlyExporterMasterSecret.
func DeriveEarlyExporterSecret(args ...any) ([]byte, error) {
	return DeriveEarlyExporterMasterSecret(args...)
}

// ComputeEarlyExporterMasterSecret is an alias for
// DeriveEarlyExporterMasterSecret.
func ComputeEarlyExporterMasterSecret(args ...any) ([]byte, error) {
	return DeriveEarlyExporterMasterSecret(args...)
}

// DeriveEarlyExporterKey is an alias for
// DeriveEarlyExporterMasterSecret.
func DeriveEarlyExporterKey(args ...any) ([]byte, error) {
	return DeriveEarlyExporterMasterSecret(args...)
}

// DeriveExporterMasterSecret derives exporter_master_secret from the TLS 1.3
// master secret and the transcript through server Finished:
//
//	Derive-Secret(master_secret, "exp master", ClientHello...server Finished)
//
// The arguments are the hash constructor, the master secret, and the
// transcript. The transcript can be a hash.Hash or raw transcript bytes.
func DeriveExporterMasterSecret(args ...any) ([]byte, error) {
	p, err := parsePhaseDerivationArgs(args, PhasePostHandshake)
	if err != nil {
		return nil, err
	}
	return derivePhaseExporterSecret(p)
}

// DeriveEarlyExporterMasterSecretFromDigest is like
// DeriveEarlyExporterMasterSecret but treats the supplied transcript bytes
// as an already-computed transcript hash.
func DeriveEarlyExporterMasterSecretFromDigest(args ...any) ([]byte, error) {
	p, err := parsePhaseDerivationArgs(args, PhaseEarly)
	if err != nil {
		return nil, err
	}
	return derivePhaseExporterSecretFromDigest(p)
}

// DeriveExporterMasterSecretFromDigest is like DeriveExporterMasterSecret but
// treats the supplied transcript bytes as an already-computed transcript
// hash.
func DeriveExporterMasterSecretFromDigest(args ...any) ([]byte, error) {
	p, err := parsePhaseDerivationArgs(args, PhasePostHandshake)
	if err != nil {
		return nil, err
	}
	return derivePhaseExporterSecretFromDigest(p)
}

// DeriveSecretFromDigest is like DeriveSecret but treats the supplied
// transcript bytes as an already-computed transcript hash.
func DeriveSecretFromDigest(args ...any) ([]byte, error) {
	p, err := parseDeriveSecretArgs(args)
	if err != nil {
		return nil, err
	}
	digest := p.transcript
	if p.transcriptHash != nil {
		digest = p.transcriptHash.Sum(nil)
	}
	if len(digest) == 0 {
		digest = hashBytes(p.hash, nil)
	}
	return expandLabel(p.hash, p.secret, p.label, digest, p.hash().Size())
}

// DeriveSecretFromTranscriptHash is an alias for DeriveSecretFromDigest.
func DeriveSecretFromTranscriptHash(args ...any) ([]byte, error) {
	return DeriveSecretFromDigest(args...)
}

// DeriveEarlyExporterMasterSecretFromTranscriptHash is an alias for
// DeriveEarlyExporterMasterSecretFromDigest.
func DeriveEarlyExporterMasterSecretFromTranscriptHash(args ...any) ([]byte, error) {
	return DeriveEarlyExporterMasterSecretFromDigest(args...)
}

// DeriveExporterMasterSecretFromTranscriptHash is an alias for
// DeriveExporterMasterSecretFromDigest.
func DeriveExporterMasterSecretFromTranscriptHash(args ...any) ([]byte, error) {
	return DeriveExporterMasterSecretFromDigest(args...)
}

// DerivePostHandshakeExporterMasterSecret is an alias for
// DeriveExporterMasterSecret.
func DerivePostHandshakeExporterMasterSecret(args ...any) ([]byte, error) {
	return DeriveExporterMasterSecret(args...)
}

// DerivePostHandshakeExporterSecret is an alias for
// DeriveExporterMasterSecret.
func DerivePostHandshakeExporterSecret(args ...any) ([]byte, error) {
	return DeriveExporterMasterSecret(args...)
}

// DeriveMainExporterMasterSecret is an alias for DeriveExporterMasterSecret.
func DeriveMainExporterMasterSecret(args ...any) ([]byte, error) {
	return DeriveExporterMasterSecret(args...)
}

// ComputeExporterMasterSecret is an alias for DeriveExporterMasterSecret.
func ComputeExporterMasterSecret(args ...any) ([]byte, error) {
	return DeriveExporterMasterSecret(args...)
}

// ComputePostHandshakeExporterMasterSecret is an alias for
// DeriveExporterMasterSecret.
func ComputePostHandshakeExporterMasterSecret(args ...any) ([]byte, error) {
	return DeriveExporterMasterSecret(args...)
}

// DerivePostHandshakeExporterKey is an alias for
// DeriveExporterMasterSecret.
func DerivePostHandshakeExporterKey(args ...any) ([]byte, error) {
	return DeriveExporterMasterSecret(args...)
}

func derivePhaseExporterSecret(p parsedPhaseDerivation) ([]byte, error) {
	label := "exp master"
	if p.phase == PhaseEarly {
		label = "e exp master"
	}
	if p.transcriptHash != nil {
		return deriveSecretHash(p.hash, p.secret, label, p.transcriptHash)
	}
	return deriveSecretBytes(p.hash, p.secret, label, p.transcript)
}

func derivePhaseExporterSecretFromDigest(p parsedPhaseDerivation) ([]byte, error) {
	label := "exp master"
	if p.phase == PhaseEarly {
		label = "e exp master"
	}
	digest := p.transcript
	if p.transcriptHash != nil {
		digest = p.transcriptHash.Sum(nil)
	}
	if len(digest) == 0 {
		digest = hashBytes(p.hash, nil)
	}
	return expandLabel(p.hash, p.secret, label, digest, p.hash().Size())
}

func defaultHash() func() hash.Hash {
	return sha256.New
}

func hashFromValue(v any) (func() hash.Hash, bool) {
	switch h := v.(type) {
	case func() hash.Hash:
		if h != nil {
			return h, true
		}
	case crypto.Hash:
		if h.Available() {
			return h.New, true
		}
	case string:
		switch strings.ToLower(strings.ReplaceAll(h, "-", "")) {
		case "sha256":
			return sha256.New, true
		case "sha384":
			return sha512.New384, true
		case "sha512":
			return sha512.New, true
		case "sha512/256", "sha512256":
			return sha512.New512_256, true
		}
	}
	return nil, false
}

func bytesFromValue(v any) ([]byte, bool) {
	b, ok := v.([]byte)
	return b, ok
}

func intFromValue(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint:
		return int(n), true
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		return int(n), true
	}
	return 0, false
}
