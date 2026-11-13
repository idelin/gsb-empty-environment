package netsec

import (
	"fmt"
	"strings"
)

type templateSegment struct {
	literal string
	claim   ClaimName
	isClaim bool
}

type parsedTemplate struct {
	raw            string
	segments       []templateSegment
	placeholders   int
	literalLength  int
	distinctClaims []ClaimName
}

func parseTemplate(template string) (*parsedTemplate, error) {
	segments := make([]templateSegment, 0, 8)
	claimsSeen := make(map[ClaimName]struct{})
	literalLength := 0
	placeholderCount := 0

	for position := 0; position < len(template); {
		if template[position] == '{' {
			end := strings.IndexByte(template[position+1:], '}')
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated placeholder at byte %d", ErrInvalidConfig, position)
			}
			end += position + 1
			name := template[position+1 : end]
			claim, ok := supportedClaim(name)
			if !ok {
				return nil, fmt.Errorf("%w: unsupported placeholder {%s}", ErrInvalidConfig, name)
			}
			segments = append(segments, templateSegment{claim: claim, isClaim: true})
			claimsSeen[claim] = struct{}{}
			placeholderCount++
			position = end + 1
			continue
		}

		start := position
		for position < len(template) && template[position] != '{' {
			position++
		}
		literal := template[start:position]
		segments = append(segments, templateSegment{literal: literal})
		literalLength += len(literal)
	}

	distinctClaims := make([]ClaimName, 0, len(claimsSeen))
	for _, claim := range canonicalClaimOrder {
		if _, ok := claimsSeen[claim]; ok {
			distinctClaims = append(distinctClaims, claim)
		}
	}
	return &parsedTemplate{
		raw:            template,
		segments:       segments,
		placeholders:   placeholderCount,
		literalLength:  literalLength,
		distinctClaims: distinctClaims,
	}, nil
}

func (t *parsedTemplate) literalBytes() []byte {
	var builder strings.Builder
	builder.Grow(t.literalLength)
	for _, segment := range t.segments {
		if !segment.isClaim {
			builder.WriteString(segment.literal)
		}
	}
	return []byte(builder.String())
}

func (t *parsedTemplate) rawBytes() []byte {
	return []byte(t.raw)
}

// transducer is the deterministic finite-state transducer from claim tuples
// to principal bytes. Its states are template token position and byte position
// within a token. Since every claim language is finite, the transition graph
// is finite and independent of map iteration order.
type transducer struct {
	template          *parsedTemplate
	maxPrincipalBytes int
}

func newTransducer(snapshot *configSnapshot) *transducer {
	return &transducer{
		template:          snapshot.template,
		maxPrincipalBytes: snapshot.maxPrincipalBytes,
	}
}

func (t *transducer) transform(tuple ClaimTuple) (string, error) {
	var builder strings.Builder
	builder.Grow(t.template.literalLength)
	for _, segment := range t.template.segments {
		if segment.isClaim {
			value, ok := tuple[segment.claim]
			if !ok {
				return "", fmt.Errorf("%w: missing claim %q", ErrClaims, segment.claim)
			}
			if value == "" {
				return "", fmt.Errorf("%w: claim %q is empty", ErrClaims, segment.claim)
			}
			if err := appendPercentEncoded(&builder, value, t.maxPrincipalBytes); err != nil {
				return "", err
			}
			continue
		}
		if builder.Len()+len(segment.literal) > t.maxPrincipalBytes {
			return "", fmt.Errorf("%w: maximum is %d bytes", ErrPrincipalTooLong, t.maxPrincipalBytes)
		}
		builder.WriteString(segment.literal)
	}
	return builder.String(), nil
}

// PercentEncodeClaimValue encodes a claim value byte-for-byte. Only ASCII
// letters, digits, '-', '.', '_', and '~' remain unescaped. All other bytes
// become uppercase %HH. The value is not trimmed, lowercased, path-normalized,
// or decoded.
func PercentEncodeClaimValue(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	_ = appendPercentEncoded(&builder, value, len(value)*3)
	return builder.String()
}

// PercentEncode is an alias for PercentEncodeClaimValue.
func PercentEncode(value string) string {
	return PercentEncodeClaimValue(value)
}

func appendPercentEncoded(builder *strings.Builder, value string, maxBytes int) error {
	const upperHex = "0123456789ABCDEF"
	for index := 0; index < len(value); index++ {
		current := value[index]
		if isUnreservedByte(current) {
			if builder.Len()+1 > maxBytes {
				return fmt.Errorf("%w: maximum is %d bytes", ErrPrincipalTooLong, maxBytes)
			}
			builder.WriteByte(current)
			continue
		}
		if builder.Len()+3 > maxBytes {
			return fmt.Errorf("%w: maximum is %d bytes", ErrPrincipalTooLong, maxBytes)
		}
		builder.WriteByte('%')
		builder.WriteByte(upperHex[current>>4])
		builder.WriteByte(upperHex[current&0x0f])
	}
	return nil
}

func isUnreservedByte(value byte) bool {
	return value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z' ||
		value >= '0' && value <= '9' ||
		value == '-' || value == '.' || value == '_' || value == '~'
}
