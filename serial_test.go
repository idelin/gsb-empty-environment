package netsec

import "testing"

func TestCompareSerial(t *testing.T) {
	tests := []struct {
		name string
		a    uint32
		b    uint32
		want SerialRelation
	}{
		{name: "equal", a: 42, b: 42, want: SerialEqual},
		{name: "ordinary greater", a: 43, b: 42, want: SerialGreater},
		{name: "ordinary less", a: 41, b: 42, want: SerialLess},
		{name: "wrap greater", a: 5, b: 0xfffffff0, want: SerialGreater},
		{name: "wrap less", a: 0xfffffff0, b: 5, want: SerialLess},
		{name: "max to zero greater", a: 0, b: 0xffffffff, want: SerialGreater},
		{name: "zero to max less", a: 0xffffffff, b: 0, want: SerialLess},
		{name: "exactly half undefined", a: 0x80000000, b: 0, want: SerialUndefined},
		{name: "exactly half undefined reversed", a: 0, b: 0x80000000, want: SerialUndefined},
		{name: "half plus one is less", a: 0x80000001, b: 0, want: SerialLess},
		{name: "half minus one is greater", a: 0x7fffffff, b: 0, want: SerialGreater},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CompareSerial(test.a, test.b); got != test.want {
				t.Fatalf("CompareSerial(%d, %d) = %s, want %s", test.a, test.b, got, test.want)
			}
		})
	}
}

func TestSerialIsNewerRejectsUndefined(t *testing.T) {
	if SerialIsNewer(0, 0x80000000) {
		t.Fatalf("undefined RFC 1982 comparison reported as newer")
	}
	if !SerialIsNewer(0, 0xffffffff) {
		t.Fatalf("wrapped newer serial not reported as newer")
	}
}
