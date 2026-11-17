package netsec

import (
	"bytes"
	"errors"
	"log"
	"testing"
)

func mustAdmitter(t *testing.T, fields FieldTable, pinMTIs []string) *Admitter {
	t.Helper()
	admitter, err := New(fields, pinMTIs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return admitter
}

func frame(mti string, numbers []int, body []byte) []byte {
	if len(mti) != 4 {
		panic("test MTI is not four bytes")
	}

	secondary := false
	for _, number := range numbers {
		if number < 2 || number > 128 {
			panic("test field is outside 2..128")
		}
		if number > 64 {
			secondary = true
		}
	}

	bitmapLength := 8
	if secondary {
		bitmapLength = 16
	}
	bitmap := make([]byte, bitmapLength)
	if secondary {
		bitmap[0] = 0x80
	}
	for _, number := range numbers {
		index := number - 1
		bitmap[index/8] |= 0x80 >> uint(index%8)
	}

	message := make([]byte, 0, 4+bitmapLength+len(body))
	message = append(message, mti...)
	message = append(message, bitmap...)
	message = append(message, body...)
	return message
}

func fixedBody(width int, fill byte) []byte {
	return bytes.Repeat([]byte{fill}, width)
}

func TestAdmitMessagePrimaryBitmap(t *testing.T) {
	fields := FieldTable{
		2: {Kind: Fixed, MaxLength: 19},
		3: {Kind: Fixed, MaxLength: 6},
		4: {Kind: Fixed, MaxLength: 12},
		7: {Kind: Fixed, MaxLength: 10},
	}
	admitter := mustAdmitter(t, fields, nil)

	var body []byte
	body = append(body, fixedBody(19, '2')...)
	body = append(body, fixedBody(6, '3')...)
	body = append(body, fixedBody(12, '4')...)
	body = append(body, fixedBody(10, '7')...)
	message := frame("0200", []int{2, 3, 4, 7}, body)

	result, err := admitter.AdmitMessage(message)
	if err != nil {
		t.Fatalf("AdmitMessage: %v", err)
	}
	if result.MTI != "0200" || result.BitmapOffset != 4 || result.BitmapLength != 8 {
		t.Fatalf("unexpected result header: %+v", result)
	}
	if result.SecondaryBitmap {
		t.Fatal("secondary bitmap reported for a primary-only message")
	}
	if len(result.Fields) != 4 {
		t.Fatalf("got %d fields, want 4", len(result.Fields))
	}
	for i, number := range []int{2, 3, 4, 7} {
		field, ok := result.Field(number)
		if !ok {
			t.Fatalf("field %d missing", number)
		}
		if field.Offset != result.Fields[i].Offset || field.Length != fields[number].MaxLength {
			t.Fatalf("field %d has wrong geometry: %+v", number, field)
		}
		if got := field.Bytes(message); len(got) != field.Length || got[0] != byte('0'+number%10) {
			t.Fatalf("field %d bytes are wrong", number)
		}
	}
}

func TestAdmitMessageSecondaryBitmap(t *testing.T) {
	fields := FieldTable{
		2:  {Kind: Fixed, MaxLength: 19},
		70: {Kind: Fixed, MaxLength: 3},
		90: {Kind: Fixed, MaxLength: 42},
	}
	admitter := mustAdmitter(t, fields, nil)

	var body []byte
	body = append(body, fixedBody(19, 'a')...)
	body = append(body, fixedBody(3, 'b')...)
	body = append(body, fixedBody(42, 'c')...)
	message := frame("0200", []int{2, 70, 90}, body)

	result, err := admitter.AdmitMessage(message)
	if err != nil {
		t.Fatalf("AdmitMessage: %v", err)
	}
	if !result.SecondaryBitmap || result.BitmapLength != 16 {
		t.Fatalf("secondary bitmap not admitted: %+v", result)
	}
	if len(result.Fields) != 3 {
		t.Fatalf("got %d fields, want 3", len(result.Fields))
	}
	if field, ok := result.Field(90); !ok || field.Length != 42 {
		t.Fatalf("field 90 was not admitted correctly: %+v, %v", field, ok)
	}
}

func TestSecondaryBitmapOnlyReadWhenBitOneIsSet(t *testing.T) {
	admitter := mustAdmitter(t, FieldTable{2: {Kind: Fixed, MaxLength: 1}}, nil)

	// Bit 1 is clear. The following eight bytes must not be treated as a
	// secondary bitmap even though they are present.
	message := append([]byte("0200"), make([]byte, 16)...)
	_, err := admitter.AdmitMessage(message)
	if !errors.Is(err, ErrTrailingData) {
		t.Fatalf("got %v, want ErrTrailingData", err)
	}

	// Bit 1 is set but only one bitmap is present.
	truncated := []byte{'0', '2', '0', '0', 0x80, 0, 0, 0, 0, 0, 0, 0}
	_, err = admitter.AdmitMessage(truncated)
	if !errors.Is(err, ErrTruncatedBitmap) {
		t.Fatalf("got %v, want ErrTruncatedBitmap", err)
	}
}

func TestUndefinedBitmapFields(t *testing.T) {
	admitter := mustAdmitter(t, FieldTable{2: {Kind: Fixed, MaxLength: 1}}, nil)

	cases := []struct {
		name    string
		numbers []int
	}{
		{name: "primary", numbers: []int{17}},
		{name: "secondary", numbers: []int{90}},
		{name: "first secondary bit", numbers: []int{65}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message := frame("0200", tc.numbers, []byte("x"))
			_, err := admitter.AdmitMessage(message)
			if !errors.Is(err, ErrUndefinedField) {
				t.Fatalf("got %v, want ErrUndefinedField", err)
			}
			var admissionError *AdmissionError
			if !errors.As(err, &admissionError) {
				t.Fatalf("got %T, want *AdmissionError", err)
			}
			if admissionError.Field != tc.numbers[0] {
				t.Fatalf("error names field %d, want %d", admissionError.Field, tc.numbers[0])
			}
		})
	}
}

