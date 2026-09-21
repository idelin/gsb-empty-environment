package bitlayout_test

import (
	"bitlayout"
	"bitlayout/internal/refimpl"
)

// sensorSchema is the acceptance schema: 3-bit tag, 10-bit signed value,
// two union branches of different lengths, a cross-byte array, and a
// trailing checksum.
const sensorSchema = `{
  "name": "sensor",
  "bitOrder": "msb",
  "byteOrder": "big",
  "fields": [
    { "kind": "int", "name": "tag", "bits": 3 },
    { "kind": "int", "name": "level", "bits": 10, "signed": true },
    { "kind": "union", "name": "body", "tagFrom": "tag", "cases": {
        "0": { "fields": [ { "kind": "int", "name": "code", "bits": 4 } ] },
        "1": { "fields": [
          { "kind": "int", "name": "count", "bits": 4 },
          { "kind": "array", "name": "samples", "lengthFrom": "count",
            "element": { "kind": "int", "bits": 12 } } ] }
    } },
    { "kind": "pad", "align": 8 },
    { "kind": "checksum", "name": "crc", "bits": 8, "algo": "crc8", "from": "tag", "to": "body" }
  ]
}`

// bytesToBits expands buf into a bit vector using the given order.
func bytesToBits(buf []byte, nbits int, order bitlayout.BitOrder) []bool {
	out := make([]bool, nbits)
	for i := 0; i < nbits; i++ {
		if order == bitlayout.MSBFirst {
			out[i] = buf[i/8]&(0x80>>uint(i%8)) != 0
		} else {
			out[i] = buf[i/8]&(1<<uint(i%8)) != 0
		}
	}
	return out
}

// bitsToBytes packs a bit vector using the given order.
func bitsToBytes(bits []bool, order bitlayout.BitOrder) []byte {
	out := make([]byte, (len(bits)+7)/8)
	for i, b := range bits {
		if b {
			if order == bitlayout.MSBFirst {
				out[i/8] |= 0x80 >> uint(i%8)
			} else {
				out[i/8] |= 1 << uint(i%8)
			}
		}
	}
	return out
}

func refKind(err error) refimpl.Kind {
	if err == nil {
		return ""
	}
	return err.(*refimpl.Error).Kind
}

func prodKind(err error) bitlayout.ErrKind {
	if err == nil {
		return -1
	}
	return err.(*bitlayout.Error).Kind
}
