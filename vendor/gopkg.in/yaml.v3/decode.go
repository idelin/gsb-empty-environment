package yaml

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type decoder struct {
	aliasStack map[*Node]bool
}

func decodeDocument(doc *Node, out interface{}) error {
	if doc == nil {
		return nil
	}
	value := reflect.ValueOf(out)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		return fmt.Errorf("yaml: unmarshal requires a non-nil pointer, got %T", out)
	}
	d := &decoder{aliasStack: make(map[*Node]bool)}
	return d.decode(doc, value.Elem())
}

func (d *decoder) decode(node *Node, out reflect.Value) error {
	if !out.IsValid() {
		return nil
	}
	if node == nil {
		node = nullNode(0, 0)
	}
	switch node.Kind {
	case DocumentNode:
		if len(node.Content) == 0 {
			return d.decode(nullNode(node.Line, node.Column), out)
		}
		return d.decode(node.Content[0], out)
	case AliasNode:
		if node.Alias == nil {
			return fmt.Errorf("yaml: line %d: alias %q has no target", node.Line, node.Value)
		}
		if d.aliasStack[node.Alias] {
			return fmt.Errorf("yaml: line %d: alias cycle involving %q", node.Line, node.Value)
		}
		d.aliasStack[node.Alias] = true
		err := d.decode(node.Alias, out)
		delete(d.aliasStack, node.Alias)
		return err
	}

	if out.Kind() == reflect.Interface && out.NumMethod() == 0 {
		value, err := d.decodeAny(node)
		if err != nil {
			return err
		}
		if value == nil {
			out.Set(reflect.Zero(out.Type()))
		} else {
			out.Set(reflect.ValueOf(value))
		}
		return nil
	}

	if out.Kind() == reflect.Ptr {
		if out.IsNil() {
			out.Set(reflect.New(out.Type().Elem()))
		}
		return d.decode(node, out.Elem())
	}

	if out.CanAddr() {
		if unmarshaler, ok := out.Addr().Interface().(Unmarshaler); ok {
			return unmarshaler.UnmarshalYAML(node)
		}
		if textUnmarshaler, ok := out.Addr().Interface().(encoding.TextUnmarshaler); ok {
			text, err := scalarText(node)
			if err != nil {
				return err
			}
			return textUnmarshaler.UnmarshalText([]byte(text))
		}
	}

	if out.Type() == reflect.TypeOf(Node{}) {
		out.Set(reflect.ValueOf(*node))
		return nil
	}

	switch node.Kind {
	case ScalarNode:
		return d.decodeScalar(node, out)
	case SequenceNode:
		return d.decodeSequence(node, out)
	case MappingNode:
		return d.decodeMapping(node, out)
	default:
		return fmt.Errorf("yaml: line %d: cannot decode %s", node.Line, node.Kind)
	}
}