func TestVariableLengthLimits(t *testing.T) {
	fields := FieldTable{
		2:  {Kind: LLVAR, MaxLength: 10},
		3:  {Kind: LLVAR, MaxLength: 4},
		55: {Kind: LLLVAR, MaxLength: 50},
		56: {Kind: LLLVAR, MaxLength: 999},
	}
	admitter := mustAdmitter(t, fields, nil)

	cases := []struct {
		name   string
		number int
		body   []byte
		want   error
	}{
		{
			name:   "LLVAR exceeds remaining",
			number: 2,
			body:   append([]byte("09"), fixedBody(5, 'x')...),
			want:   ErrLengthExceedsRemaining,
		},
		{
			name:   "LLVAR exceeds field maximum",
			number: 3,
			body:   append([]byte("05"), fixedBody(5, 'x')...),
			want:   ErrFieldLimitExceeded,
		},
		{
			name:   "LLLVAR exceeds field maximum",
			number: 55,
			body:   append([]byte("051"), fixedBody(51, 'x')...),
			want:   ErrFieldLimitExceeded,
		},
		{
			name:   "LLLVAR exceeds remaining",
			number: 56,
			body:   append([]byte("100"), fixedBody(50, 'x')...),
			want:   ErrLengthExceedsRemaining,
		},
		{
			name:   "LLVAR non-decimal prefix",
			number: 2,
			body:   []byte("0Ax"),
			want:   ErrInvalidLengthPrefix,
		},
		{
			name:   "LLLVAR truncated prefix",
			number: 55,
			body:   []byte("05"),
			want:   ErrTruncatedLengthPrefix,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message := frame("0200", []int{tc.number}, tc.body)
			_, err := admitter.AdmitMessage(message)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if tc.want == ErrLengthExceedsRemaining || tc.want == ErrFieldLimitExceeded {
				if !errors.Is(err, ErrLengthExceeded) {
					t.Fatalf("length error does not wrap ErrLengthExceeded: %v", err)
				}
			}
		})
	}
}

func TestVariableLengthSuccess(t *testing.T) {
	fields := FieldTable{
		2:  {Kind: LLVAR, MaxLength: 10},
		55: {Kind: LLLVAR, MaxLength: 50},
	}
	admitter := mustAdmitter(t, fields, nil)
	body := append([]byte("04"), []byte("1234")...)
	body = append(body, []byte("005")...)
	body = append(body, []byte("abcde")...)
	message := frame("0200", []int{2, 55}, body)

	result, err := admitter.AdmitMessage(message)
	if err != nil {
		t.Fatalf("AdmitMessage: %v", err)
	}
	field2, _ := result.Field(2)
	field55, _ := result.Field(55)
	if string(field2.Bytes(message)) != "1234" || string(field55.Bytes(message)) != "abcde" {
		t.Fatalf("unexpected values: %q %q", field2.Bytes(message), field55.Bytes(message))
	}
}

