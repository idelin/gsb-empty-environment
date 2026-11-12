package avroocf

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash/crc64"
	"regexp"
)

// Kind identifies an Avro schema type.
type Kind int

const (
	KindNull Kind = iota
	KindBoolean
	KindInt
	KindLong
	KindFloat
	KindDouble
	KindBytes
	KindString
	KindRecord
	KindEnum
	KindArray
	KindMap
	KindUnion
	KindFixed
	kindRef
)

// Field is one field of an Avro record schema.
type Field struct {
	Name   string
	Schema *Schema
}

// Schema is a parsed Avro schema.
type Schema struct {
	Kind      Kind
	Name      string
	Namespace string
	Fields    []Field
	Symbols   []string
	Size      int
	Items     *Schema
	Values    *Schema
	Branches  []*Schema
	refName   string
}

type schemaParser struct {
	registry  map[string]*Schema
	maxDepth  int
	depth     int
	namespace string
}

var namePart = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validFullName(name string) bool {
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			if i == 0 || i == len(name)-1 || name[i+1] == '.' {
				return false
			}
		}
	}
	for _, part := range splitDotted(name) {
		if !namePart.MatchString(part) {
			return false
		}
	}
	return true
}

func splitDotted(name string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			parts = append(parts, name[start:i])
			start = i + 1
		}
	}
	return append(parts, name[start:])
}

func containsDot(name string) bool {
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			return true
		}
	}
	return false
}

func (p *schemaParser) qualify(name string) string {
	if containsDot(name) || p.namespace == "" {
		return name
	}
	return p.namespace + "." + name
}

// ParseSchema parses an Avro schema JSON and enforces a nesting depth limit.
// Recursive (self-referential) schemas are allowed as long as their textual
// nesting does not exceed maxDepth.
func ParseSchema(raw []byte, maxDepth int) (*Schema, error) {
	if maxDepth <= 0 {
		return nil, fmt.Errorf("%w: max depth must be positive", ErrDepthLimit)
	}
	var node any
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, fmt.Errorf("avroocf: invalid schema json: %w", err)
	}
	p := &schemaParser{
		registry: make(map[string]*Schema),
		maxDepth: maxDepth,
	}
	schema, err := p.parse(node)
	if err != nil {
		return nil, err
	}
	if err := p.resolve(schema, make(map[*Schema]bool)); err != nil {
		return nil, err
	}
	if err := validate(schema, make(map[*Schema]bool)); err != nil {
		return nil, err
	}
	return schema, nil
}

func (p *schemaParser) enter() error {
	p.depth++
	if p.depth > p.maxDepth {
		return fmt.Errorf("%w: schema nesting exceeds %d", ErrDepthLimit, p.maxDepth)
	}
	return nil
}

func (p *schemaParser) parse(node any) (*Schema, error) {
	switch value := node.(type) {
	case string:
		if schema, ok := primitiveSchema(value); ok {
			return schema, nil
		}
		fullName := p.qualify(value)
		if !validFullName(fullName) {
			return nil, fmt.Errorf("avroocf: invalid schema name %q", value)
		}
		return &Schema{Kind: kindRef, refName: fullName}, nil
	case []any:
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer func() { p.depth-- }()
		return p.parseUnion(value)
	case map[string]any:
		typeName, ok := value["type"].(string)
		if !ok {
			return nil, fmt.Errorf("avroocf: schema object missing string type")
		}
		if schema, ok := primitiveSchema(typeName); ok {
			return schema, nil
		}
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer func() { p.depth-- }()
		switch typeName {
		case "array":
			itemsNode, ok := value["items"]
			if !ok {
				return nil, fmt.Errorf("avroocf: array schema missing items")
			}
			items, err := p.parse(itemsNode)
			if err != nil {
				return nil, err
			}
			return &Schema{Kind: KindArray, Items: items}, nil
		case "map":
			valuesNode, ok := value["values"]
			if !ok {
				return nil, fmt.Errorf("avroocf: map schema missing values")
			}
			values, err := p.parse(valuesNode)
			if err != nil {
				return nil, err
			}
			return &Schema{Kind: KindMap, Values: values}, nil
		case "enum":
			return p.parseEnum(value)
		case "fixed":
			return p.parseFixed(value)
		case "record":
			return p.parseRecord(value)
		default:
			fullName := p.qualify(typeName)
			if !validFullName(fullName) {
				return nil, fmt.Errorf("avroocf: unknown type %q", typeName)
			}
			return &Schema{Kind: kindRef, refName: fullName}, nil
		}
	default:
		return nil, fmt.Errorf("avroocf: unsupported schema node %T", node)
	}
}

