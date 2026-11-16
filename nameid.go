package netsec

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrNameIDNotFound              = errors.New("netsec: NameID element not found")
	ErrNameIDMultiple              = errors.New("netsec: multiple NameID elements")
	ErrNameIDComment               = errors.New("netsec: NameID contains a comment")
	ErrNameIDCDATA                 = errors.New("netsec: NameID contains CDATA")
	ErrNameIDMultipleText          = errors.New("netsec: NameID contains multiple text nodes")
	ErrNameIDNestedElement         = errors.New("netsec: NameID contains a nested element")
	ErrNameIDProcessingInstruction = errors.New("netsec: NameID contains a processing instruction")
	ErrNameIDWhitespace            = errors.New("netsec: NameID has leading or trailing whitespace")
	ErrNameIDEmpty                 = errors.New("netsec: NameID is empty")
)

// ReadNameID extracts the single NameID element from a SAML XML document. The
// NameID must contain exactly one plain text node. Comments, CDATA sections,
// nested elements, processing instructions, and multiple text nodes are
// rejected so that different XML consumers cannot disagree on the value.
func ReadNameID(data []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var stack []xml.Name
	var value strings.Builder
	var textNodes int
	var nameIDDepth int
	var foundNameID bool
	var valueRead bool
	var seenRoot bool
	var lastOffset int64

	for {
		token, err := decoder.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrMalformedXML, err)
		}

		offset := decoder.InputOffset()
		raw := data[lastOffset:offset]
		lastOffset = offset

		switch token := token.(type) {
		case xml.StartElement:
			if len(stack) == 0 {
				if seenRoot {
					return "", fmt.Errorf("%w: multiple root elements", ErrMalformedXML)
				}
				seenRoot = true
			}
			if nameIDDepth != 0 {
				return "", ErrNameIDNestedElement
			}
			if token.Name.Local == "NameID" {
				if foundNameID {
					return "", ErrNameIDMultiple
				}
				foundNameID = true
				nameIDDepth = len(stack) + 1
				textNodes = 0
				value.Reset()
			}
			stack = append(stack, token.Name)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1] != token.Name {
				return "", fmt.Errorf("%w: element mismatch", ErrMalformedXML)
			}
			if nameIDDepth != 0 && len(stack) == nameIDDepth {
				if textNodes == 0 || value.Len() == 0 {
					return "", ErrNameIDEmpty
				}
				if textNodes > 1 {
					return "", ErrNameIDMultipleText
				}
				nameID := value.String()
				if nameID != strings.TrimSpace(nameID) {
					return "", ErrNameIDWhitespace
				}
				valueRead = true
				nameIDDepth = 0
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if nameIDDepth != 0 {
				if bytes.Contains(raw, []byte("<![CDATA[")) {
					return "", ErrNameIDCDATA
				}
				textNodes++
				if textNodes > 1 {
					return "", ErrNameIDMultipleText
				}
				value.Write(token)
				continue
			}
			if len(stack) == 0 && !isXMLWhitespace(token) {
				return "", fmt.Errorf("%w: text outside the root element", ErrMalformedXML)
			}
		case xml.Comment:
			if nameIDDepth != 0 {
				return "", ErrNameIDComment
			}
		case xml.ProcInst:
			if nameIDDepth != 0 {
				return "", ErrNameIDProcessingInstruction
			}
		case xml.Directive:
			return "", fmt.Errorf("%w: DTD declarations are not accepted", ErrMalformedXML)
		}
	}

	if len(stack) != 0 {
		return "", fmt.Errorf("%w: unclosed element", ErrMalformedXML)
	}
	if !seenRoot {
		return "", fmt.Errorf("%w: no root element", ErrMalformedXML)
	}
	if !foundNameID {
		return "", ErrNameIDNotFound
	}
	if nameIDDepth != 0 || !valueRead {
		return "", fmt.Errorf("%w: unterminated NameID", ErrMalformedXML)
	}
	return value.String(), nil
}

// ExtractNameID is an alias for ReadNameID.
func ExtractNameID(data []byte) (string, error) {
	return ReadNameID(data)
}

// ParseNameID is an alias for ReadNameID.
func ParseNameID(data []byte) (string, error) {
	return ReadNameID(data)
}