func TestField52AuthorizationAndLength(t *testing.T) {
	fields := FieldTable{
		2:  {Kind: Fixed, MaxLength: 19},
		52: {Kind: Fixed, MaxLength: 8},
	}
	admitter := mustAdmitter(t, fields, []string{"0100", "0120"})

	t.Run("unauthorized message type", func(t *testing.T) {
		message := frame("0200", []int{52}, fixedBody(8, 'p'))
		_, err := admitter.AdmitMessage(message)
		if !errors.Is(err, ErrPINBlockNotAllowed) {
			t.Fatalf("got %v, want ErrPINBlockNotAllowed", err)
		}
	})

	t.Run("authorization precedes length parsing", func(t *testing.T) {
		message := frame("0200", []int{52}, fixedBody(7, 'p'))
		_, err := admitter.AdmitMessage(message)
		if !errors.Is(err, ErrPINBlockNotAllowed) {
			t.Fatalf("got %v, want ErrPINBlockNotAllowed", err)
		}
	})

	t.Run("authorized fixed field", func(t *testing.T) {
		message := frame("0100", []int{52}, fixedBody(8, 'p'))
		result, err := admitter.AdmitMessage(message)
		if err != nil {
			t.Fatalf("AdmitMessage: %v", err)
		}
		field, ok := result.Field(52)
		if !ok || field.Length != 8 {
			t.Fatalf("field 52 geometry: %+v, %v", field, ok)
		}
	})

	llvarFields := FieldTable{
		52: {Kind: LLVAR, MaxLength: 16},
	}
	llvarAdmitter := mustAdmitter(t, llvarFields, []string{"0100"})
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{name: "seven bytes", body: append([]byte("07"), fixedBody(7, 'p')...)},
		{name: "nine bytes", body: append([]byte("09"), fixedBody(9, 'p')...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := frame("0100", []int{52}, tc.body)
			_, err := llvarAdmitter.AdmitMessage(message)
			if !errors.Is(err, ErrPINBlockLength) {
				t.Fatalf("got %v, want ErrPINBlockLength", err)
			}
		})
	}

	t.Run("authorized variable field with eight bytes", func(t *testing.T) {
		message := frame("0100", []int{52}, append([]byte("08"), fixedBody(8, 'p')...))
		if _, err := llvarAdmitter.AdmitMessage(message); err != nil {
			t.Fatalf("AdmitMessage: %v", err)
		}
	})
}

func TestField52RequiresRegistration(t *testing.T) {
	admitter := mustAdmitter(t, FieldTable{52: {Kind: Fixed, MaxLength: 8}}, nil)
	message := frame("0100", []int{52}, fixedBody(8, 'p'))
	_, err := admitter.AdmitMessage(message)
	if !errors.Is(err, ErrPINBlockNotAllowed) {
		t.Fatalf("got %v, want ErrPINBlockNotAllowed", err)
	}
}

func TestTrailingData(t *testing.T) {
	fields := FieldTable{2: {Kind: Fixed, MaxLength: 1}}
	message := frame("0200", []int{2}, []byte("ab"))

	_, err := mustAdmitter(t, fields, nil).AdmitMessage(message)
	if !errors.Is(err, ErrTrailingData) {
		t.Fatalf("got %v, want ErrTrailingData", err)
	}

	lenient, err := NewWithConfig(Config{Fields: fields, AllowTrailing: true})
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	result, err := lenient.AdmitMessage(message)
	if err != nil {
		t.Fatalf("AdmitMessage: %v", err)
	}
	if result.TrailingLength != 1 {
		t.Fatalf("TrailingLength = %d, want 1", result.TrailingLength)
	}
}

func TestAdmitSeparateMTIAndBitmap(t *testing.T) {
	admitter := mustAdmitter(t, FieldTable{2: {Kind: Fixed, MaxLength: 2}}, nil)
	data := frame("0200", []int{2}, []byte("ok"))[4:]

	result, err := admitter.Admit("0200", data)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	field, ok := result.Field(2)
	if !ok || string(field.Bytes(data)) != "ok" {
		t.Fatalf("field 2: %+v, %v", field, ok)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	cases := []struct {
		name   string
		fields FieldTable
		mtis   []string
	}{
		{name: "field one", fields: FieldTable{1: {Kind: Fixed, MaxLength: 1}}},
		{name: "field too large", fields: FieldTable{129: {Kind: Fixed, MaxLength: 1}}},
		{name: "field zero", fields: FieldTable{0: {Kind: Fixed, MaxLength: 1}}},
		{name: "unknown kind", fields: FieldTable{2: {Kind: FieldKind(9), MaxLength: 1}}},
		{name: "zero length", fields: FieldTable{2: {Kind: Fixed, MaxLength: 0}}},
		{name: "LLVAR too wide", fields: FieldTable{2: {Kind: LLVAR, MaxLength: 100}}},
		{name: "LLLVAR too wide", fields: FieldTable{2: {Kind: LLLVAR, MaxLength: 1000}}},
		{name: "short MTI", mtis: []string{"100"}},
		{name: "non-numeric MTI", mtis: []string{"01A0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.fields, tc.mtis); !errors.Is(err, ErrInvalidFieldTable) && !errors.Is(err, ErrInvalidMTI) {
				t.Fatalf("got %v, want configuration error", err)
			}
		})
	}
}

