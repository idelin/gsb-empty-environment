package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"time"
)

type fixture struct {
	key               *ecdsa.PrivateKey
	sessionTranscript []byte
	now               time.Time
	requested         []RequestedElement
	response          *Response
}

func newFixture() *fixture {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

	items := map[string][]IssuerSignedItem{
		"org.iso.18013.5.1": {
			{
				DigestID:          0,
				Random:            bytes.Repeat([]byte{0x01}, 16),
				ElementIdentifier: "family_name",
				ElementValue:      "Smith",
			},
			{
				DigestID:          1,
				Random:            bytes.Repeat([]byte{0x02}, 16),
				ElementIdentifier: "given_name",
				ElementValue:      "Alice",
			},
		},
		"org.iso.18013.5.1.other": {
			{
				DigestID:          0,
				Random:            bytes.Repeat([]byte{0x03}, 16),
				ElementIdentifier: "issue_date",
				ElementValue:      "2026-01-01",
			},
		},
	}

	msoDigests := map[string]map[uint64][]byte{}
	for ns, nsItems := range items {
		msoDigests[ns] = map[uint64][]byte{}
		for _, item := range nsItems {
			encoded, err := encodeIssuerSignedItem(item)
			if err != nil {
				panic(err)
			}
			d := sha256.Sum256(encoded)
			msoDigests[ns][item.DigestID] = d[:]
		}
	}

	transcript := encodedSessionTranscript([]byte("verifier-session-transcript-payload"))

	requested := []RequestedElement{
		{NameSpace: "org.iso.18013.5.1", ElementIdentifier: "family_name"},
		{NameSpace: "org.iso.18013.5.1", ElementIdentifier: "given_name"},
		{NameSpace: "org.iso.18013.5.1.other", ElementIdentifier: "issue_date"},
	}

	resp := &Response{
		DocType: "org.iso.18013.5.1.mDL",
		IssuerSigned: IssuerSigned{
			NameSpaces: items,
			MSO: MobileSecurityObject{
				DocType:         "org.iso.18013.5.1.mDL",
				DigestAlgorithm: "SHA-256",
				ValueDigests:    msoDigests,
				ValidityInfo: ValidityInfo{
					Signed:     now.Add(-time.Hour),
					ValidFrom:  now.Add(-24 * time.Hour),
					ValidUntil: now.Add(24 * time.Hour),
				},
				DeviceKey: &key.PublicKey,
			},
		},
		DeviceSigned: DeviceSigned{
			NameSpaces: map[string]map[string]any{
				"org.iso.18013.5.1": {
					"device_auth_nonce": "nonce-value",
				},
			},
		},
	}

	f := &fixture{
		key:               key,
		sessionTranscript: transcript,
		now:               now,
		requested:         requested,
		response:          resp,
	}
	if err := f.signDevice(); err != nil {
		panic(err)
	}
	return f
}

// signDevice rebuilds DeviceNameSpacesBytes and the device signature over the
// transcript held by the fixture.
func (f *fixture) signDevice() error {
	dnsBytes, err := encodeDeviceNameSpaces(f.response.DeviceSigned.NameSpaces)
	if err != nil {
		return err
	}
	f.response.DeviceSigned.NameSpacesRaw = dnsBytes
	deviceAuth, err := encodeDeviceAuthentication(
		f.sessionTranscript, f.response.DocType, dnsBytes)
	if err != nil {
		return err
	}
	cose, err := signCOSESign1(f.key, deviceAuth)
	if err != nil {
		return err
	}
	f.response.DeviceSigned.DeviceAuth = DeviceAuth{DeviceAuthCOSE: cose}
	return nil
}

func (f *fixture) options() *Options {
	return &Options{
		SessionTranscript:     f.sessionTranscript,
		Requested:             f.requested,
		Now:                   f.now,
		NewHash:               testNewHash,
		VerifyDeviceSignature: verifyCOSESign1,
	}
}

// cloneResponse returns a copy safe for mutation without touching the fixture.
func (f *fixture) cloneResponse() *Response {
	original := f.response
	dup := &Response{
		DocType: original.DocType,
		IssuerSigned: IssuerSigned{
			NameSpaces: IssuerNameSpaces{},
			MSO: MobileSecurityObject{
				DocType:         original.MSO.DocType,
				DigestAlgorithm: original.MSO.DigestAlgorithm,
				ValueDigests:    map[string]map[uint64][]byte{},
				ValidityInfo:    original.MSO.ValidityInfo,
				DeviceKey:       original.MSO.DeviceKey,
			},
		},
		DeviceSigned: DeviceSigned{
			NameSpaces:    map[string]map[string]any{},
			NameSpacesRaw: append([]byte(nil), original.DeviceSigned.NameSpacesRaw...),
			DeviceAuth: DeviceAuth{
				DeviceAuthCOSE: append([]byte(nil), original.DeviceSigned.DeviceAuth.DeviceAuthCOSE...),
			},
		},
	}
	for ns, nsItems := range original.IssuerSigned.NameSpaces {
		copied := make([]IssuerSignedItem, len(nsItems))
		for i, item := range nsItems {
			copied[i] = item
			copied[i].Random = append([]byte(nil), item.Random...)
		}
		dup.IssuerSigned.NameSpaces[ns] = copied
	}
	for ns, digestMap := range original.MSO.ValueDigests {
		dup.MSO.ValueDigests[ns] = map[uint64][]byte{}
		for id, digest := range digestMap {
			dup.MSO.ValueDigests[ns][id] = append([]byte(nil), digest...)
		}
	}
	for ns, elements := range original.DeviceSigned.NameSpaces {
		dup.DeviceSigned.NameSpaces[ns] = map[string]any{}
		for k, v := range elements {
			dup.DeviceSigned.NameSpaces[ns][k] = v
		}
	}
	return dup
}
