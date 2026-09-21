package bitlayout_test

import (
	"bytes"
	"reflect"
	"testing"

	"bitlayout"
	"bitlayout/internal/refimpl"
)

// TestAcceptanceRoundTrip covers the acceptance record: 3-bit tag,
// 10-bit signed value, both union branches, a cross-byte array, and the
// trailing checksum.
func TestAcceptanceRoundTrip(t *testing.T) {
	plan := mustCompile(t, sensorSchema)
	records := []map[string]any{
		{ // branch 0: short
			"tag": uint64(0), "level": int64(-512),
			"body": map[string]any{"code": uint64(9)},
		},
		{ // branch 1: cross-byte array (12-bit elements)
			"tag": uint64(1), "level": int64(511),
			"body": map[string]any{"count": uint64(3),
				"samples": []any{uint64(2748), uint64(291), uint64(4095)}},
		},
		{ // empty array
			"tag": uint64(1), "level": int64(0),
			"body": map[string]any{"count": uint64(0), "samples": []any{}},
		},
	}
	for i, rec := range records {
		nbits, err := plan.Validate(rec)
		if err != nil {
			t.Fatalf("rec %d validate: %v", i, err)
		}
		buf := make([]byte, (nbits+7)/8)
		n, err := plan.Encode(rec, buf)
		if err != nil {
			t.Fatalf("rec %d encode: %v", i, err)
		}
		if n != nbits {
			t.Fatalf("rec %d: Validate said %d bits, Encode wrote %d", i, nbits, n)
		}
		// Cross-check the bit layout against the independent reference.
		refBits, err := refimpl.Encode([]byte(sensorSchema), rec)
		if err != nil {
			t.Fatalf("rec %d ref encode: %v", i, err)
		}
		if !bytes.Equal(bitsToBytes(refBits, bitlayout.MSBFirst), buf) {
			t.Fatalf("rec %d: production bits differ from reference", i)
		}
		got, used, err := plan.Decode(buf, n, nil)
		if err != nil {
			t.Fatalf("rec %d decode: %v", i, err)
		}
		if used != n {
			t.Fatalf("rec %d: consumed %d of %d bits", i, used, n)
		}
		for k, want := range rec {
			if !reflect.DeepEqual(got[k], want) {
				t.Fatalf("rec %d field %q: got %#v want %#v", i, k, got[k], want)
			}
		}
		if _, ok := got["crc"]; !ok {
			t.Fatalf("rec %d: decoded record missing checksum", i)
		}
	}
}

// TestSignedValueRange checks the 10-bit signed field boundaries.
func TestSignedValueRange(t *testing.T) {
	plan := mustCompile(t, sensorSchema)
	for _, level := range []int64{-512, -1, 0, 1, 511} {
		rec := map[string]any{"tag": uint64(0), "level": level,
			"body": map[string]any{"code": uint64(0)}}
		buf := make([]byte, 8)
		n, err := plan.Encode(rec, buf)
		if err != nil {
			t.Fatalf("level %d: %v", level, err)
		}
		got, _, err := plan.Decode(buf, n, nil)
		if err != nil {
			t.Fatalf("level %d decode: %v", level, err)
		}
		if got["level"] != level {
			t.Fatalf("level %d round trip gave %v", level, got["level"])
		}
	}
	for _, level := range []int64{-513, 512} {
		rec := map[string]any{"tag": uint64(0), "level": level,
			"body": map[string]any{"code": uint64(0)}}
		if _, err := plan.Encode(rec, make([]byte, 8)); err == nil {
			t.Fatalf("level %d should be rejected", level)
		}
	}
}
