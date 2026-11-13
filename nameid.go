package netsec

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// Errors returned by the NameID reader. Callers can use errors.Is to
// distinguish the reason a NameID was rejected.
var (
	ErrNameIDNotFound       = errors.New("netsec: no NameID element")
	ErrMultipleNameIDs      = errors.New("netsec: multiple NameID elements")
	ErrEmptyNameID          = errors.New("netsec: NameID has no text")
	ErrNameIDComment        = errors.New("netsec: NameID contains an XML comment")
	ErrNameIDCDATA          = errors.New("netsec: NameID contains CDATA")
	ErrNameIDMultipleText   = errors.New("netsec: NameID contains multiple text nodes")
	ErrNameIDChildElement   = errors.New("netsec: NameID contains an element")
	ErrNameIDProcessingInst = errors.New("netsec: NameID contains a processing instruction")
	ErrNameIDDirective      = errors.New("netsec: NameID contains a directive")
	ErrNameIDWhitespace     = errors.New("netsec: NameID has leading or trailing whitespace")
)

// ParseNameID extracts the value of the first NameID element in data.
//
// It accepts a complete XML document as well as an XML fragment containing a
// NameID. The NameID must contain exactly one non-CDATA text node. Comments,
// CDATA sections, child elements, processing instructions, and directives
// inside the NameID are always rejected. This prevents parsers from seeing only
// the first text node and truncating a value such as
//
//	admin@victim.com<!-- padding -->.evil.com
//
// into admin@victim.com. Leading and trailing XML whitespace in the NameID
// value is also rejected.
func ParseNameID(data []byte) (string, error) {
	// XML parsers normalize literal line endings before constructing character
	// data. encoding/xml does not perform that normalization, so do it here to
	// keep the result consistent with XML semantics.
	data = normalizeLineEndings(data)

	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true

	var (
		inside      bool
		found       bool
		nameIDDepth int
		depth       int
		textCount   int
		text        strings.Builder
		prevOffset  int64
	)

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("netsec: parsing XML: %w", err)
		}

		endOffset := dec.InputOffset()
		raw := data[prevOffset:endOffset]
		prevOffset = endOffset

		switch t := tok.(type) {
		case xml.StartElement:
			if inside {
				return "", fmt.Errorf("%w: <%s>", ErrNameIDChildElement, t.Name.Local)
			}
			if t.Name.Local == "NameID" {
				if found {
					return "", ErrMultipleNameIDs
				}
				found = true
				inside = true
				nameIDDepth = depth
			}
			depth++

		case xml.EndElement:
			if inside && depth-1 == nameIDDepth {
				if err := validateNameIDValue(textCount, text.String()); err != nil {
					return "", err
				}
				inside = false
			}
			depth--

		case xml.CharData:
			if !inside {
				continue
			}
			// RawToken folds CDATA sections into CharData. Inspect the raw
			// input covered by this token so CDATA cannot masquerade as plain
			// text.
			if bytes.HasPrefix(raw, []byte("<![CDATA[")) {
				return "", ErrNameIDCDATA
			}
			textCount++
			if textCount > 1 {
				return "", ErrNameIDMultipleText
			}
			text.Write([]byte(t))

		case xml.Comment:
			if inside {
				return "", ErrNameIDComment
			}

		case xml.ProcInst:
			if inside {
				return "", fmt.Errorf("%w: %s", ErrNameIDProcessingInst, t.Target)
			}

		case xml.Directive:
			if inside {
				return "", ErrNameIDDirective
			}
		}
	}

	if !found {
		return "", ErrNameIDNotFound
	}
	if inside {
		return "", fmt.Errorf("netsec: parsing XML: unexpected EOF in NameID")
	}
	return text.String(), nil
}

// ReadNameID is the stream-oriented form of ParseNameID.
func ReadNameID(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("netsec: reading XML: %w", err)
	}
	return ParseNameID(data)
}

func validateNameIDValue(textCount int, value string) error {
	switch {
	case textCount == 0:
		return ErrEmptyNameID
	case textCount > 1:
		return ErrNameIDMultipleText
	case strings.TrimFunc(value, unicode.IsSpace) != value:
		return ErrNameIDWhitespace
	default:
		return nil
	}
}

func normalizeLineEndings(data []byte) []byte {
	if !bytes.Contains(data, []byte{'\r'}) {
		return data
	}
	return bytes.ReplaceAll(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
}
