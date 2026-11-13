package netsec

import (
	"errors"
	"testing"
)

func testDevices() DeviceSet {
	return DeviceSet{
		"/dev/sda":           {SizeSectors: 4096},
		"/dev/sdb":           {SizeSectors: 2048},
		"/dev/readonly":      {SizeSectors: 1024, ReadOnly: true},
		"/dev/mapper/secure": {SizeSectors: 512},
	}
}

func TestValidateTableAllowsSupportedTargets(t *testing.T) {
	table := `
0 100 linear /dev/sda 0
100 100 crypt aes-cbc-plain64 00112233445566778899aabbccddeeff 0 /dev/sda 0
200 100 verity 1 /dev/sda /dev/sdb 512 512 100 1 sha256 00
300 100 thin /dev/mapper/pool 1
400 100 thin-pool /dev/sdb /dev/sda 128 10
`
	if err := ValidateTable(table, testDevices()); err != nil {
		t.Fatalf("valid table rejected: %v", err)
	}
}

func TestValidateTableAllowsCompactVerityForm(t *testing.T) {
	table := "0 100 verity /dev/sda /dev/sdb\n"
	if err := ValidateTable(table, testDevices()); err != nil {
		t.Fatalf("compact verity form rejected: %v", err)
	}
}

func TestValidateTableRejectsHoles(t *testing.T) {
	tests := []struct {
		name  string
		table string
	}{
		{
			name:  "does not start at zero",
			table: "1 100 linear /dev/sda 0\n",
		},
		{
			name:  "gap between segments",
			table: "0 100 linear /dev/sda 0\n200 100 linear /dev/sda 0\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTable(tt.table, testDevices())
			if !errors.Is(err, ErrNotContiguous) {
				t.Fatalf("got %v, want ErrNotContiguous", err)
			}
		})
	}
}

func TestValidateTableRejectsOverlap(t *testing.T) {
	table := "0 100 linear /dev/sda 0\n50 100 linear /dev/sda 0\n"
	err := ValidateTable(table, testDevices())
	if !errors.Is(err, ErrNotContiguous) {
		t.Fatalf("got %v, want ErrNotContiguous", err)
	}
}

func TestValidateTableRejectsCycle(t *testing.T) {
	table := "0 100 linear /dev/mapper/secure 0\n"
	err := ValidateTable(table, testDevices(), WithMappingName("secure"))
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("got %v, want ErrCycle", err)
	}
}

func TestValidateTableRejectsOutOfBounds(t *testing.T) {
	tests := []struct {
		name  string
		table string
	}{
		{
			name:  "linear offset plus length exceeds device",
			table: "0 100 linear /dev/sda 4000\n",
		},
		{
			name:  "linear offset equals device size",
			table: "0 100 linear /dev/sda 4096\n",
		},
		{
			name:  "verity data exceeds device",
			table: "0 5000 verity 1 /dev/sda /dev/sdb 512 512 5000 1 sha256 00\n",
		},
		{
			name:  "verity hash starts beyond device",
			table: "0 100 verity 1 /dev/sda /dev/sdb 512 512 100 4096 sha256 00\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTable(tt.table, testDevices())
			if !errors.Is(err, ErrOutOfBounds) {
				t.Fatalf("got %v, want ErrOutOfBounds", err)
			}
		})
	}
}

func TestValidateTableRejectsUnregisteredDevice(t *testing.T) {
	table := "0 100 linear /dev/unknown 0\n"
	err := ValidateTable(table, testDevices())
	if !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("got %v, want ErrUnknownDevice", err)
	}
}

func TestValidateTableRejectsCipherNull(t *testing.T) {
	table := "0 100 crypt cipher_null deadbeef 0 /dev/sda 0\n"
	err := ValidateTable(table, testDevices())
	if !errors.Is(err, ErrInsecureCipher) {
		t.Fatalf("got %v, want ErrInsecureCipher", err)
	}
}

