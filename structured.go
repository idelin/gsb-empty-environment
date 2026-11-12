package netsec

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

type parameterKind uint8

const (
	parameterString parameterKind = iota
	parameterInteger
	parameterToken
	parameterByteSequence
	parameterBoolean
)

type parameterValue struct {
	kind parameterKind
	text string
	num  int64
	bool bool
	data []byte
}

type coveredComponent struct {
	name   string
	params []componentParameter
}

type componentParameter struct {
	name     string
	value    parameterValue
	hasValue bool
}

type signatureParameters struct {
	values  map[string]parameterValue
	ordered []componentParameter
}

type signatureInput struct {
	label      string
	components []coveredComponent
	params     signatureParameters
}

type structuredParser struct {
	input string
	pos   int
}

func newStructuredParser(input string) (*structuredParser, error) {
	if strings.ContainsAny(input, "\r\n") {
		return nil, fmt.Errorf("%w: CR or LF in structured field", ErrMalformedSignature)
	}
	return &structuredParser{input: input}, nil
}

func (p *structuredParser) malformed(format string, args ...any) error {
	return fmt.Errorf("%w: %s at byte %d", ErrMalformedSignature, fmt.Sprintf(format, args...), p.pos)
}

func (p *structuredParser) skipSpace() {
	for p.pos < len(p.input) {
		switch p.input[p.pos] {
		case ' ', '\t':
			p.pos++
		default:
			return
		}
	}
}

func (p *structuredParser) peek() byte {
	if p.pos >= len(p.input) {
		return 0
	}
	return p.input[p.pos]
}

func (p *structuredParser) expect(want byte) error {
	if p.peek() != want {
		return p.malformed("expected %q", want)
	}
	p.pos++
	return nil
}

func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	}
	return strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))
}

func (p *structuredParser) parseToken() (string, error) {
	start := p.pos
	for p.pos < len(p.input) && isTokenChar(p.input[p.pos]) {
		p.pos++
	}
	if start == p.pos {
		return "", p.malformed("expected token")
	}
	return p.input[start:p.pos], nil
}

func (p *structuredParser) parseString() (string, error) {
	if err := p.expect('"'); err != nil {
		return "", err
	}
	var out strings.Builder
	for p.pos < len(p.input) {
		c := p.input[p.pos]
		p.pos++
		switch c {
		case '"':
			return out.String(), nil
		case '\\':
			if p.pos >= len(p.input) {
				return "", p.malformed("truncated string escape")
			}
			escaped := p.input[p.pos]
			p.pos++
			if escaped != '"' && escaped != '\\' {
				return "", p.malformed("invalid string escape")
			}
			out.WriteByte(escaped)
		case '\r', '\n', 0:
			return "", p.malformed("invalid character in string")
		default:
			out.WriteByte(c)
		}
	}
	return "", p.malformed("unterminated string")
}

func (p *structuredParser) parseByteSequence() ([]byte, error) {
	if err := p.expect(':'); err != nil {
		return nil, err
	}
	start := p.pos
	for p.pos < len(p.input) && p.input[p.pos] != ':' {
		if p.input[p.pos] == '\r' || p.input[p.pos] == '\n' {
			return nil, p.malformed("invalid byte sequence")
		}
		p.pos++
	}
	if p.pos >= len(p.input) {
		return nil, p.malformed("unterminated byte sequence")
	}
	encoded := p.input[start:p.pos]
	p.pos++
	if len(encoded)%4 == 0 {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err == nil {
			return decoded, nil
		}
	}
	decoded, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, p.malformed("invalid base64 byte sequence")
	}
	return decoded, nil
}

func (p *structuredParser) parseInteger() (int64, error) {
	start := p.pos
	if p.peek() == '-' {
		p.pos++
	}
	digitStart := p.pos
	for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
		p.pos++
	}
	if digitStart == p.pos {
		return 0, p.malformed("expected integer")
	}
	literal := p.input[start:p.pos]
	if len(literal) > 1 && literal[0] == '0' {
		return 0, p.malformed("leading zero in integer")
	}
	if len(literal) > 2 && literal[0] == '-' && literal[1] == '0' {
		return 0, p.malformed("leading zero in integer")
	}
	value, err := strconv.ParseInt(literal, 10, 64)
	if err != nil {
		return 0, p.malformed("integer out of range")
	}
	return value, nil
}

