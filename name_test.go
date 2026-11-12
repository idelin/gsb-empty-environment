package taskpkg

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func directExample() []byte {
	return []byte{
		0x03, 'w', 'w', 'w',
		0x07, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		0x03, 'c', 'o', 'm',
		0x00,
	}
}

func mustDecode(t *testing.T, message []byte, offset int) *Name {
	t.Helper()
	name, err := DecodeName(message, offset)
	if err != nil {
		t.Fatalf("DecodeName(%x, %d): %v", message, offset, err)
	}
	return name
}

func TestDirectNameAndOrigins(t *testing.T) {
	message := directExample()
	name := mustDecode(t, message, 0)

	if got, want := name.String(), "www.example.com"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	if got, want := name.Identity(), "www.example.com"; got != want {
		t.Fatalf("Identity() = %q, want %q", got, want)
	}
	if name.ExpandedLength() != len(message) {
		t.Fatalf("ExpandedLength() = %d, want %d", name.ExpandedLength(), len(message))
	}
	if !bytes.Equal(name.ExpandedWire(), message) {
		t.Fatalf("ExpandedWire() = %x, want %x", name.ExpandedWire(), message)
	}

	for i := range message {
		origin, ok := name.SourcePosition(i)
		if !ok || origin != i {
			t.Fatalf("SourcePosition(%d) = %d, %v; want %d, true", i, origin, ok, i)
		}
		wireByte := name.ExpandedWire()[i]
		if wireByte != message[origin] {
			t.Fatalf("expanded byte %d = %x, source byte %d = %x", i, wireByte, origin, message[origin])
		}
	}

	if got, want := name.LabelCount(), 3; got != want {
		t.Fatalf("LabelCount() = %d, want %d", got, want)
	}
	if label, ok := name.Label(1); !ok || label != "example" {
		t.Fatalf("Label(1) = %q, %v", label, ok)
	}
	if pos, ok := name.LabelSourcePosition(1); !ok || pos != 4 {
		t.Fatalf("LabelSourcePosition(1) = %d, %v; want 4, true", pos, ok)
	}
	if pos, ok := name.LabelByteSourcePosition(1, 0); !ok || pos != 5 {
		t.Fatalf("LabelByteSourcePosition(1, 0) = %d, %v; want 5, true", pos, ok)
	}
}

func TestCompressionAliasesShareIdentityAndOrigins(t *testing.T) {
	direct := directExample()
	message := append(append([]byte(nil), direct...), 0xc0, 0x00)

	m := NewMessage(message)
	first, err := m.DecodeName(0)
	if err != nil {
		t.Fatalf("decode direct name: %v", err)
	}
	alias, err := m.DecodeName(len(direct))
	if err != nil {
		t.Fatalf("decode alias: %v", err)
	}

	if !first.Equal(alias) {
		t.Fatalf("alias differs: %q != %q", first.Identity(), alias.Identity())
	}
	if first.Identity() != alias.Identity() {
		t.Fatalf("alias identity differs: %q != %q", first.Identity(), alias.Identity())
	}
	if alias.ExpandedLength() != len(direct) {
		t.Fatalf("alias expanded length = %d, want %d", alias.ExpandedLength(), len(direct))
	}

	if origin, ok := alias.SourcePosition(0); !ok || origin != 0 {
		t.Fatalf("alias byte 0 origin = %d, %v; want 0, true", origin, ok)
	}
	if origin, ok := alias.SourcePosition(len(direct) - 1); !ok || origin != len(direct)-1 {
		t.Fatalf("alias root origin = %d, %v; want %d, true", origin, ok, len(direct)-1)
	}
}