func (d *decoder) decodeAny(node *Node) (interface{}, error) {
	switch node.Kind {
	case DocumentNode:
		if len(node.Content) == 0 {
			return nil, nil
		}
		return d.decodeAny(node.Content[0])
	case AliasNode:
		if node.Alias == nil {
			return nil, fmt.Errorf("yaml: line %d: alias %q has no target", node.Line, node.Value)
		}
		if d.aliasStack[node.Alias] {
			return nil, fmt.Errorf("yaml: line %d: alias cycle involving %q", node.Line, node.Value)
		}
		d.aliasStack[node.Alias] = true
		value, err := d.decodeAny(node.Alias)
		delete(d.aliasStack, node.Alias)
		return value, err
	case ScalarNode:
		switch shortTag(node.Tag) {
		case "!!null":
			return nil, nil
		case "!!bool":
			return parseBool(node.Value)
		case "!!int":
			if value, err := strconv.ParseInt(cleanNumber(node.Value), 0, 64); err == nil {
				return int(value), nil
			}
			if value, err := strconv.ParseUint(cleanNumber(node.Value), 0, 64); err == nil {
				return value, nil
			}
			return node.Value, nil
		case "!!float":
			if value, err := strconv.ParseFloat(cleanNumber(node.Value), 64); err == nil {
				return value, nil
			}
			return node.Value, nil
		default:
			return node.Value, nil
		}
	case SequenceNode:
		result := make([]interface{}, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := d.decodeAny(child)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return result, nil
	case MappingNode:
		entries, err := resolveMapping(node, make(map[*Node]bool))
		if err != nil {
			return nil, err
		}
		result := make(map[string]interface{}, len(entries))
		for _, entry := range entries {
			key, err := d.decodeAny(entry.key)
			if err != nil {
				return nil, err
			}
			value, err := d.decodeAny(entry.value)
			if err != nil {
				return nil, err
			}
			result[fmt.Sprint(key)] = value
		}
		return result, nil
	default:
		return nil, fmt.Errorf("yaml: line %d: cannot decode %s", node.Line, node.Kind)
	}
}

func (d *decoder) decodeScalar(node *Node, out reflect.Value) error {
	if out.Kind() == reflect.Ptr {
		if out.IsNil() {
			out.Set(reflect.New(out.Type().Elem()))
		}
		return d.decodeScalar(node, out.Elem())
	}
	switch out.Kind() {
	case reflect.String:
		out.SetString(node.Value)
	case reflect.Bool:
		value, err := parseBool(node.Value)
		if err != nil {
			return err
		}
		out.SetBool(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if out.Type() == reflect.TypeOf(time.Duration(0)) {
			value, err := time.ParseDuration(node.Value)
			if err != nil {
				return err
			}
			out.SetInt(int64(value))
			return nil
		}
		value, err := strconv.ParseInt(cleanNumber(node.Value), 0, out.Type().Bits())
		if err != nil {
			return fmt.Errorf("yaml: line %d: cannot decode %q into %s", node.Line, node.Value, out.Type())
		}
		out.SetInt(value)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		value, err := strconv.ParseUint(cleanNumber(node.Value), 0, out.Type().Bits())
		if err != nil {
			return fmt.Errorf("yaml: line %d: cannot decode %q into %s", node.Line, node.Value, out.Type())
		}
		out.SetUint(value)
	case reflect.Float32, reflect.Float64:
		value, err := strconv.ParseFloat(cleanNumber(node.Value), out.Type().Bits())
		if err != nil {
			return fmt.Errorf("yaml: line %d: cannot decode %q into %s", node.Line, node.Value, out.Type())
		}
		out.SetFloat(value)
	case reflect.Struct:
		if out.Type() == reflect.TypeOf(time.Time{}) {
			value, err := time.Parse(time.RFC3339, node.Value)
			if err != nil {
				return err
			}
			out.Set(reflect.ValueOf(value))
			return nil
		}
		return fmt.Errorf("yaml: line %d: cannot decode scalar into %s", node.Line, out.Type())
	case reflect.Slice:
		if out.Type().Elem().Kind() == reflect.Uint8 {
			out.SetBytes([]byte(node.Value))
			return nil
		}
		return fmt.Errorf("yaml: line %d: cannot decode scalar into %s", node.Line, out.Type())
	default:
		return fmt.Errorf("yaml: line %d: cannot decode scalar into %s", node.Line, out.Type())
	}
	return nil
}

func (d *decoder) decodeSequence(node *Node, out reflect.Value) error {
	if out.Kind() == reflect.Ptr {
		if out.IsNil() {
			out.Set(reflect.New(out.Type().Elem()))
		}
		return d.decodeSequence(node, out.Elem())
	}
	switch out.Kind() {
	case reflect.Slice:
		result := reflect.MakeSlice(out.Type(), len(node.Content), len(node.Content))
		for i, child := range node.Content {
			if err := d.decode(child, result.Index(i)); err != nil {
				return err
			}
		}
		out.Set(result)
		return nil
	case reflect.Array:
		if out.Len() != len(node.Content) {
			return fmt.Errorf("yaml: line %d: cannot decode sequence of length %d into %s", node.Line, len(node.Content), out.Type())
		}
		for i, child := range node.Content {
			if err := d.decode(child, out.Index(i)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("yaml: line %d: cannot decode sequence into %s", node.Line, out.Type())
	}
}

func (d *decoder) decodeMapping(node *Node, out reflect.Value) error {
	if out.Kind() == reflect.Ptr {
		if out.IsNil() {
			out.Set(reflect.New(out.Type().Elem()))
		}
		return d.decodeMapping(node, out.Elem())
	}
	entries, err := resolveMapping(node, make(map[*Node]bool))
	if err != nil {
		return err
	}
	switch out.Kind() {
	case reflect.Map:
		if out.IsNil() {
			out.Set(reflect.MakeMap(out.Type()))
		}
		for _, entry := range entries {
			key := reflect.New(out.Type().Key()).Elem()
			if err := d.decode(entry.key, key); err != nil {
				return err
			}
			value := reflect.New(out.Type().Elem()).Elem()
			if err := d.decode(entry.value, value); err != nil {
				return err
			}
			out.SetMapIndex(key, value)
		}
		return nil
	case reflect.Struct:
		fields := structFieldIndexes(out.Type())
		for _, entry := range entries {
			key, err := scalarText(entry.key)
			if err != nil {
				return err
			}
			index, ok := fields[key]
			if !ok {
				continue
			}
			if err := d.decode(entry.value, out.Field(index)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("yaml: line %d: cannot decode mapping into %s", node.Line, out.Type())
	}
}

func structFieldIndexes(t reflect.Type) map[string]int {
	fields := make(map[string]int, t.NumField())
	for i := 0; i < t.NumField(); i++ {
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
		} else if tag := field.Tag.Get("json"); tag != "" {
			parts := strings.Split(tag, ",")
			if parts[0] == "-" {
				continue
			}
			if parts[0] != "" {
				name = parts[0]
			}
		}
		fields[name] = i
	}
	return fields
}

func scalarText(node *Node) (string, error) {
	if node == nil {
		return "", nil
	}
	if node.Kind == AliasNode {
		if node.Alias == nil {
			return "", fmt.Errorf("yaml: line %d: alias %q has no target", node.Line, node.Value)
		}
		return scalarText(node.Alias)
	}
	if node.Kind != ScalarNode {
		return "", fmt.Errorf("yaml: line %d: expected a scalar key", node.Line)
	}
	return node.Value, nil
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(value) {
	case "true", "yes", "on", "y":
		return true, nil
	case "false", "no", "off", "n":
		return false, nil
	}
	return strconv.ParseBool(value)
}

type mappingEntry struct {
	key   *Node
	value *Node
}

func resolveMapping(node *Node, stack map[*Node]bool) ([]mappingEntry, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind == AliasNode {
		if node.Alias == nil {
			return nil, fmt.Errorf("yaml: line %d: alias %q has no target", node.Line, node.Value)
		}
		if stack[node.Alias] {
			return nil, fmt.Errorf("yaml: line %d: alias cycle involving %q", node.Line, node.Value)
		}
		stack[node.Alias] = true
		entries, err := resolveMapping(node.Alias, stack)
		delete(stack, node.Alias)
		return entries, err
	}
	if node.Kind == DocumentNode {
		if len(node.Content) == 0 {
			return nil, nil
		}
		return resolveMapping(node.Content[0], stack)
	}
	if node.Kind != MappingNode {
		return nil, fmt.Errorf("yaml: line %d: merge value is not a mapping", node.Line)
	}
	if stack[node] {
		return nil, fmt.Errorf("yaml: line %d: recursive merge", node.Line)
	}
	stack[node] = true
	defer delete(stack, node)

	var explicit []mappingEntry
	var merges []*Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		if isMergeKey(key) {
			targets, err := mergeTargets(value, stack)
			if err != nil {
				return nil, err
			}
			merges = append(merges, targets...)
			continue
		}
		explicit = append(explicit, mappingEntry{key: key, value: value})
	}

	result := make([]mappingEntry, 0, len(explicit))
	present := make(map[string]bool)
	for _, target := range merges {
		entries, err := resolveMapping(target, stack)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			identity := nodeIdentity(entry.key)
			if present[identity] {
				continue
			}
			present[identity] = true
			result = append(result, entry)
		}
	}
	for _, entry := range explicit {
		identity := nodeIdentity(entry.key)
		if present[identity] {
			for i := range result {
				if nodeIdentity(result[i].key) == identity {
					result = append(result[:i], result[i+1:]...)
					break
				}
			}
		}
		present[identity] = true
		result = append(result, entry)
	}
	return result, nil
}

func mergeTargets(value *Node, stack map[*Node]bool) ([]*Node, error) {
	if value == nil {
		return nil, nil
	}
	if value.Kind == AliasNode {
		if value.Alias == nil {
			return nil, fmt.Errorf("yaml: line %d: merge alias has no target", value.Line)
		}
		return mergeTargets(value.Alias, stack)
	}
	switch value.Kind {
	case MappingNode:
		return []*Node{value}, nil
	case SequenceNode:
		var targets []*Node
		for _, child := range value.Content {
			items, err := mergeTargets(child, stack)
			if err != nil {
				return nil, err
			}
			targets = append(targets, items...)
		}
		return targets, nil
	default:
		return nil, fmt.Errorf("yaml: line %d: invalid merge value", value.Line)
	}
}

func isMergeKey(node *Node) bool {
	if node == nil || node.Kind != ScalarNode || node.Value != "<<" {
		return false
	}
	switch shortTag(node.Tag) {
	case "", "!!merge":
		return node.Style&DoubleQuotedStyle == 0 && node.Style&SingleQuotedStyle == 0
	default:
		return false
	}
}

func nodeIdentity(node *Node) string {
	if node == nil {
		return "<nil>"
	}
	if node.Kind == AliasNode {
		return nodeIdentity(node.Alias)
	}
	if node.Kind == ScalarNode {
		return shortTag(node.Tag) + "\x00" + node.Value
	}
	return fmt.Sprintf("%p", node)
}