func TestInputErrors(t *testing.T) {
	admitter := mustAdmitter(t, nil, nil)
	for _, tc := range []struct {
		name    string
		message []byte
		want    error
	}{
		{name: "empty", message: nil, want: ErrTruncatedMTI},
		{name: "short MTI", message: []byte("020"), want: ErrTruncatedMTI},
		{name: "non-numeric MTI", message: []byte("02A0"), want: ErrInvalidMTI},
		{name: "short primary bitmap", message: []byte("0200"), want: ErrTruncatedBitmap},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := admitter.AdmitMessage(tc.message)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNoLogOutputOnRejection(t *testing.T) {
	var captured bytes.Buffer
	original := log.Writer()
	log.SetOutput(&captured)
	defer log.SetOutput(original)

	admitter := mustAdmitter(t, FieldTable{52: {Kind: Fixed, MaxLength: 8}}, []string{"0100"})
	messages := [][]byte{
		frame("0200", []int{17}, []byte("secret")),
		frame("0200", []int{52}, fixedBody(8, 'p')),
		frame("0100", []int{52}, fixedBody(7, 'p')),
	}
	for _, message := range messages {
		if _, err := admitter.AdmitMessage(message); err == nil {
			t.Fatal("expected rejection")
		}
	}
	if captured.Len() != 0 {
		t.Fatalf("package wrote to log: %q", captured.String())
	}
}

func FuzzAdmitMessage(f *testing.F) {
	f.Add([]byte("0200"))
	f.Add(frame("0200", []int{2}, fixedBody(19, '1')))
	f.Add(frame("0200", []int{90}, []byte("x")))
	f.Add(frame("0200", []int{2}, append([]byte("99"), []byte("short")...)))
	f.Add(frame("0100", []int{52}, fixedBody(7, 'p')))

	fields := FieldTable{
		2:  {Kind: LLVAR, MaxLength: 19},
		3:  {Kind: Fixed, MaxLength: 6},
		4:  {Kind: Fixed, MaxLength: 12},
		52: {Kind: Fixed, MaxLength: 8},
		55: {Kind: LLLVAR, MaxLength: 255},
		70: {Kind: Fixed, MaxLength: 3},
		90: {Kind: Fixed, MaxLength: 42},
	}
	admitter, err := New(fields, []string{"0100", "0120"})
	if err != nil {
		f.Fatalf("New: %v", err)
	}

	f.Fuzz(func(t *testing.T, message []byte) {
		result, err := admitter.AdmitMessage(message)
		if err != nil {
			return
		}

		lastEnd := result.BitmapOffset + result.BitmapLength
		seen := make(map[int]struct{}, len(result.Fields))
		for _, field := range result.Fields {
			if field.Number < 2 || field.Number > 128 {
				t.Fatalf("admitted out-of-range field %d", field.Number)
			}
			if _, duplicate := seen[field.Number]; duplicate {
				t.Fatalf("admitted duplicate field %d", field.Number)
			}
			seen[field.Number] = struct{}{}
			if field.Offset < lastEnd || field.Length < 0 || field.Offset+field.Length > len(message) {
				t.Fatalf("field %d has invalid geometry: %+v", field.Number, field)
			}
			lastEnd = field.Offset + field.Length
		}
		if lastEnd+result.TrailingLength != len(message) {
			t.Fatalf("field walk does not cover input")
		}
	})
}

func TestFieldBytesRejectsOutOfRange(t *testing.T) {
	field := FieldValue{Number: 2, Offset: 2, Length: 4}
	if field.Bytes([]byte("abc")) != nil {
		t.Fatal("out-of-range field returned bytes")
	}
	field.Offset = -1
	if field.Bytes([]byte("abcdef")) != nil {
		t.Fatal("negative offset returned bytes")
	}
}