func TestValidateTableRejectsWrongCryptKeyLength(t *testing.T) {
	tests := []struct {
		name  string
		table string
	}{
		{
			name:  "short AES key",
			table: "0 100 crypt aes-cbc-plain64 deadbeef 0 /dev/sda 0\n",
		},
		{
			name:  "single AES-XTS key",
			table: "0 100 crypt aes-xts-plain64 00112233445566778899aabbccddeeff 0 /dev/sda 0\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTable(tt.table, testDevices())
			if !errors.Is(err, ErrKeyLength) {
				t.Fatalf("got %v, want ErrKeyLength", err)
			}
		})
	}
}

func TestValidateTableAllowsDoubleLengthAESXTSKey(t *testing.T) {
	table := "0 100 crypt aes-xts-plain64 00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff 0 /dev/sda 0\n"
	if err := ValidateTable(table, testDevices()); err != nil {
		t.Fatalf("valid AES-XTS table rejected: %v", err)
	}
}

func TestValidateTableRejectsReadOnlyDeviceThroughWritableLinear(t *testing.T) {
	table := "0 100 linear /dev/readonly 0\n"
	err := ValidateTable(table, testDevices())
	if !errors.Is(err, ErrReadOnlyDevice) {
		t.Fatalf("got %v, want ErrReadOnlyDevice", err)
	}
}

func TestValidateTableAllowsReadOnlyDeviceThroughReadOnlyMapping(t *testing.T) {
	table := "0 100 linear /dev/readonly 0\n"
	err := ValidateTable(table, testDevices(), WithReadOnlyMapping(true))
	if err != nil {
		t.Fatalf("read-only mapping rejected: %v", err)
	}
}

func TestValidateTableRejectsTotalOverflow(t *testing.T) {
	table := "0 18446744073709551615 thin /dev/mapper/pool 1\n" +
		"18446744073709551615 1 thin /dev/mapper/pool 1\n"
	err := ValidateTable(table, testDevices())
	if !errors.Is(err, ErrTotalOverflow) {
		t.Fatalf("got %v, want ErrTotalOverflow", err)
	}
}

func TestValidateTableRejectsUnsupportedTarget(t *testing.T) {
	table := "0 100 mirror /dev/sda 0\n"
	err := ValidateTable(table, testDevices())
	if !errors.Is(err, ErrUnsupportedTarget) {
		t.Fatalf("got %v, want ErrUnsupportedTarget", err)
	}
}

func TestValidateTableRejectsZeroLength(t *testing.T) {
	table := "0 0 linear /dev/sda 0\n"
	err := ValidateTable(table, testDevices())
	if !errors.Is(err, ErrZeroLength) {
		t.Fatalf("got %v, want ErrZeroLength", err)
	}
}

func TestValidateTableRejectsMalformedLine(t *testing.T) {
	table := "0 100 linear\n"
	err := ValidateTable(table, testDevices())
	if !errors.Is(err, ErrMalformedTable) {
		t.Fatalf("got %v, want ErrMalformedTable", err)
	}
}

func TestValidateTableRejectsEmptyTable(t *testing.T) {
	err := ValidateTable("# only comments\n\n", testDevices())
	if !errors.Is(err, ErrEmptyTable) {
		t.Fatalf("got %v, want ErrEmptyTable", err)
	}
}

func TestValidateTableRejectsHugeSectorValues(t *testing.T) {
	table := "0 18446744073709551616 thin /dev/mapper/pool 1\n"
	err := ValidateTable(table, testDevices())
	if !errors.Is(err, ErrMalformedTable) {
		t.Fatalf("got %v, want ErrMalformedTable", err)
	}
}

func TestParseTable(t *testing.T) {
	segments, err := ParseTable("0 10 linear /dev/sda 0\n10 5 crypt aes-cbc-plain64 00 0 /dev/sdb 0\n")
	if err != nil {
		t.Fatalf("ParseTable failed: %v", err)
	}
	if len(segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(segments))
	}
	if segments[0].Start != 0 || segments[0].Length != 10 ||
		segments[0].TargetType != "linear" || len(segments[0].Params) != 2 {
		t.Fatalf("unexpected first segment: %+v", segments[0])
	}
}

func TestOutOfBoundsNearUint64Limit(t *testing.T) {
	table := "0 2 linear /dev/sda 18446744073709551614\n"
	err := ValidateTable(table, testDevices())
	if !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("got %v, want ErrOutOfBounds", err)
	}
}
