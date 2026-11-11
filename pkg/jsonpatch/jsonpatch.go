// Package jsonpatch implements a limited RFC 6902 JSON Patch.
//
// The supported operation set is intentionally limited to the six standard
// operations "add", "remove", "replace", "move", "copy" and "test". Patches
// are applied strictly in order: every array index is interpreted against the
// document view produced by the preceding operations.
//
// Apply never mutates its input document. A failing "test" aborts the whole
// patch and the partially modified working copy is discarded, so callers can
// treat a patch as an atomic transaction.
package jsonpatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Op is a single RFC 6902 patch operation. Value is kept as raw JSON so that
// booleans, numbers, strings, arrays and objects all round-trip faithfully.
type Op struct {
	Op    string           `json:"op"`
	Path  string           `json:"path"`
	From  string           `json:"from,omitempty"`
	Value *json.RawMessage `json:"value,omitempty"`
}

// ErrTestFailed is the sentinel wrapped by every failing "test" operation.
var ErrTestFailed = errors.New("jsonpatch: test operation failed")

// TestFailureError describes a concrete "test" mismatch.
type TestFailureError struct {
	Index int    // operation index within the patch
	Path  string // JSON Pointer of the tested value
	Want  any    // expected value
	Got   any    // value found in the document
}

func (e *TestFailureError) Error() string {
	return fmt.Sprintf("jsonpatch: test failed at op %d path %q: expected %v, got %v",
		e.Index, e.Path, e.Want, e.Got)
}

// Unwrap enables errors.Is(err, ErrTestFailed).
func (e *TestFailureError) Unwrap() error { return ErrTestFailed }

// ParsePointer parses an RFC 6901 JSON Pointer into its raw tokens, decoding
// the "~1" ("/") and "~0" ("~") escapes. The empty pointer yields no tokens.
func ParsePointer(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("jsonpatch: invalid JSON Pointer %q (must be empty or start with \"/\")", pointer)
	}
	tokens := strings.Split(pointer[1:], "/")
	for i, token := range tokens {
		token = strings.ReplaceAll(token, "~1", "/")
		token = strings.ReplaceAll(token, "~0", "~")
		tokens[i] = token
	}
	return tokens, nil
}

// GetValue resolves a JSON Pointer against a decoded JSON tree.
func GetValue(doc any, tokens []string) (any, error) {
	cur := doc
	for i, token := range tokens {
		switch node := cur.(type) {
		case map[string]any:
			value, ok := node[token]
			if !ok {
				return nil, fmt.Errorf("jsonpatch: key %q not found at token %d", token, i)
			}
			cur = value
		case []any:
			index, err := arrayIndex(token, len(node), false)
			if err != nil {
				return nil, fmt.Errorf("jsonpatch: token %d: %w", i, err)
			}
			cur = node[index]
		default:
			return nil, fmt.Errorf("jsonpatch: cannot traverse %T with token %q at %d", cur, token, i)
		}
	}
	return cur, nil
}

// Apply runs every operation against a deep copy of doc and returns the result.
// The input document is never modified. On any error (in particular a failing
// test) the working copy is abandoned and the original document stays intact.
func Apply(doc any, ops []Op) (any, error) {
	root, err := DeepCopy(doc)
	if err != nil {
		return nil, fmt.Errorf("jsonpatch: cannot snapshot document: %w", err)
	}
	for i, op := range ops {
		root, err = applyOne(root, i, op)
		if err != nil {
			return nil, err
		}
	}
	return root, nil
}

func applyOne(root any, index int, op Op) (any, error) {
	path, err := ParsePointer(op.Path)
	if err != nil {
		return nil, fmt.Errorf("jsonpatch: op %d: %w", index, err)
	}
	switch op.Op {
	case "add":
		value, err := decodeValue(index, op)
		if err != nil {
			return nil, err
		}
		return addAt(root, path, value)
	case "remove":
		if len(path) == 0 {
			return nil, fmt.Errorf("jsonpatch: op %d: cannot remove the whole document", index)
		}
		newRoot, _, err := removeAt(root, path)
		return newRoot, err
	case "replace":
		value, err := decodeValue(index, op)
		if err != nil {
			return nil, err
		}
		return replaceAt(root, path, value)
	case "move":
		from, err := ParsePointer(op.From)
		if err != nil {
			return nil, fmt.Errorf("jsonpatch: op %d: %w", index, err)
		}
		if len(from) == 0 {
			return nil, fmt.Errorf("jsonpatch: op %d: cannot move from the whole document", index)
		}
		if isPrefix(from, path) {
			return nil, fmt.Errorf("jsonpatch: op %d: cannot move %q into its own subtree %q", index, op.From, op.Path)
		}
		newRoot, removed, err := removeAt(root, from)
		if err != nil {
			return nil, err
		}
		return addAt(newRoot, path, removed)
	case "copy":
		from, err := ParsePointer(op.From)
		if err != nil {
			return nil, fmt.Errorf("jsonpatch: op %d: %w", index, err)
		}
		value, err := GetValue(root, from)
		if err != nil {
			return nil, fmt.Errorf("jsonpatch: op %d: %w", index, err)
		}
		snapshot, err := DeepCopy(value)
		if err != nil {
			return nil, err
		}
		return addAt(root, path, snapshot)
	case "test":
		want, err := decodeValue(index, op)
		if err != nil {
			return nil, err
		}
		got, err := GetValue(root, path)
		if err != nil {
			return nil, fmt.Errorf("jsonpatch: op %d: %w", index, err)
		}
		if !reflect.DeepEqual(got, want) {
			return nil, &TestFailureError{Index: index, Path: op.Path, Want: want, Got: got}
		}
		return root, nil
	default:
		return nil, fmt.Errorf("jsonpatch: op %d: unsupported op %q (allowed: add, remove, replace, move, copy, test)", index, op.Op)
	}
}

