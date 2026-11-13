package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func TestValidCurrentSignatureAccepted(t *testing.T) {
	data := buildSignedPDF(t, nil)
	doc, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Signatures) != 1 {
		t.Fatalf("got %d signatures, want 1", len(doc.Signatures))
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func TestAppendedObjectAfterSignedRangeRejected(t *testing.T) {
	base := buildSignedPDF(t, nil)
	data, err := AppendObject(base, []byte("4 0 obj\n<< /Type /Metadata >>\nendobj\n"))
	if err != nil {
		t.Fatalf("AppendObject: %v", err)
	}

	if !bytes.Equal(data[:len(base)], base) {
		t.Fatal("append modified the signed prefix")
	}
	err = Validate(data)
	if err == nil {
		t.Fatal("signature with a stale byte range was accepted")
	}
	if !errors.Is(err, ErrUncoveredBytes) {
		t.Fatalf("got %v, want ErrUncoveredBytes", err)
	}
}

func TestUpdatedRangeCoveringCurrentRevisionAccepted(t *testing.T) {
	trailing := []byte("4 0 obj\n<< /Type /Metadata >>\nendobj\n")
	data := buildSignedPDF(t, trailing)
	if err := Validate(data); err != nil {
		t.Fatalf("signature whose range covers the current revision was rejected: %v", err)
	}
}

func TestHoleOutsideSignatureValueRejected(t *testing.T) {
	data := buildSignedPDF(t, nil)
	doc, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sig := doc.Signatures[0]
	fileSize := int64(len(data))

	// The hole starts before /Contents, so it omits bytes that are not part
	// of the signature value.
	setByteRangeOccurrence(t, data, 0, []int64{
		0,
		sig.ContentsSpan.Start - 4,
		sig.ContentsSpan.End,
		fileSize - sig.ContentsSpan.End,
	})

	err = Validate(data)
	if err == nil {
		t.Fatal("signature with a hole outside /Contents was accepted")
	}
	if !errors.Is(err, ErrUncoveredBytes) {
		t.Fatalf("got %v, want ErrUncoveredBytes", err)
	}
}

func TestGapNotAlignedWithContentsRejected(t *testing.T) {
	data := buildSignedPDF(t, nil)
	doc, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sig := doc.Signatures[0]
	fileSize := int64(len(data))

	// Cover the /Contents bytes but leave the four bytes before it out.
	setByteRangeOccurrence(t, data, 0, []int64{
		0,
		sig.ContentsSpan.Start - 4,
		sig.ContentsSpan.Start,
		fileSize - sig.ContentsSpan.Start,
	})

	err = Validate(data)
	if err == nil {
		t.Fatal("misaligned gap was accepted")
	}
	if !errors.Is(err, ErrUncoveredBytes) {
		t.Fatalf("got %v, want ErrUncoveredBytes", err)
	}
}

func TestNewerSignatureCoveringCurrentRevisionAccepted(t *testing.T) {
	base := buildSignedPDF(t, nil)
	withSecond := appendSecondSignature(t, base)

	doc, err := Parse(withSecond)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Signatures) != 2 {
		t.Fatalf("got %d signatures, want 2", len(doc.Signatures))
	}

	results := doc.Results()
	if results[0].Err == nil {
		t.Fatal("older signature unexpectedly covers the current revision")
	}
	if results[1].Err != nil {
		t.Fatalf("newer signature does not cover current revision: %v", results[1].Err)
	}
	if len(doc.CurrentSignatures()) != 1 {
		t.Fatalf("got %d current signatures, want 1", len(doc.CurrentSignatures()))
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("document with a current signature was rejected: %v", err)
	}
}