func primitiveSchema(typeName string) (*Schema, bool) {
	switch typeName {
	case "null":
		return &Schema{Kind: KindNull}, true
	case "boolean":
		return &Schema{Kind: KindBoolean}, true
	case "int":
		return &Schema{Kind: KindInt}, true
	case "long":
		return &Schema{Kind: KindLong}, true
	case "float":
		return &Schema{Kind: KindFloat}, true
	case "double":
		return &Schema{Kind: KindDouble}, true
	case "bytes":
		return &Schema{Kind: KindBytes}, true
	case "string":
		return &Schema{Kind: KindString}, true
	default:
		return nil, false
	}
}

func (p *schemaParser) parseUnion(nodes []any) (*Schema, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("avroocf: union must have at least one branch")
	}
	schema := &Schema{Kind: KindUnion}
	seen := make(map[string]bool)
	for _, branchNode := range nodes {
		branch, err := p.parse(branchNode)
		if err != nil {
			return nil, err
		}
		if branch.Kind == KindUnion {
			return nil, fmt.Errorf("avroocf: union branch must not itself be a union")
		}
		key := branchKey(branch)
		if seen[key] {
			return nil, fmt.Errorf("avroocf: duplicate union branch %s", key)
		}
		seen[key] = true
		schema.Branches = append(schema.Branches, branch)
	}
	return schema, nil
}

func branchKey(schema *Schema) string {
	switch schema.Kind {
	case KindRecord, KindEnum, KindFixed:
		return schema.Kind.String() + ":" + schema.Name
	default:
		return schema.Kind.String()
	}
}

func (p *schemaParser) parseEnum(value map[string]any) (*Schema, error) {
	name, namespace, err := p.parseName(value)
	if err != nil {
		return nil, err
	}
	rawSymbols, ok := value["symbols"].([]any)
	if !ok || len(rawSymbols) == 0 {
		return nil, fmt.Errorf("avroocf: enum %s requires non-empty symbols", name)
	}
	schema := &Schema{Kind: KindEnum, Name: name, Namespace: namespace}
	seen := make(map[string]bool)
	for _, rawSymbol := range rawSymbols {
		symbol, ok := rawSymbol.(string)
		if !ok || symbol == "" {
			return nil, fmt.Errorf("avroocf: enum %s symbols must be non-empty strings", name)
		}
		if seen[symbol] {
			return nil, fmt.Errorf("avroocf: enum %s has duplicate symbol %q", name, symbol)
		}
		seen[symbol] = true
		schema.Symbols = append(schema.Symbols, symbol)
	}
	if err := p.register(schema); err != nil {
		return nil, err
	}
	return schema, nil
}

func (p *schemaParser) parseRecord(value map[string]any) (*Schema, error) {
	name, namespace, err := p.parseName(value)
	if err != nil {
		return nil, err
	}
	schema := &Schema{Kind: KindRecord, Name: name, Namespace: namespace}
	if err := p.register(schema); err != nil {
		return nil, err
	}
	previousNamespace := p.namespace
	p.namespace = namespace
	defer func() { p.namespace = previousNamespace }()

	rawFields, ok := value["fields"].([]any)
	if !ok || len(rawFields) == 0 {
		return nil, fmt.Errorf("avroocf: record %s requires non-empty fields", name)
	}
	seenFields := make(map[string]bool)
	for _, rawField := range rawFields {
		fieldObject, ok := rawField.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("avroocf: record %s fields must be objects", name)
		}
		fieldName, ok := fieldObject["name"].(string)
		if !ok || !namePart.MatchString(fieldName) {
			return nil, fmt.Errorf("avroocf: record %s has invalid field name", name)
		}
		if seenFields[fieldName] {
			return nil, fmt.Errorf("avroocf: record %s has duplicate field %q", name, fieldName)
		}
		seenFields[fieldName] = true
		rawType, ok := fieldObject["type"]
		if !ok {
			return nil, fmt.Errorf("avroocf: field %s.%s missing type", name, fieldName)
		}
		fieldSchema, err := p.parse(rawType)
		if err != nil {
			return nil, err
		}
		schema.Fields = append(schema.Fields, Field{Name: fieldName, Schema: fieldSchema})
	}
	return schema, nil
}

