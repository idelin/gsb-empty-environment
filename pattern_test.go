package derstrict

import "testing"

func TestDecodeWithPattern(t *testing.T) {
	data := buildValidSample()
	pattern := Pattern{
		Class: ClassUniversal,
		Tag:   TagSequence,
		Children: []Pattern{
			{Class: ClassUniversal, Tag: TagInteger},
			{Class: ClassUniversal, Tag: TagBoolean},
			{Class: ClassUniversal, Tag: TagSet, Children: []Pattern{
				{Class: ClassUniversal, Tag: TagInteger, Repeat: true},
			}},
			{Class: ClassUniversal, Tag: TagNull},
			{Class: ClassContextSpecific, Tag: 0},
		},
	}
	node, err := DecodeWithPattern(data, pattern)
	if err != nil {
		t.Fatalf("pattern decode failed: %v", err)
	}
	if node.Tag != TagSequence {
		t.Fatalf("unexpected tag: %d", node.Tag)
	}
}

func TestPatternAnyTagAndRepeat(t *testing.T) {
	data := buildValidSample()
	pattern := Pattern{
		Class:    ClassUniversal,
		Tag:      TagSequence,
		Children: []Pattern{{Class: AnyClass, Tag: AnyTag, Repeat: true}},
	}
	if _, err := DecodeWithPattern(data, pattern); err != nil {
		t.Fatalf("wildcard pattern failed: %v", err)
	}
}

func TestPatternMismatchReportsOffset(t *testing.T) {
	data := buildValidSample()

	// A wrong root pattern reports the root node offset.
	rootPattern := Pattern{Class: ClassUniversal, Tag: TagSet}
	_, err := DecodeWithPattern(data, rootPattern)
	if err == nil {
		t.Fatal("expected root pattern mismatch error")
	}
	if got := OffsetOf(err); got != 0 {
		t.Fatalf("root offset = %d, want 0", got)
	}

	// A wrong child pattern reports the offending child's identifier offset.
	pattern := Pattern{
		Class: ClassUniversal,
		Tag:   TagSequence,
		Children: []Pattern{
			{Class: ClassUniversal, Tag: TagBoolean},
			{Class: ClassUniversal, Tag: AnyTag, Repeat: true},
		},
	}
	_, err = DecodeWithPattern(data, pattern)
	if err == nil {
		t.Fatal("expected pattern mismatch error")
	}
	if got := OffsetOf(err); got != 2 {
		t.Fatalf("offset = %d, want 2 (first child identifier)", got)
	}
}

func TestPatternRejectsMalformedBeforeSchema(t *testing.T) {
	valid := buildValidSample()
	malformed := mutateAt(valid, indexOf(valid, []byte{0x01, 0x01, 0xff})+2, []byte{0x01})
	pattern := Pattern{Class: AnyClass, Tag: AnyTag}
	_, err := DecodeWithPattern(malformed, pattern)
	if err == nil {
		t.Fatal("expected strict DER error under permissive pattern")
	}
	if got := OffsetOf(err); got < 0 {
		t.Fatalf("expected violation offset, got %v", err)
	}
}

func TestPatternRepeatOnlyLast(t *testing.T) {
	pattern := Pattern{
		Class: ClassUniversal,
		Tag:   TagSequence,
		Children: []Pattern{
			{Class: ClassUniversal, Tag: TagInteger, Repeat: true},
			{Class: ClassUniversal, Tag: TagNull},
		},
	}
	_, err := DecodeWithPattern(buildValidSample(), pattern)
	if err == nil {
		t.Fatal("expected invalid-pattern error")
	}
}

func TestNestedTrailingDataRejected(t *testing.T) {
	// Trailing byte inside the outer SEQUENCE content.
	valid := buildValidSample()
	// Shorten the outer length by one and keep an extra byte dangling.
	malformed := append([]byte{}, valid[:len(valid)-1]...)
	malformed = append(malformed, 0x00, 0x00)
	_, err := Decode(malformed)
	if err == nil {
		t.Fatal("expected error for nested trailing/truncated structure")
	}
}