func (p *structuredParser) parseBareItem() (parameterValue, error) {
	if p.pos >= len(p.input) {
		return parameterValue{}, p.malformed("expected bare item")
	}
	switch c := p.peek(); {
	case c == '"':
		value, err := p.parseString()
		if err != nil {
			return parameterValue{}, err
		}
		return parameterValue{kind: parameterString, text: value}, nil
	case c == ':':
		value, err := p.parseByteSequence()
		if err != nil {
			return parameterValue{}, err
		}
		return parameterValue{kind: parameterByteSequence, data: value}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		value, err := p.parseInteger()
		if err != nil {
			return parameterValue{}, err
		}
		return parameterValue{kind: parameterInteger, num: value}, nil
	case c == '?':
		p.pos++
		if p.pos >= len(p.input) {
			return parameterValue{}, p.malformed("truncated boolean")
		}
		switch p.input[p.pos] {
		case '0':
			p.pos++
			return parameterValue{kind: parameterBoolean, bool: false}, nil
		case '1':
			p.pos++
			return parameterValue{kind: parameterBoolean, bool: true}, nil
		default:
			return parameterValue{}, p.malformed("invalid boolean")
		}
	default:
		value, err := p.parseToken()
		if err != nil {
			return parameterValue{}, err
		}
		return parameterValue{kind: parameterToken, text: value}, nil
	}
}

func (p *structuredParser) parseParameters() ([]componentParameter, error) {
	var params []componentParameter
	seen := make(map[string]struct{})
	for p.peek() == ';' {
		p.pos++
		name, err := p.parseToken()
		if err != nil {
			return nil, err
		}
		if _, exists := seen[name]; exists {
			return nil, p.malformed("duplicate parameter %q", name)
		}
		seen[name] = struct{}{}
		param := componentParameter{name: name}
		if p.peek() == '=' {
			p.pos++
			value, err := p.parseBareItem()
			if err != nil {
				return nil, err
			}
			param.value = value
			param.hasValue = true
		}
		params = append(params, param)
	}
	return params, nil
}

func (p *structuredParser) parseSignatureParameters() (signatureParameters, error) {
	params, err := p.parseParameters()
	if err != nil {
		return signatureParameters{}, err
	}
	values := make(map[string]parameterValue, len(params))
	for _, param := range params {
		if !param.hasValue {
			return signatureParameters{}, p.malformed("signature parameter %q has no value", param.name)
		}
		values[param.name] = param.value
	}
	return signatureParameters{values: values, ordered: params}, nil
}

func parseSignatureInputs(values []string) (map[string]signatureInput, error) {
	p, err := newStructuredParser(strings.Join(values, ","))
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos == len(p.input) {
		return nil, p.malformed("empty Signature-Input")
	}

	inputs := make(map[string]signatureInput)
	for {
		label, err := p.parseToken()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if err := p.expect('='); err != nil {
			return nil, err
		}
		p.skipSpace()
		if err := p.expect('('); err != nil {
			return nil, err
		}

		var components []coveredComponent
		for {
			p.skipSpace()
			if p.peek() == ')' {
				p.pos++
				break
			}
			if p.peek() != '"' {
				return nil, p.malformed("expected covered component string")
			}
			name, err := p.parseString()
			if err != nil {
				return nil, err
			}
			params, err := p.parseParameters()
			if err != nil {
				return nil, err
			}
			components = append(components, coveredComponent{name: name, params: params})
		}
		params, err := p.parseSignatureParameters()
		if err != nil {
			return nil, err
		}
		if _, exists := inputs[label]; exists {
			return nil, p.malformed("duplicate signature label %q", label)
		}
		inputs[label] = signatureInput{label: label, components: components, params: params}

		p.skipSpace()
		if p.pos == len(p.input) {
			return inputs, nil
		}
		if err := p.expect(','); err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.pos == len(p.input) {
			return nil, p.malformed("trailing comma")
		}
	}
}