func decodeValue(index int, op Op) (any, error) {
	if op.Value == nil {
		return nil, fmt.Errorf("jsonpatch: op %d (%s): missing \"value\"", index, op.Op)
	}
	var value any
	if err := json.Unmarshal(*op.Value, &value); err != nil {
		return nil, fmt.Errorf("jsonpatch: op %d: invalid value: %w", index, err)
	}
	return value, nil
}

// addAt inserts value at tokens. For arrays the index addresses the view after
// earlier operations, and "-" appends.
func addAt(node any, tokens []string, value any) (any, error) {
	if len(tokens) == 0 {
		return value, nil
	}
	key := tokens[0]
	if len(tokens) == 1 {
		switch container := node.(type) {
		case map[string]any:
			container[key] = value
			return node, nil
		case []any:
			index, err := arrayIndex(key, len(container), true)
			if err != nil {
				return nil, err
			}
			out := make([]any, 0, len(container)+1)
			out = append(out, container[:index]...)
			out = append(out, value)
			out = append(out, container[index:]...)
			return out, nil
		default:
			return nil, fmt.Errorf("jsonpatch: cannot add into %T", node)
		}
	}
	child, err := childAt(node, key)
	if err != nil {
		return nil, err
	}
	newChild, err := addAt(child, tokens[1:], value)
	if err != nil {
		return nil, err
	}
	return setChild(node, key, newChild)
}

func removeAt(node any, tokens []string) (any, any, error) {
	key := tokens[0]
	if len(tokens) == 1 {
		switch container := node.(type) {
		case map[string]any:
			value, ok := container[key]
			if !ok {
				return nil, nil, fmt.Errorf("jsonpatch: key %q not found", key)
			}
			delete(container, key)
			return node, value, nil
		case []any:
			index, err := arrayIndex(key, len(container), false)
			if err != nil {
				return nil, nil, err
			}
			value := container[index]
			out := make([]any, 0, len(container)-1)
			out = append(out, container[:index]...)
			out = append(out, container[index+1:]...)
			return out, value, nil
		default:
			return nil, nil, fmt.Errorf("jsonpatch: cannot remove from %T", node)
		}
	}
	child, err := childAt(node, key)
	if err != nil {
		return nil, nil, err
	}
	newChild, removed, err := removeAt(child, tokens[1:])
	if err != nil {
		return nil, nil, err
	}
	newRoot, err := setChild(node, key, newChild)
	return newRoot, removed, err
}

func replaceAt(node any, tokens []string, value any) (any, error) {
	if len(tokens) == 0 {
		return value, nil
	}
	key := tokens[0]
	if len(tokens) == 1 {
		switch container := node.(type) {
		case map[string]any:
			if _, ok := container[key]; !ok {
				return nil, fmt.Errorf("jsonpatch: cannot replace missing key %q", key)
			}
			container[key] = value
			return node, nil
		case []any:
			index, err := arrayIndex(key, len(container), false)
			if err != nil {
				return nil, err
			}
			container[index] = value
			return node, nil
		default:
			return nil, fmt.Errorf("jsonpatch: cannot replace inside %T", node)
		}
	}
	child, err := childAt(node, key)
	if err != nil {
		return nil, err
	}
	newChild, err := replaceAt(child, tokens[1:], value)
	if err != nil {
		return nil, err
	}
	return setChild(node, key, newChild)
}

func childAt(node any, key string) (any, error) {
	switch container := node.(type) {
	case map[string]any:
		child, ok := container[key]
		if !ok {
			return nil, fmt.Errorf("jsonpatch: key %q not found", key)
		}
		return child, nil
	case []any:
		index, err := arrayIndex(key, len(container), false)
		if err != nil {
			return nil, err
		}
		return container[index], nil
	default:
		return nil, fmt.Errorf("jsonpatch: cannot traverse %T", node)
	}
}

func setChild(node any, key string, child any) (any, error) {
	switch container := node.(type) {
	case map[string]any:
		container[key] = child
		return node, nil
	case []any:
		index, err := arrayIndex(key, len(container), false)
		if err != nil {
			return nil, err
		}
		container[index] = child
		return node, nil
	default:
		return nil, fmt.Errorf("jsonpatch: cannot update %T", node)
	}
}

func arrayIndex(token string, length int, allowEnd bool) (int, error) {
	if token == "-" {
		if allowEnd {
			return length, nil
		}
		return 0, fmt.Errorf("array index \"-\" is only valid for add")
	}
	if len(token) > 1 && token[0] == '0' {
		return 0, fmt.Errorf("invalid array index %q (leading zero)", token)
	}
	index, err := strconv.Atoi(token)
	if err != nil || index < 0 {
		return 0, fmt.Errorf("invalid array index %q", token)
	}
	limit := length - 1
	if allowEnd {
		limit = length
	}
	if index > limit {
		return 0, fmt.Errorf("array index %d out of range (length %d)", index, length)
	}
	return index, nil
}

func isPrefix(prefix, path []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if prefix[i] != path[i] {
			return false
		}
	}
	return true
}

// DeepCopy returns a value that shares no mutable structure with doc, using a
// canonical JSON round-trip so the result is a plain decoded JSON tree.
func DeepCopy(doc any) (any, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