func TestSuffixAliasAndCrossNameAlias(t *testing.T) {
	direct := directExample()
	message := append(append([]byte(nil), direct...), 0xc0, 0x04, 0xc0, 0x00)

	m := NewMessage(message)
	if _, err := m.DecodeName(0); err != nil {
		t.Fatalf("decode first name: %v", err)
	}

	suffix, err := m.DecodeName(len(direct))
	if err != nil {
		t.Fatalf("decode suffix alias: %v", err)
	}
	if got, want := suffix.Identity(), "example.com"; got != want {
		t.Fatalf("suffix Identity() = %q, want %q", got, want)
	}

	whole, err := m.DecodeName(len(direct) + 2)
	if err != nil {
		t.Fatalf("decode whole alias: %v", err)
	}
	if !whole.Equal(mustDecode(t, direct, 0)) {
		t.Fatalf("whole alias identity %q differs from direct %q", whole.Identity(), "www.example.com")
	}
}

func TestCaseInsensitiveIdentityWithoutPresentationCollision(t *testing.T) {
	mixed := mustDecode(t, []byte{
		0x05, 'E', 'x', 'A', 'm', 'P',
		0x01, 'C',
		0x00,
	}, 0)
	lower := mustDecode(t, []byte{
		0x05, 'e', 'x', 'a', 'm', 'p',
		0x01, 'c',
		0x00,
	}, 0)
	if !mixed.Equal(lower) || mixed.Identity() != lower.Identity() {
		t.Fatalf("case variants have different identities: %q != %q", mixed.Identity(), lower.Identity())
	}

	oneLabel := mustDecode(t, []byte{0x03, 'a', '.', 'b', 0x00}, 0)
	twoLabels := mustDecode(t, []byte{0x01, 'a', 0x01, 'b', 0x00}, 0)
	if oneLabel.Identity() == twoLabels.Identity() {
		t.Fatalf("single label %q collided with two labels %q", oneLabel.Identity(), twoLabels.Identity())
	}
}

func TestRejectPointerIntoMiddleOfLabel(t *testing.T) {
	// The first label payload contains bytes that look like the valid name
	// "\x02\x00", but byte 1 is in the middle of the real four-byte label.
	message := []byte{
		0x04, 0x01, 0x02, 0x00, 'A',
		0x00,
		0xc0, 0x01,
	}

	m := NewMessage(message)
	if _, err := m.DecodeName(0); err != nil {
		t.Fatalf("decode first name: %v", err)
	}

	var checkCalls, businessCalls int
	err := m.Process(6, Callbacks{
		Check: func(*Name) error {
			checkCalls++
			return nil
		},
		Business: func(*Name) error {
			businessCalls++
			return nil
		},
	})
	if !errors.Is(err, ErrPointerNotLabelStart) {
		t.Fatalf("middle pointer error = %v, want ErrPointerNotLabelStart", err)
	}
	if checkCalls != 0 || businessCalls != 0 {
		t.Fatalf("callbacks = %d/%d, want 0/0", checkCalls, businessCalls)
	}
}

func TestMessageRejectsDecodeStartingInsideValidatedLabel(t *testing.T) {
	message := directExample()
	m := NewMessage(message)
	if _, err := m.DecodeName(0); err != nil {
		t.Fatalf("decode first name: %v", err)
	}

	// Offset 1 is payload byte 'w', not the start of the label at zero.
	if _, err := m.DecodeName(1); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("middle offset error = %v, want ErrInvalidOffset", err)
	}

	// Offset 4 is a validated label start and remains decodable as a suffix.
	if _, err := m.DecodeName(4); err != nil {
		t.Fatalf("valid suffix start rejected: %v", err)
	}

	// A pointer's second byte is also a middle byte, never a name start.
	aliased := append(append([]byte(nil), message...), 0xc0, 0x00)
	m2 := NewMessage(aliased)
	if _, err := m2.DecodeName(0); err != nil {
		t.Fatalf("decode prefix name: %v", err)
	}
	if _, err := m2.DecodeName(len(message)); err != nil {
		t.Fatalf("decode pointer-led name: %v", err)
	}
	if _, err := m2.DecodeName(len(message) + 1); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("pointer second byte error = %v, want ErrInvalidOffset", err)
	}
}

