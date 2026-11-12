// Package yaml implements the subset of gopkg.in/yaml.v3 used by the
// expansion-budget checker. It deliberately preserves the public Node layout
// and the anchor, alias, merge-key, and line-number behavior expected by
// callers that use yaml.v3 to inspect configuration documents.
package yaml

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// Kind identifies the kind of a Node.
type Kind uint32

const (
	DocumentNode Kind = 1 << iota
	SequenceNode
	MappingNode
	ScalarNode
	AliasNode
)

// String returns the name of the node kind.
func (k Kind) String() string {
	switch k {
	case DocumentNode:
		return "document"
	case SequenceNode:
		return "sequence"
	case MappingNode:
		return "mapping"
	case ScalarNode:
		return "scalar"
	case AliasNode:
		return "alias"
	default:
		return fmt.Sprintf("Kind(%d)", uint32(k))
	}
}

// Style describes the presentation style of a node.
type Style uint32

const (
	TaggedStyle Style = 1 << iota
	DoubleQuotedStyle
	SingleQuotedStyle
	LiteralStyle
	FoldedStyle
	FlowStyle
)

// Node is the representation of a YAML node. Anchors and aliases are kept as
// pointers, so callers can inspect a document without first materializing the
// potentially enormous expanded tree.
type Node struct {
	Kind  Kind
	Style Style

	Tag   string
	Value string

	Anchor  string
	Alias   *Node
	Content []*Node

	HeadComment string
	LineComment string
	FootComment string

	Line   int
	Column int
}

// IsZero reports whether the node is empty.
func (n *Node) IsZero() bool {
	return n == nil || n.Kind == 0
}

// LongTag returns the full YAML tag for a short tag.
func (n *Node) LongTag() string {
	return longTag(n.Tag)
}

// ShortTag returns the short YAML tag for a full tag.
func (n *Node) ShortTag() string {
	return shortTag(n.Tag)
}

func longTag(tag string) string {
	switch tag {
	case "", "!!null":
		return "!!null"
	case "!!bool":
		return "tag:yaml.org,2002:bool"
	case "!!str":
		return "tag:yaml.org,2002:str"
	case "!!int":
		return "tag:yaml.org,2002:int"
	case "!!float":
		return "tag:yaml.org,2002:float"
	case "!!timestamp":
		return "tag:yaml.org,2002:timestamp"
	case "!!seq":
		return "tag:yaml.org,2002:seq"
	case "!!map":
		return "tag:yaml.org,2002:map"
	case "!!binary":
		return "tag:yaml.org,2002:binary"
	case "!!merge":
		return "tag:yaml.org,2002:merge"
	default:
		return tag
	}
}

func shortTag(tag string) string {
	const prefix = "tag:yaml.org,2002:"
	if strings.HasPrefix(tag, prefix) {
		return "!!" + strings.TrimPrefix(tag, prefix)
	}
	return tag
}

// TypeError is returned when one or more values cannot be decoded into the
// requested Go type.
type TypeError struct {
	Errors []string
}

func (e *TypeError) Error() string {
	return fmt.Sprintf("yaml: unmarshal errors:\n  %s", strings.Join(e.Errors, "\n  "))
}

// Unmarshaler is implemented by types that customize YAML decoding.
type Unmarshaler interface {
	UnmarshalYAML(value *Node) error
}

// Marshaler is implemented by types that customize YAML encoding.
type Marshaler interface {
	MarshalYAML() (interface{}, error)
}

// IsZeroer is implemented by types that define their own zero value.
type IsZeroer interface {
	IsZero() bool
}

// Decoder reads YAML documents from a stream.
type Decoder struct {
	docs        []*Node
	index       int
	parseErr    error
	knownFields bool
}

// NewDecoder returns a decoder that reads from r.
func NewDecoder(r io.Reader) *Decoder {
	data, err := io.ReadAll(r)
	if err != nil {
		return &Decoder{parseErr: err}
	}
	docs, err := parseDocuments(data)
	return &Decoder{docs: docs, parseErr: err}
}

// KnownFields, when enabled, rejects mapping keys that do not have a matching
// struct field. The compatibility layer accepts the call but does not enforce
// the option; budget checking does not rely on it.
func (d *Decoder) KnownFields(enable bool) {
	d.knownFields = enable
}

// Decode reads the next YAML document into v.
func (d *Decoder) Decode(v interface{}) error {
	if d.parseErr != nil {
		return d.parseErr
	}
	if d.index >= len(d.docs) {
		return io.EOF
	}
	doc := d.docs[d.index]
	d.index++
	return decodeDocument(doc, v)
}

// Unmarshal decodes the first YAML document in in into out.
func Unmarshal(in []byte, out interface{}) error {
	return NewDecoder(bytes.NewReader(in)).Decode(out)
}

// UnmarshalStrict is retained for callers migrating from yaml.v2.
func UnmarshalStrict(in []byte, out interface{}) error {
	return Unmarshal(in, out)
}

// Decode decodes the node into out.
func (n *Node) Decode(out interface{}) error {
	return decodeDocument(n, out)
}

// Encode replaces the node with the encoded form of v.
func (n *Node) Encode(v interface{}) error {
	encoded, err := encodeValue(reflect.ValueOf(v))
	if err != nil {
		return err
	}
	*n = *encoded
	return nil
}

// Marshal serializes v as YAML.
func Marshal(v interface{}) ([]byte, error) {
	var n *Node
	switch value := v.(type) {
	case *Node:
		n = value
	case Node:
		n = &value
	default:
		var err error
		n, err = encodeValue(reflect.ValueOf(v))
		if err != nil {
			return nil, err
		}
	}
	return marshalNode(n)
}
