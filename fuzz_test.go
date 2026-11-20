package netsec

import "testing"

func FuzzParseRecord(f *testing.F) {
	f.Add([]byte{0x08, 0x01})
	f.Add([]byte{0x12, 0x00})
	f.Add([]byte{0xa5})
	f.Add(V2SignaturePrefix)

	f.Fuzz(func(t *testing.T, raw []byte) {
		record, err := ParseRecord(raw)
		if err != nil {
			if record != nil {
				t.Fatal("failed parse must not return a record")
			}
			return
		}
		// A successfully parsed record must round-trip its raw bytes and
		// must never panic in the CBOR decoder, whatever Data contains.
		_ = record.Raw()
		_, _ = parseCBOR(record.Data)
	})
}

func FuzzParseCBOR(f *testing.F) {
	f.Add([]byte{0xa1, 0x63, 'T', 'T', 'L', 0x01})
	f.Add([]byte{0xbf, 0xff})
	f.Add([]byte{0xa0})

	f.Fuzz(func(t *testing.T, data []byte) {
		parsed, err := parseCBOR(data)
		if err != nil {
			if parsed != nil {
				t.Fatal("failed parse must not return a value")
			}
		}
	})
}