func TestRejectLoops(t *testing.T) {
	// Parsing the label at zero reaches the pointer at two, jumps back to the
	// label, and reaches the same pointer again.
	message := []byte{0x01, 'a', 0xc0, 0x00}
	_, err := DecodeName(message, 0)
	if !errors.Is(err, ErrCompressionLoop) {
		t.Fatalf("loop error = %v, want ErrCompressionLoop", err)
	}
}

func TestRejectBadPointersAndLabelTypes(t *testing.T) {
	tests := []struct {
		name string
		msg  []byte
		off  int
		want error
	}{
		{"forward", []byte{0x00, 0xc0, 0x02}, 1, ErrPointerForward},
		{"self", []byte{0xc0, 0x00}, 0, ErrPointerForward},
		{"out-of-range", []byte{0xc0, 0x05}, 0, ErrPointerOutOfRange},
		{"dangling", []byte{0x01, 'a', 0xc0}, 0, ErrTruncated},
		{"label-01", []byte{0x40}, 0, ErrBadLabelType},
		{"label-10", []byte{0x80}, 0, ErrBadLabelType},
		{"label-10-max", []byte{0xbf}, 0, ErrBadLabelType},
		{"short-label", []byte{0x03, 'a'}, 0, ErrTruncated},
		{"empty-message", nil, 0, ErrTruncated},
		{"negative-offset", []byte{0x00}, -1, ErrInvalidOffset},
		{"past-message", []byte{0x00}, 2, ErrInvalidOffset},
		{"offset-at-end", []byte{0x00}, 1, ErrTruncated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeName(tt.msg, tt.off)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRejectExpandedNamesOver255(t *testing.T) {
	label := append([]byte{63}, bytes.Repeat([]byte{'a'}, 63)...)
	message := append(append(append(append([]byte(nil), label...), label...), label...), label...)
	message = append(message, 0)

	_, err := DecodeName(message, 0)
	if !errors.Is(err, ErrNameTooLong) {
		t.Fatalf("error = %v, want ErrNameTooLong", err)
	}

	// The largest direct legal name is 254 bytes of labels plus the root.
	var max []byte
	for i := 0; i < 3; i++ {
		max = append(max, label...)
	}
	max = append(max, 61) // 192 + 62 = 254
	max = append(max, bytes.Repeat([]byte{'b'}, 61)...)
	max = append(max, 0)
	name, err := DecodeName(max, 0)
	if err != nil {
		t.Fatalf("255-byte name rejected: %v", err)
	}
	if name.ExpandedLength() != MaxNameWireLength {
		t.Fatalf("ExpandedLength() = %d, want %d", name.ExpandedLength(), MaxNameWireLength)
	}
}

func TestPointerExpansionCannotBypassLengthLimit(t *testing.T) {
	label := append([]byte{63}, bytes.Repeat([]byte{'a'}, 63)...)
	var message []byte
	for i := 0; i < 3; i++ {
		message = append(message, label...)
	}
	message = append(message, 0)
	second := len(message)

	// A second name adds another 63-byte label and then points at the first
	// name. Its expansion is 257 bytes.
	message = append(message, label...)
	message = append(message, 0xc0, 0x00)

	m := NewMessage(message)
	if _, err := m.DecodeName(0); err != nil {
		t.Fatalf("decode first name: %v", err)
	}
	if _, err := m.DecodeName(second); !errors.Is(err, ErrNameTooLong) {
		t.Fatalf("pointer expansion error = %v, want ErrNameTooLong", err)
	}
}

func TestRejectEverythingWithoutBusinessCall(t *testing.T) {
	tests := []struct {
		name string
		msg  []byte
		off  int
	}{
		{"empty", nil, 0},
		{"reserved-label", []byte{0x40}, 0},
		{"short-label", []byte{0x03, 'a'}, 0},
		{"dangling-pointer", []byte{0xc0}, 0},
		{"forward-pointer", []byte{0x00, 0xc0, 0x02}, 1},
		{"self-pointer", []byte{0xc0, 0x00}, 0},
		{"loop", []byte{0x01, 'a', 0xc0, 0x00}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var checkCalls, businessCalls int
			err := Process(tt.msg, tt.off, Callbacks{
				Check: func(*Name) error {
					checkCalls++
					return nil
				},
				Business: func(*Name) error {
					businessCalls++
					return nil
				},
			})
			if err == nil {
				t.Fatal("invalid input was accepted")
			}
			if checkCalls != 0 || businessCalls != 0 {
				t.Fatalf("callbacks = %d/%d, want 0/0", checkCalls, businessCalls)
			}
		})
	}
}

func TestCallbacksShareOneImmutableName(t *testing.T) {
	message := directExample()
	var checkName, businessName *Name
	var checkCalls, businessCalls int

	err := Process(message, 0, Callbacks{
		Check: func(name *Name) error {
			checkCalls++
			checkName = name
			return nil
		},
		Business: func(name *Name) error {
			businessCalls++
			businessName = name
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if checkCalls != 1 || businessCalls != 1 {
		t.Fatalf("callbacks = %d/%d, want 1/1", checkCalls, businessCalls)
	}
	if checkName == nil || checkName != businessName {
		t.Fatalf("callbacks did not receive the same *Name: %p != %p", checkName, businessName)
	}

	// Mutating the host's input and returned copies cannot change the shared
	// object observed by callbacks.
	message[1] = 'X'
	expanded := checkName.ExpandedWire()
	expanded[0] = 0xff
	positions := checkName.SourcePositions()
	positions[0] = 999
	if got := checkName.Identity(); got != "www.example.com" {
		t.Fatalf("identity changed through input/copy mutation: %q", got)
	}
	if got := checkName.ExpandedWire()[0]; got != 0x03 {
		t.Fatalf("expanded wire changed through returned slice: %x", got)
	}
	if got := checkName.SourcePositions()[0]; got != 0 {
		t.Fatalf("origin map changed through returned slice: %d", got)
	}
}

func TestSecurityRejectionMakesZeroBusinessCalls(t *testing.T) {
	message := directExample()
	deny := errors.New("denied")
	var businessCalls int

	err := Process(message, 0, Callbacks{
		Check: func(*Name) error { return deny },
		Business: func(*Name) error {
			businessCalls++
			return nil
		},
	})
	if !errors.Is(err, deny) {
		t.Fatalf("error = %v, want deny", err)
	}
	if businessCalls != 0 {
		t.Fatalf("business calls = %d, want 0", businessCalls)
	}
}

func TestMissingCallbacksAreRejected(t *testing.T) {
	message := directExample()
	callbacks := Callbacks{
		Business: func(*Name) error {
			t.Fatal("business called without a security check")
			return nil
		},
	}
	if err := Process(message, 0, callbacks); !errors.Is(err, ErrMissingCheck) {
		t.Fatalf("error = %v, want ErrMissingCheck", err)
	}

	callbacks = Callbacks{Check: func(*Name) error { return nil }}
	if err := Process(message, 0, callbacks); !errors.Is(err, ErrMissingBusiness) {
		t.Fatalf("error = %v, want ErrMissingBusiness", err)
	}
}

func TestExhaustiveShortMessagesNeverDispatchRejectedInput(t *testing.T) {
	alphabet := []byte{0x00, 0x01, 0x02, 0x3f, 0x40, 0x80, 0xc0, 0xc1, 0xff, 'a'}

	for length := 0; length <= 4; length++ {
		total := 1
		for i := 0; i < length; i++ {
			total *= len(alphabet)
		}
		for sequence := 0; sequence < total; sequence++ {
			message := make([]byte, length)
			n := sequence
			for i := length - 1; i >= 0; i-- {
				message[i] = alphabet[n%len(alphabet)]
				n /= len(alphabet)
			}

			var checkCalls, businessCalls int
			var checkName, businessName *Name
			err := Process(message, 0, Callbacks{
				Check: func(name *Name) error {
					checkCalls++
					checkName = name
					return nil
				},
				Business: func(name *Name) error {
					businessCalls++
					businessName = name
					return nil
				},
			})

			if err != nil {
				if checkCalls != 0 || businessCalls != 0 {
					t.Fatalf("rejected %x called callbacks %d/%d: %v",
						message, checkCalls, businessCalls, err)
				}
				continue
			}
			if checkCalls != 1 || businessCalls != 1 {
				t.Fatalf("accepted %x called callbacks %d/%d",
					message, checkCalls, businessCalls)
			}
			if checkName != businessName {
				t.Fatalf("accepted %x received distinct names", message)
			}
			if checkName.ExpandedLength() > MaxNameWireLength {
				t.Fatalf("accepted %x expanded to %d bytes", message, checkName.ExpandedLength())
			}
			wire := checkName.ExpandedWire()
			origins := checkName.SourcePositions()
			if len(wire) != len(origins) {
				t.Fatalf("accepted %x has %d bytes and %d origins", message, len(wire), len(origins))
			}
			for i, origin := range origins {
				if origin < 0 || origin >= len(message) {
					t.Fatalf("accepted %x origin %d out of range", message, origin)
				}
				if message[origin] != wire[i] {
					t.Fatalf("accepted %x byte %d = %x from source %x",
						message, i, wire[i], message[origin])
				}
			}
		}
	}
}

func TestEnumeratePointerTargetsAtNameBoundary(t *testing.T) {
	base := directExample() // label starts: 0, 4, 12; root start: 16
	expected := map[int]string{
		0:  "www.example.com",
		4:  "example.com",
		12: "com",
		16: ".",
	}

	for target := 0; target < 256; target++ {
		message := append(append([]byte(nil), base...), 0xc0, byte(target))
		m := NewMessage(message)
		if _, err := m.DecodeName(0); err != nil {
			t.Fatalf("decode prefix: %v", err)
		}

		var checkCalls, businessCalls int
		var businessName *Name
		err := m.Process(len(base), Callbacks{
			Check: func(*Name) error {
				checkCalls++
				return nil
			},
			Business: func(name *Name) error {
				businessCalls++
				businessName = name
				return nil
			},
		})

		if want, ok := expected[target]; ok {
			if err != nil {
				t.Errorf("target %d: valid pointer rejected: %v", target, err)
				continue
			}
			if checkCalls != 1 || businessCalls != 1 {
				t.Errorf("target %d: callbacks = %d/%d, want 1/1", target, checkCalls, businessCalls)
			}
			if got := businessName.Identity(); got != want {
				t.Errorf("target %d: identity = %q, want %q", target, got, want)
			}
			continue
		}

		if err == nil {
			t.Errorf("target %d: pointer accepted, identity %q", target, businessName.Identity())
		}
		if checkCalls != 0 || businessCalls != 0 {
			t.Errorf("target %d: rejected input called callbacks %d/%d", target, checkCalls, businessCalls)
		}
	}
}

func TestEnumerateChunkPositions(t *testing.T) {
	direct := directExample()
	full := append(append([]byte(nil), direct...), 0xc0, 0x04, 0xc0, 0x00)

	for cut := 0; cut <= len(full); cut++ {
		message := full[:cut]
		var checkCalls, businessCalls int
		err := Process(message, 0, Callbacks{
			Check: func(*Name) error {
				checkCalls++
				return nil
			},
			Business: func(*Name) error {
				businessCalls++
				return nil
			},
		})
		if err != nil {
			if checkCalls != 0 || businessCalls != 0 {
				t.Fatalf("cut %d rejected but called callbacks %d/%d", cut, checkCalls, businessCalls)
			}
			continue
		}
		if checkCalls != 1 || businessCalls != 1 {
			t.Fatalf("cut %d accepted but called callbacks %d/%d", cut, checkCalls, businessCalls)
		}
	}

	// Cut the second and third names at every byte boundary after the first
	// name. The first name is always decoded first to populate trusted starts.
	for cut := len(direct); cut <= len(full); cut++ {
		message := full[:cut]
		m := NewMessage(message)
		if _, err := m.DecodeName(0); err != nil {
			t.Fatalf("cut %d: decode first name: %v", cut, err)
		}

		var checkCalls, businessCalls int
		err := m.Process(len(direct), Callbacks{
			Check: func(*Name) error {
				checkCalls++
				return nil
			},
			Business: func(*Name) error {
				businessCalls++
				return nil
			},
		})
		if err != nil {
			if checkCalls != 0 || businessCalls != 0 {
				t.Fatalf("cut %d rejected but called callbacks %d/%d", cut, checkCalls, businessCalls)
			}
			continue
		}
		if checkCalls != 1 || businessCalls != 1 {
			t.Fatalf("cut %d accepted but called callbacks %d/%d", cut, checkCalls, businessCalls)
		}
	}
}

func TestMessageCopiesInput(t *testing.T) {
	message := directExample()
	m := NewMessage(message)
	message[1] = 'X'

	name, err := m.DecodeName(0)
	if err != nil {
		t.Fatalf("decode after input mutation: %v", err)
	}
	if got := name.String(); got != "www.example.com" {
		t.Fatalf("decoded mutated input: %q", got)
	}
}

func FuzzDecodeAndProcess(f *testing.F) {
	f.Add([]byte{}, 0)
	f.Add(directExample(), 0)
	f.Add([]byte{0x01, 'a', 0xc0, 0x00}, 0)
	f.Add([]byte{0x04, 0x01, 0x02, 0x00, 'A', 0x00, 0xc0, 0x01}, 0)

	f.Fuzz(func(t *testing.T, message []byte, offset int) {
		if len(message) > 1024 {
			message = message[:1024]
		}
		if offset < 0 || offset > len(message) {
			offset = 0
		}

		var checkCalls, businessCalls int
		var checkName, businessName *Name
		err := Process(message, offset, Callbacks{
			Check: func(name *Name) error {
				checkCalls++
				checkName = name
				return nil
			},
			Business: func(name *Name) error {
				businessCalls++
				businessName = name
				return nil
			},
		})
		if err != nil {
			if checkCalls != 0 || businessCalls != 0 {
				t.Fatalf("rejected input called callbacks: %d/%d (%v)", checkCalls, businessCalls, err)
			}
			return
		}
		if checkCalls != 1 || businessCalls != 1 {
			t.Fatalf("accepted input callbacks: %d/%d", checkCalls, businessCalls)
		}
		if checkName != businessName {
			t.Fatal("callbacks saw different names")
		}
		if checkName.ExpandedLength() > MaxNameWireLength {
			t.Fatalf("expanded length %d", checkName.ExpandedLength())
		}
		wire := checkName.ExpandedWire()
		origins := checkName.SourcePositions()
		for i, origin := range origins {
			if origin < 0 || origin >= len(message) {
				t.Fatalf("origin %d out of range", origin)
			}
			if wire[i] != message[origin] {
				t.Fatalf("wire byte %d does not match source", i)
			}
		}
	})
}

func ExampleProcess() {
	message := directExample()
	seen := 0
	_ = Process(message, 0, Callbacks{
		Check: func(name *Name) error {
			if name.Identity() != "www.example.com" {
				return fmt.Errorf("unexpected identity %q", name.Identity())
			}
			return nil
		},
		Business: func(name *Name) error {
			seen++
			fmt.Println(name.Identity())
			return nil
		},
	})
	if seen != 1 {
		fmt.Println("business was not called exactly once")
	}

	// Output: www.example.com
}
