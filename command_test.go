package netsec

import "testing"

func TestDecodeSingleCommand(t *testing.T) {
	// IOA 0x000102, S/E=1 (select), QU=1, SCS=1 (ON).
	// Value octet: 0x80 | (1<<1) | 0x01 = 0x83.
	cmd, err := DecodeSingleCommand([]byte{0x02, 0x01, 0x00, 0x83})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cmd.IOA != 258 {
		t.Errorf("IOA = %d, want 258", cmd.IOA)
	}
	if !cmd.Select {
		t.Error("Select = false, want true (S/E=1)")
	}
	if !cmd.On {
		t.Error("On = false, want true (SCS=1)")
	}
	if cmd.QU != 1 {
		t.Errorf("QU = %d, want 1", cmd.QU)
	}
}

func TestDecodeExecuteCommand(t *testing.T) {
	// S/E=0 (execute), QU=0, SCS=0 (OFF): value octet 0x00.
	cmd, err := DecodeSingleCommand([]byte{0x07, 0x00, 0x00, 0x00})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cmd.Select {
		t.Error("Select = true, want false (S/E=0)")
	}
	if cmd.On {
		t.Error("On = true, want false (SCS=0)")
	}
	if cmd.IOA != 7 {
		t.Errorf("IOA = %d, want 7", cmd.IOA)
	}
}

func TestDecodeSingleCommandMalformed(t *testing.T) {
	if _, err := DecodeSingleCommand([]byte{0x01, 0x02}); err != ErrMalformedSingleCommand {
		t.Fatalf("err = %v, want ErrMalformedSingleCommand", err)
	}
}

func TestSingleCommandRoundTrip(t *testing.T) {
	cases := []SingleCommand{
		{IOA: 1, Select: true, On: true, QU: 1},
		{IOA: 65535, Select: false, On: false, QU: 0},
		{IOA: 16777215, Select: true, On: false, QU: 31},
		{IOA: 4096, Select: false, On: true, QU: 17},
	}
	for _, want := range cases {
		got, err := DecodeSingleCommand(want.Encode())
		if err != nil {
			t.Fatalf("decode %+v: %v", want, err)
		}
		if got != want {
			t.Errorf("round trip = %+v, want %+v", got, want)
		}
	}
}

func TestSingleCommandValueEqualityIgnoresSE(t *testing.T) {
	sel := SingleCommand{IOA: 9, Select: true, On: true}
	exec := SingleCommand{IOA: 9, Select: false, On: true}
	if !sel.sameCommandValue(exec) {
		t.Error("same SCS with different S/E should be the same command value")
	}
	off := SingleCommand{IOA: 9, Select: false, On: false}
	if sel.sameCommandValue(off) {
		t.Error("ON select and OFF execute must not match")
	}
}