func TestValidateByteRange(t *testing.T) {
	const size = 100
	contents := Span{Start: 40, End: 50}

	tests := []struct {
		name      string
		byteRange []int64
		contents  Span
		wantErr   error
	}{
		{
			name:      "exact coverage",
			byteRange: []int64{0, 40, 50, 50},
			contents:  contents,
		},
		{
			name:      "gap inside signature value",
			byteRange: []int64{0, 42, 48, 52},
			contents:  contents,
		},
		{
			name:      "gap is inner value",
			byteRange: []int64{0, 41, 49, 51},
			contents:  Span{Start: 40, End: 50},
		},
		{
			name:      "no gap",
			byteRange: []int64{0, 40, 40, 60},
			contents:  contents,
		},
		{
			name:      "trailing bytes uncovered",
			byteRange: []int64{0, 40, 50, 40},
			contents:  contents,
			wantErr:   ErrUncoveredBytes,
		},
		{
			name:      "leading bytes uncovered",
			byteRange: []int64{5, 35, 50, 50},
			contents:  contents,
			wantErr:   ErrUncoveredBytes,
		},
		{
			name:      "gap before value",
			byteRange: []int64{0, 35, 40, 60},
			contents:  contents,
			wantErr:   ErrUncoveredBytes,
		},
		{
			name:      "two holes",
			byteRange: []int64{0, 10, 20, 10, 40, 60},
			contents:  contents,
			wantErr:   ErrUncoveredBytes,
		},
		{
			name:      "out of bounds",
			byteRange: []int64{0, 40, 50, 51},
			contents:  contents,
			wantErr:   ErrInvalidByteRange,
		},
		{
			name:      "negative length",
			byteRange: []int64{0, -1, 50, 50},
			contents:  contents,
			wantErr:   ErrInvalidByteRange,
		},
		{
			name:      "overlap",
			byteRange: []int64{0, 60, 50, 50},
			contents:  contents,
			wantErr:   ErrInvalidByteRange,
		},
		{
			name:      "odd array",
			byteRange: []int64{0, 40, 50},
			contents:  contents,
			wantErr:   ErrInvalidByteRange,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateByteRangeSpan(size, test.byteRange, test.contents)
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateByteRangeSpan: %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("got %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestNoSignaturesRejected(t *testing.T) {
	err := Validate([]byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n"))
	if !errors.Is(err, ErrNoSignatures) {
		t.Fatalf("got %v, want ErrNoSignatures", err)
	}
}

func TestParserSkipsStreamContainingFakeSignature(t *testing.T) {
	data := []byte("%PDF-1.7\n" +
		"10 0 obj\n<< /Length 70 >>\nstream\n" +
		"/ByteRange [0 10 20 30] /Contents <00112233445566778899aabbccddeeff>\n" +
		"endstream\nendobj\n%%EOF\n")
	doc, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Signatures) != 0 {
		t.Fatalf("got %d signatures, want 0", len(doc.Signatures))
	}
}

func TestIndirectByteRangeAndContentsResolved(t *testing.T) {
	const contentsHex = "00112233445566778899aabbccddeeff"
	data := []byte(fmt.Sprintf(`%%PDF-1.7
3 0 obj
<< /Type /Sig /ByteRange 4 0 R /Contents 5 0 R >>
endobj
4 0 obj
[0000000000 0000000000 0000000000 0000000000]
endobj
5 0 obj
<%s>
endobj
%%%%EOF
`, contentsHex))

	contentsStart := int64(bytes.Index(data, []byte("<"+contentsHex)))
	contentsEnd := contentsStart + int64(len(contentsHex)) + 2
	arrayStart := bytes.Index(data, []byte("4 0 obj\n[")) + len("4 0 obj\n[")
	setByteRangeAt(t, data, arrayStart, []int64{
		0,
		contentsStart,
		contentsEnd,
		int64(len(data)) - contentsEnd,
	})

	if err := Validate(data); err != nil {
		t.Fatalf("indirect signature values rejected: %v", err)
	}
}

func TestMalformedSignatureRejected(t *testing.T) {
	data := []byte("%PDF-1.7\n" +
		"3 0 obj\n<< /Type /Sig /Contents <0011> >>\nendobj\n" +
		"trailer\n<< /Root 1 0 R >>\n%%EOF\n")
	err := Validate(data)
	if !errors.Is(err, ErrMalformedSignature) {
		t.Fatalf("got %v, want ErrMalformedSignature", err)
	}
}

func TestAppendObjectBody(t *testing.T) {
	base := buildSignedPDF(t, nil)
	updated, err := AppendObjectBody(base, 4, 0, []byte("<< /Type /Metadata >>"))
	if err != nil {
		t.Fatalf("AppendObjectBody: %v", err)
	}
	if len(updated) <= len(base) {
		t.Fatal("object was not appended")
	}
	if !bytes.Equal(updated[:len(base)], base) {
		t.Fatal("append modified the signed prefix")
	}
	if err := Validate(updated); !errors.Is(err, ErrUncoveredBytes) {
		t.Fatalf("got %v, want ErrUncoveredBytes", err)
	}
}

func buildSignedPDF(t *testing.T, extra []byte) []byte {
	t.Helper()
	const contentsHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	data := []byte(fmt.Sprintf(`%%PDF-1.7
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Kids [] /Count 0 >>
endobj
3 0 obj
<< /Type /Sig
   /Filter /Adobe.PPKLite
   /SubFilter /adbe.pkcs7.detached
   /ByteRange [0000000000 0000000000 0000000000 0000000000]
   /Contents <%s>
>>
endobj
trailer
<< /Size 4 /Root 1 0 R >>
startxref
0
%%%%EOF
`, contentsHex))
	data = append(data, extra...)

	contentsStart := int64(bytes.Index(data, []byte("/Contents <")) + len("/Contents "))
	contentsEnd := int64(bytes.IndexByte(data[contentsStart:], '>')) + contentsStart + 1
	setByteRange(t, data, 0, []int64{
		0,
		contentsStart,
		contentsEnd,
		int64(len(data)) - contentsEnd,
	})
	return data
}

func appendSecondSignature(t *testing.T, data []byte) []byte {
	t.Helper()
	const contentsHex = "ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100"
	object := []byte(fmt.Sprintf("4 0 obj\n<< /Type /Sig /ByteRange [0000000000 0000000000 0000000000 0000000000] /Contents <%s> >>\nendobj\n", contentsHex))
	updated, err := AppendObject(data, object)
	if err != nil {
		t.Fatalf("AppendObject: %v", err)
	}

	prefix := bytes.LastIndex(updated, []byte("/Contents <"))
	contentsStart := int64(prefix + len("/Contents "))
	contentsEnd := int64(bytes.IndexByte(updated[contentsStart:], '>')) + contentsStart + 1
	setByteRangeOccurrence(t, updated, 1, []int64{
		0,
		contentsStart,
		contentsEnd,
		int64(len(updated)) - contentsEnd,
	})
	return updated
}

func setByteRangeOccurrence(t *testing.T, data []byte, occurrence int, values []int64) {
	t.Helper()
	setByteRange(t, data, occurrence, values)
}

func setByteRange(t *testing.T, data []byte, occurrence int, values []int64) {
	t.Helper()
	if len(values) != 4 {
		t.Fatalf("need four byte range values, got %d", len(values))
	}

	searchFrom := 0
	start := -1
	for i := 0; i <= occurrence; i++ {
		index := bytes.Index(data[searchFrom:], []byte("/ByteRange ["))
		if index < 0 {
			t.Fatalf("byte range %d not found", occurrence)
		}
		start = searchFrom + index + len("/ByteRange [")
		searchFrom = start
	}

	setByteRangeAt(t, data, start, values)
}

func setByteRangeAt(t *testing.T, data []byte, start int, values []int64) {
	t.Helper()
	for i, value := range values {
		fieldStart := start + i*11
		fragment := fmt.Sprintf("%010d", value)
		if fieldStart+len(fragment) > len(data) {
			t.Fatalf("byte range field %d is outside the file", i)
		}
		if !isDecimalField(data[fieldStart : fieldStart+10]) {
			t.Fatalf("byte range field %d does not have a ten-byte slot", i)
		}
		copy(data[fieldStart:fieldStart+10], fragment)
	}
}

func isDecimalField(field []byte) bool {
	if len(field) != 10 {
		return false
	}
	for _, c := range field {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
