package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

const (
	exporterLabel     = "netsec/eap/tunnel-exporter/v1"
	transcriptLabel   = "netsec/eap/inner-transcript/v1"
	innerBindingLabel = "netsec/eap/inner-binding/v1"
	maxExchangePacket = 1 << 16
)

func writeField(h hash.Hash, field []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(field)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(field)
}

func writeUint64(h hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = h.Write(encoded[:])
}

func computeExporterDigest(exporter []byte) [32]byte {
	h := sha256.New()
	writeField(h, []byte(exporterLabel))
	writeField(h, exporter)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// ExchangeDigest returns the domain-separated digest of protected inner EAP
// exchange packets. Message boundaries are included, so concatenation is
// unambiguous.
func ExchangeDigest(packets ...[]byte) [32]byte {
	h := sha256.New()
	writeField(h, []byte(transcriptLabel))
	for _, packet := range packets {
		writeField(h, packet)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func computeBindingDigest(
	exporter []byte,
	tunnel TunnelID,
	port Port,
	transcriptDigest [32]byte,
	innerIdentity string,
) [32]byte {
	mac := hmac.New(sha256.New, exporter)
	writeField(mac, []byte(innerBindingLabel))
	writeField(mac, tunnel[:])
	writeField(mac, []byte(port.name))
	writeField(mac, port.instance[:])
	writeUint64(mac, port.generation)
	writeField(mac, transcriptDigest[:])
	writeField(mac, []byte(innerIdentity))

	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