func (p *schemaParser) parseName(value map[string]any) (string, string, error) {
	name, ok := value["name"].(string)
	if !ok || name == "" {
		return "", "", fmt.Errorf("avroocf: named schema requires a name")
	}
	namespace, _ := value["namespace"].(string)
	if namespace == "" {
		namespace = p.namespace
	}
	if containsDot(name) {
		namespace = ""
	}
	fullName := name
	if namespace != "" && !containsDot(name) {
		fullName = namespace + "." + name
	}
	if !validFullName(fullName) {
		return "", "", fmt.Errorf("avroocf: invalid schema name %q", fullName)
	}
	return fullName, namespace, nil
}

func (p *schemaParser) register(schema *Schema) error {
	if _, exists := p.registry[schema.Name]; exists {
		return fmt.Errorf("avroocf: duplicate schema name %q", schema.Name)
	}
	p.registry[schema.Name] = schema
	return nil
}

func (p *schemaParser) resolve(schema *Schema, seen map[*Schema]bool) error {
	if schema == nil || seen[schema] {
		return nil
	}
	seen[schema] = true
	if schema.Kind == kindRef {
		target, ok := p.registry[schema.refName]
		if !ok {
			return fmt.Errorf("avroocf: unknown schema reference %q", schema.refName)
		}
		*schema = *target
	}
	switch schema.Kind {
	case KindRecord:
		for index := range schema.Fields {
			if err := p.resolve(schema.Fields[index].Schema, seen); err != nil {
				return err
			}
		}
	case KindArray:
		return p.resolve(schema.Items, seen)
	case KindMap:
		return p.resolve(schema.Values, seen)
	case KindUnion:
		for _, branch := range schema.Branches {
			if err := p.resolve(branch, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

func validate(schema *Schema, seen map[*Schema]bool) error {
	if schema == nil || seen[schema] {
		return nil
	}
	seen[schema] = true
	var err error
	switch schema.Kind {
	case KindRecord:
		for index := range schema.Fields {
			if err = validate(schema.Fields[index].Schema, seen); err != nil {
				return err
			}
		}
	case KindArray:
		err = validate(schema.Items, seen)
	case KindMap:
		err = validate(schema.Values, seen)
	case KindUnion:
		for _, branch := range schema.Branches {
			if branch.Kind == KindUnion {
				return fmt.Errorf("avroocf: nested union is not allowed")
			}
			if err = validate(branch, seen); err != nil {
				return err
			}
		}
	}
	return err
}

// String returns the Avro type name.
func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindBoolean:
		return "boolean"
	case KindInt:
		return "int"
	case KindLong:
		return "long"
	case KindFloat:
		return "float"
	case KindDouble:
		return "double"
	case KindBytes:
		return "bytes"
	case KindString:
		return "string"
	case KindRecord:
		return "record"
	case KindEnum:
		return "enum"
	case KindArray:
		return "array"
	case KindMap:
		return "map"
	case KindUnion:
		return "union"
	case KindFixed:
		return "fixed"
	default:
		return "unknown"
	}
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// Canonical returns the Parsing Canonical Form defined by the Avro spec.
func (s *Schema) Canonical() []byte {
	var buffer bytes.Buffer
	s.canonical(&buffer, make(map[*Schema]bool))
	return buffer.Bytes()
}

func (s *Schema) canonical(buffer *bytes.Buffer, defined map[*Schema]bool) {
	switch s.Kind {
	case KindRecord, KindEnum, KindFixed:
		if defined[s] {
			buffer.WriteString(jsonString(s.Name))
			return
		}
		defined[s] = true
	}
	switch s.Kind {
	case KindNull, KindBoolean, KindInt, KindLong, KindFloat, KindDouble, KindBytes, KindString:
		buffer.WriteString(jsonString(s.Kind.String()))
	case KindRecord:
		buffer.WriteString(`{"name":`)
		buffer.WriteString(jsonString(s.Name))
		buffer.WriteString(`,"type":"record","fields":[`)
		for index, field := range s.Fields {
			if index > 0 {
				buffer.WriteByte(',')
			}
			buffer.WriteString(`{"name":`)
			buffer.WriteString(jsonString(field.Name))
			buffer.WriteString(`,"type":`)
			field.Schema.canonical(buffer, defined)
			buffer.WriteByte('}')
		}
		buffer.WriteString(`]}`)
	case KindEnum:
		buffer.WriteString(`{"name":`)
		buffer.WriteString(jsonString(s.Name))
		buffer.WriteString(`,"type":"enum","symbols":[`)
		for index, symbol := range s.Symbols {
			if index > 0 {
				buffer.WriteByte(',')
			}
			buffer.WriteString(jsonString(symbol))
		}
		buffer.WriteString(`]}`)
	case KindFixed:
		buffer.WriteString(`{"name":`)
		buffer.WriteString(jsonString(s.Name))
		buffer.WriteString(`,"type":"fixed","size":`)
		fmt.Fprintf(buffer, "%d", s.Size)
		buffer.WriteByte('}')
	case KindArray:
		buffer.WriteString(`{"type":"array","items":`)
		s.Items.canonical(buffer, defined)
		buffer.WriteByte('}')
	case KindMap:
		buffer.WriteString(`{"type":"map","values":`)
		s.Values.canonical(buffer, defined)
		buffer.WriteByte('}')
	case KindUnion:
		buffer.WriteByte('[')
		for index, branch := range s.Branches {
			if index > 0 {
				buffer.WriteByte(',')
			}
			branch.canonical(buffer, defined)
		}
		buffer.WriteByte(']')
	}
}

// FingerprintSHA256 returns the SHA-256 fingerprint of the canonical form,
// encoded as lowercase hexadecimal.
func (s *Schema) FingerprintSHA256() string {
	digest := sha256.Sum256(s.Canonical())
	return hexEncode(digest[:])
}

// FingerprintCRC64 returns the 64-bit Avro fingerprint (RFC 3720 polynomial,
// reflected) of the canonical form.
func (s *Schema) FingerprintCRC64() uint64 {
	return crc64Avro(s.Canonical())
}

func hexEncode(data []byte) string {
	const digits = "0123456789abcdef"
	buffer := make([]byte, len(data)*2)
	for index, value := range data {
		buffer[index*2] = digits[value>>4]
		buffer[index*2+1] = digits[value&0x0f]
	}
	return string(buffer)
}

var crc64AvroTable = crc64.MakeTable(0xC96C5795D7870F42)

// crc64Avro implements the CRC-64-AVRO fingerprint: reflected polynomial
// 0xC96C5795D7870F42, initial value zero, no final xor.
func crc64Avro(data []byte) uint64 {
	return crc64.Checksum(data, crc64AvroTable)
}

func (p *schemaParser) parseFixed(value map[string]any) (*Schema, error) {
	name, namespace, err := p.parseName(value)
	if err != nil {
		return nil, err
	}
	rawSize, ok := value["size"].(float64)
	if !ok || rawSize < 0 || rawSize != float64(int(rawSize)) {
		return nil, fmt.Errorf("avroocf: fixed %s requires non-negative integer size", name)
	}
	schema := &Schema{Kind: KindFixed, Name: name, Namespace: namespace, Size: int(rawSize)}
	if err := p.register(schema); err != nil {
		return nil, err
	}
	return schema, nil
}