func parseSignatureValues(values []string) (map[string][]byte, error) {
	p, err := newStructuredParser(strings.Join(values, ","))
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos == len(p.input) {
		return nil, p.malformed("empty Signature")
	}

	signatures := make(map[string][]byte)
	for {
		label, err := p.parseToken()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if err := p.expect('='); err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.peek() != ':' {
			return nil, p.malformed("expected signature byte sequence")
		}
		signature, err := p.parseByteSequence()
		if err != nil {
			return nil, err
		}
		if _, exists := signatures[label]; exists {
			return nil, p.malformed("duplicate signature label %q", label)
		}
		signatures[label] = signature

		p.skipSpace()
		if p.pos == len(p.input) {
			return signatures, nil
		}
		if err := p.expect(','); err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.pos == len(p.input) {
			return nil, p.malformed("trailing comma")
		}
	}
}

func parseContentDigest(values []string) (map[string][]byte, error) {
	p, err := newStructuredParser(strings.Join(values, ","))
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos == len(p.input) {
		return nil, p.malformed("empty Content-Digest")
	}

	digests := make(map[string][]byte)
	for {
		algorithm, err := p.parseToken()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if err := p.expect('='); err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.peek() != ':' {
			return nil, p.malformed("expected digest byte sequence")
		}
		digest, err := p.parseByteSequence()
		if err != nil {
			return nil, err
		}
		if _, exists := digests[algorithm]; exists {
			return nil, p.malformed("duplicate digest algorithm %q", algorithm)
		}
		digests[algorithm] = digest

		p.skipSpace()
		if p.pos == len(p.input) {
			return digests, nil
		}
		if err := p.expect(','); err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.pos == len(p.input) {
			return nil, p.malformed("trailing comma")
		}
	}
}

func stringParameter(params signatureParameters, name string) (string, bool, error) {
	value, ok := params.values[name]
	if !ok {
		return "", false, nil
	}
	if value.kind != parameterString && value.kind != parameterToken {
		return "", false, fmt.Errorf("%w: %s parameter must be a string", ErrMalformedSignature, name)
	}
	return value.text, true, nil
}

func integerParameter(params signatureParameters, name string) (int64, bool, error) {
	value, ok := params.values[name]
	if !ok {
		return 0, false, nil
	}
	if value.kind != parameterInteger {
		return 0, false, fmt.Errorf("%w: %s parameter must be an integer", ErrMalformedSignature, name)
	}
	return value.num, true, nil
}

func quoteStructuredString(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("%w: CR or LF in string value", ErrMalformedSignature)
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`, nil
}

func serializeParameterValue(value parameterValue) (string, error) {
	switch value.kind {
	case parameterString:
		return quoteStructuredString(value.text)
	case parameterInteger:
		return strconv.FormatInt(value.num, 10), nil
	case parameterToken:
		for i := 0; i < len(value.text); i++ {
			if !isTokenChar(value.text[i]) {
				return "", fmt.Errorf("%w: invalid token parameter value", ErrMalformedSignature)
			}
		}
		return value.text, nil
	case parameterByteSequence:
		return ":" + base64.StdEncoding.EncodeToString(value.data) + ":", nil
	case parameterBoolean:
		if value.bool {
			return "?1", nil
		}
		return "?0", nil
	default:
		return "", fmt.Errorf("%w: unknown parameter value type", ErrMalformedSignature)
	}
}

func serializeSignatureParameters(input signatureInput) (string, error) {
	var serialized strings.Builder
	serialized.WriteByte('(')
	for index, component := range input.components {
		if index != 0 {
			serialized.WriteByte(' ')
		}
		quotedName, err := quoteStructuredString(component.name)
		if err != nil {
			return "", err
		}
		serialized.WriteString(quotedName)
		for _, parameter := range component.params {
			serialized.WriteByte(';')
			serialized.WriteString(parameter.name)
			if parameter.hasValue {
				value, err := serializeParameterValue(parameter.value)
				if err != nil {
					return "", err
				}
				serialized.WriteByte('=')
				serialized.WriteString(value)
			}
		}
	}
	serialized.WriteByte(')')
	for _, parameter := range input.params.ordered {
		serialized.WriteByte(';')
		serialized.WriteString(parameter.name)
		serialized.WriteByte('=')
		value, err := serializeParameterValue(parameter.value)
		if err != nil {
			return "", err
		}
		serialized.WriteString(value)
	}
	return serialized.String(), nil
}
