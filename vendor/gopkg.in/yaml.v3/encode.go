package yaml

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

func encodeValue(value reflect.Value) (*Node, error) {
	if !value.IsValid() {
		return &Node{Kind: ScalarNode, Tag: "!!null"}, nil
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return &Node{Kind: ScalarNode, Tag: "!!null"}, nil
		}
		if value.Kind() == reflect.Interface {
			value = value.Elem()
		} else {
			value = value.Elem()
		}
	}
	if value.Type() == reflect.TypeOf(Node{}) {
		node := value.Interface().(Node)
		return &node, nil
	}
	if value.CanInterface() {
		if marshaler, ok := value.Interface().(Marshaler); ok {
			marshaled, err := marshaler.MarshalYAML()
			if err != nil {
				return nil, err
			}
			return encodeValue(reflect.ValueOf(marshaled))
		}
	}

	switch value.Kind() {
	case reflect.String:
		return &Node{Kind: ScalarNode, Tag: "!!str", Value: value.String()}, nil
	case reflect.Bool:
		return &Node{Kind: ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(value.Bool())}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return &Node{Kind: ScalarNode, Tag: "!!int", Value: strconv.FormatInt(value.Int(), 10)}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return &Node{Kind: ScalarNode, Tag: "!!int", Value: strconv.FormatUint(value.Uint(), 10)}, nil
	case reflect.Float32, reflect.Float64:
		return &Node{Kind: ScalarNode, Tag: "!!float", Value: strconv.FormatFloat(value.Float(), 'g', -1, 64)}, nil
	case reflect.Slice, reflect.Array:
		node := &Node{Kind: SequenceNode, Tag: "!!seq"}
		for i := 0; i < value.Len(); i++ {
			child, err := encodeValue(value.Index(i))
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content, child)
		}
		return node, nil
	case reflect.Map:
		node := &Node{Kind: MappingNode, Tag: "!!map"}
		iter := value.MapRange()
		for iter.Next() {
			key, err := encodeValue(iter.Key())
			if err != nil {
				return nil, err
			}
			child, err := encodeValue(iter.Value())
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content, key, child)
		}
		return node, nil
	case reflect.Struct:
		node := &Node{Kind: MappingNode, Tag: "!!map"}
		t := value.Type()
		for i := 0; i < value.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name := strings.ToLower(field.Name)
			if tag := field.Tag.Get("yaml"); tag != "" {
				parts := strings.Split(tag, ",")
				if parts[0] == "-" {
					continue
				}
				if parts[0] != "" {
					name = parts[0]
				}
			}
			key := &Node{Kind: ScalarNode, Tag: "!!str", Value: name}
			child, err := encodeValue(value.Field(i))
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content, key, child)
		}
		return node, nil
	default:
		return nil, fmt.Errorf("yaml: cannot marshal %s", value.Type())
	}
}

func marshalNode(node *Node) ([]byte, error) {
	var builder strings.Builder
	if err := emitNode(&builder, node, 0, false); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(builder.String(), "\n") {
		builder.WriteByte('\n')
	}
	return []byte(builder.String()), nil
}

func emitNode(builder *strings.Builder, node *Node, indent int, inline bool) error {
	if node == nil {
		builder.WriteString("null")
		return nil
	}
	switch node.Kind {
	case DocumentNode:
		if len(node.Content) == 0 {
			builder.WriteString("null\n")
			return nil
		}
		return emitNode(builder, node.Content[0], indent, inline)
	case AliasNode:
		if node.Value == "" {
			return fmt.Errorf("yaml: cannot marshal anonymous alias")
		}
		builder.WriteString("*")
		builder.WriteString(node.Value)
		return nil
	case ScalarNode:
		builder.WriteString(formatScalar(node))
		return nil
	case SequenceNode:
		if len(node.Content) == 0 {
			builder.WriteString("[]")
			return nil
		}
		for i, child := range node.Content {
			if i > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(strings.Repeat(" ", indent))
			builder.WriteString("- ")
			if child.Kind == ScalarNode || child.Kind == AliasNode {
				emitNode(builder, child, indent+2, true)
			} else {
				builder.WriteByte('\n')
				emitNode(builder, child, indent+2, false)
			}
		}
		return nil
	case MappingNode:
		if len(node.Content) == 0 {
			builder.WriteString("{}")
			return nil
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			if i > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(strings.Repeat(" ", indent))
			builder.WriteString(formatScalar(node.Content[i]))
			builder.WriteString(":")
			value := node.Content[i+1]
			if value.Kind == ScalarNode || value.Kind == AliasNode {
				builder.WriteByte(' ')
				emitNode(builder, value, indent+2, true)
			} else {
				builder.WriteByte('\n')
				emitNode(builder, value, indent+2, false)
			}
		}
		return nil
	default:
		return fmt.Errorf("yaml: cannot marshal %s", node.Kind)
	}
}

func formatScalar(node *Node) string {
	if node.Anchor != "" {
		return "&" + node.Anchor + " " + formatScalarValue(node)
	}
	return formatScalarValue(node)
}

func formatScalarValue(node *Node) string {
	if shortTag(node.Tag) == "!!null" && node.Value == "" {
		return "null"
	}
	if node.Style&DoubleQuotedStyle != 0 {
		return strconv.Quote(node.Value)
	}
	if node.Style&SingleQuotedStyle != 0 {
		return "'" + strings.ReplaceAll(node.Value, "'", "''") + "'"
	}
	if node.Value == "" {
		return `""`
	}
	if strings.ContainsAny(node.Value, "\n") {
		return strconv.Quote(node.Value)
	}
	if strings.Contains(node.Value, ": ") || strings.HasPrefix(node.Value, "#") ||
		strings.Contains(node.Value, " #") || strings.HasPrefix(node.Value, "- ") {
		return strconv.Quote(node.Value)
	}
	return node.Value
}
