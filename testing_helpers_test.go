package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"testing"

	_ "crypto/sha1"
	_ "crypto/sha256"
	_ "crypto/sha512"
)

const (
	evBootCode   uint32 = 0x1
	evBootConfig uint32 = 0x2
)

type rawEvent struct {
	pcr       int
	etype     uint32
	data      []byte
	eventData []byte
}

func testReader() io.Reader {
	return rand.Reader
}

// buildEventLog serializes a TCG PC Client log whose Spec ID advertises the
// given banks. Each digest is computed from rawEvent.data per bank, allowing
// tests to exercise mixed SHA-1/SHA-256 replay.
func buildEventLog(algorithms []AlgorithmID, events []rawEvent) []byte {
	var log []byte

	var specData []byte
	sig := make([]byte, 16)
	copy(sig, specIDSignature)
	specData = append(specData, sig...)
	specData = append(specData, 0, 0, 0, 0) // platformClass
	specData = append(specData, 0)          // specVersionMinor
	specData = append(specData, 2)          // specVersionMajor
	specData = append(specData, 0)          // specErrata
	specData = append(specData, 2)          // uintnSize
	specData = append(specData, byte(len(algorithms)>>8), byte(len(algorithms)))
	specData = append(specData, 0, 0) // vendorInfoSize
	for _, alg := range algorithms {
		spec, _ := hashSpecFor(alg)
		specData = append(specData, u16(alg)...)
		specData = append(specData, u16n(uint16(spec.size))...)
	}

	log = append(log, u32(0)...)
	log = append(log, u32(evNoAction)...)
	log = append(log, make([]byte, 20)...)
	log = append(log, u32(uint32(len(specData)))...)
	log = append(log, specData...)

	for _, ev := range events {
		log = append(log, u32(uint32(ev.pcr))...)
		log = append(log, u32(ev.etype)...)
		log = append(log, u32(uint32(len(algorithms)))...)
		for _, alg := range algorithms {
			spec, _ := hashSpecFor(alg)
			hasher := spec.crypto.New()
			hasher.Write(ev.data)
			log = append(log, u16(alg)...)
			log = append(log, hasher.Sum(nil)...)
		}
		log = append(log, sized16(ev.eventData)...)
	}
	return log
}

func encodeECCAKPublic(priv *ecdsa.PrivateKey) []byte {
	var body []byte
	body = append(body, u16(AlgECC)...)
	body = append(body, u16(AlgSHA256)...)
	body = append(body, u32(objectAttrSign)...)
	body = append(body, sized16(nil)...)
	body = append(body, u16(AlgNull)...)
	body = append(body, u16n(0x0003)...) // TPM2_ECC_NIST_P256
	body = append(body, u16(AlgNull)...)
	size := (priv.Curve.Params().BitSize + 7) / 8
	body = append(body, sized16(padLeft(priv.X.Bytes(), size))...)
	body = append(body, sized16(padLeft(priv.Y.Bytes(), size))...)
	return sized16(body)
}

func encodeRSAAKPublic(priv *rsa.PrivateKey) []byte {
	var body []byte
	body = append(body, u16(AlgRSA)...)
	body = append(body, u16(AlgSHA256)...)
	body = append(body, u32(objectAttrSign)...)
	body = append(body, sized16(nil)...)
	body = append(body, u16(AlgNull)...)
	body = append(body, u16n(uint16(priv.N.BitLen()))...)
	body = append(body, u32(0)...)
	body = append(body, sized16(priv.N.Bytes())...)
	return sized16(body)
}

func encodePCRSelection(banks []PCRBank) []byte {
	out := u32(uint32(len(banks)))
	for _, bank := range banks {
		out = append(out, u16(bank.Hash)...)
		bitmap := make([]byte, 3)
		for _, pcr := range bank.PCRs {
			bitmap[pcr/8] |= 1 << (7 - pcr%8)
		}
		out = append(out, byte(len(bitmap)))
		out = append(out, bitmap...)
	}
	return out
}

func buildQuote(t *testing.T, nonce []byte, selection []PCRBank,
	replay *ReplayedLog, sigHash crypto.Hash) []byte {
	t.Helper()

	var body []byte
	body = append(body, u16n(tpmGeneratedMagic)...)
	body = append(body, u16n(tpmStAttestQuote)...)
	body = append(body, sized16(nil)...)
	body = append(body, sized16(nonce)...)
	body = append(body, make([]byte, 8)...)
	body = append(body, make([]byte, 4)...)
	body = append(body, make([]byte, 4)...)
	body = append(body, 0)
	body = append(body, make([]byte, 8)...)
	body = append(body, encodePCRSelection(selection)...)

	hasher := sigHash.New()
	for _, bank := range selection {
		for _, pcr := range bank.PCRs {
			hasher.Write(replay.PCRs[bank.Hash][pcr])
		}
	}
	body = append(body, sized16(hasher.Sum(nil))...)
	return sized16(body)
}

func fixtureEvents() []rawEvent {
	return []rawEvent{
		{pcr: 0, etype: evBootCode, data: []byte("firmware-volume"), eventData: []byte("EV_PLATFORM_FIRMWARE_BLOB")},
		{pcr: 1, etype: evBootConfig, data: []byte("boot-config-0"), eventData: []byte("EV_VARIABLE_DRIVER_CONFIG")},
		{pcr: 0, etype: evBootCode, data: []byte("bootloader-stage"), eventData: []byte("EV_BOOT_SERVICES_APPLICATION")},
	}
}

func fixtureBanks() []AlgorithmID {
	return []AlgorithmID{AlgSHA1, AlgSHA256}
}

func allowedFromReplay(replay *ReplayedLog, deviceID string) AllowedSequence {
	var measurements []Measurement
	for _, event := range replay.Events {
		if event.EventType == evNoAction {
			continue
		}
		measurements = append(measurements, Measurement{
			PCRIndex:  event.PCRIndex,
			EventType: event.EventType,
			Digests:   event.Digests,
		})
	}
	return AllowedSequence{DeviceID: deviceID, Measurements: measurements}
}
