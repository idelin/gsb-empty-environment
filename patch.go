package netsec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// validatePointer enforces RFC 6901 syntax.
func validatePointer(path string) error {
	if path != "" && !strings.HasPrefix(path, "/") {
		return errors.Join(errPointer, errors.New(path+" must start with /"))
	}
	return nil
}

func pointerTokens(path string) []string {
	if path == "" {
		return nil
	}
	raw := strings.Split(path[1:], "/")
	tokens := make([]string, len(raw))
	for i, t := range raw {
		t = strings.ReplaceAll(t, "~1", "/")
		t = strings.ReplaceAll(t, "~0", "~")
		tokens[i] = t
	}
	return tokens
}

func pointerGet(root any, path string) (any, bool, error) {
	if err := validatePointer(path); err != nil {
		return nil, false, err
	}
	cur := root
	for _, token := range pointerTokens(path) {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[token]
			if !ok {
				return nil, false, nil
			}
			cur = next
		case []any:
			idx, err := arrayIndex(token, len(node)+1)
			if err != nil {
				return nil, false, err
			}
			if idx >= len(node) {
				return nil, false, nil
			}
			cur = node[idx]
		default:
			return nil, false, nil
		}
	}
	return cur, true, nil
}

func pointerSet(root any, path string, value any) error {
	tokens := pointerTokens(path)
	if len(tokens) == 0 {
		return errors.Join(errPointer, errors.New("cannot replace document root"))
	}
	last := tokens[len(tokens)-1]
	parent, err := traverseParent(root, tokens[:len(tokens)-1])
	if err != nil {
		return err
	}
	switch node := parent.(type) {
	case map[string]any:
		node[last] = value
	case []any:
		idx, err := arrayIndex(last, len(node))
		if err != nil {
			return err
		}
		if idx == len(node) {
			return writeBack(root, tokens[:len(tokens)-1], append(node, value))
		}
		node[idx] = value
	default:
		return errors.Join(errPointer, errors.New("parent is neither object nor array"))
	}
	return nil
}

func pointerRemove(root any, path string) (any, error) {
	tokens := pointerTokens(path)
	if len(tokens) == 0 {
		return nil, errors.Join(errPointer, errors.New("cannot remove document root"))
	}
	last := tokens[len(tokens)-1]
	parent, err := traverseParent(root, tokens[:len(tokens)-1])
	if err != nil {
		return nil, err
	}
	switch node := parent.(type) {
	case map[string]any:
		old, ok := node[last]
		if !ok {
			return nil, errors.Join(errPointer, errors.New("path not found: "+path))
		}
		delete(node, last)
		return old, nil
	case []any:
		idx, err := arrayIndex(last, len(node))
		if err != nil {
			return nil, err
		}
		old := node[idx]
		return old, writeBack(root, tokens[:len(tokens)-1], append(node[:idx], node[idx+1:]...))
	default:
		return nil, errors.Join(errPointer, errors.New("path not found: "+path))
	}
}

// traverseParent walks every token except the last, creating missing object
// segments on demand.
func traverseParent(root any, tokens []string) (any, error) {
	cur := root
	for i, token := range tokens {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[token]
			if !ok {
				next = map[string]any{}
				node[token] = next
			}
			cur = next
		case []any:
			idx, err := arrayIndex(token, len(node))
			if err != nil {
				return nil, err
			}
			cur = node[idx]
		default:
			return nil, errors.Join(errPointer, fmt.Errorf("segment %q at %d is not traversable", token, i))
		}
	}
	return cur, nil
}

// writeBack assigns a replacement slice/value to the location addressed by
// tokens. Maps are reference types and mutate in place; only array slots need
// explicit write-back.
func writeBack(root any, tokens []string, value any) error {
	cur := root
	for i, token := range tokens {
		node, ok := cur.(map[string]any)
		if !ok {
			arr, isArray := cur.([]any)
			if !isArray {
				return errors.Join(errPointer, errors.New("unexpected parent while writing back"))
			}
			idx, err := arrayIndex(token, len(arr))
			if err != nil {
				return err
			}
			if i == len(tokens)-1 {
				arr[idx] = value
				return nil
			}
			cur = arr[idx]
			continue
		}
		if i == len(tokens)-1 {
			node[token] = value
			return nil
		}
		cur = node[token]
	}
	return errors.Join(errPointer, errors.New("write-back target not reached"))
}

func arrayIndex(token string, length int) (int, error) {
	if token == "-" {
		return length, nil
	}
	if token == "" || (len(token) > 1 && token[0] == '0') {
		return 0, errors.Join(errPointer, errors.New("bad array index: "+token))
	}
	idx, err := strconv.Atoi(token)
	if err != nil || idx < 0 {
		return 0, errors.Join(errPointer, errors.New("bad array index: "+token))
	}
	if idx > length {
		return 0, errors.Join(errPointer, fmt.Errorf("array index %d out of range", idx))
	}
	return idx, nil
}

// ApplyPatch executes RFC 6902-style operations against a raw map tree and
// returns the transformed tree plus per-operation structural records.
func ApplyPatch(root map[string]any, ops []PatchOperation) (map[string]any, []ChangeDecision, error) {
	work := deepClone(root)
	records := make([]ChangeDecision, 0, len(ops))
	for i, op := range ops {
		record, err := applyOne(work, op)
		if err != nil {
			return nil, nil, errors.Join(errPatch, fmt.Errorf("operation %d (%s): %w", i, op.Op, err))
		}
		records = append(records, record)
	}
	return work, records, nil
}

func applyOne(root map[string]any, op PatchOperation) (ChangeDecision, error) {
	if err := validatePointer(op.Path); err != nil {
		return ChangeDecision{}, err
	}
	record := ChangeDecision{Op: op.Op, Path: op.Path, From: op.From, Field: op.Path}
	switch op.Op {
	case "add", "replace":
		old, existed, err := pointerGet(root, op.Path)
		if err != nil {
			return ChangeDecision{}, err
		}
		if existed {
			record.OldValue = old
		}
		record.NewValue = op.Value
		if existed && deepEqual(old, op.Value) {
			record.Verdict = VerdictAllow
			record.Basis = BasisNoOp
		}
		return record, pointerSet(root, op.Path, op.Value)
	case "remove":
		old, existed, err := pointerGet(root, op.Path)
		if err != nil {
			return ChangeDecision{}, err
		}
		if !existed {
			return ChangeDecision{}, errors.New("remove target does not exist: " + op.Path)
		}
		record.OldValue = old
		if _, err := pointerRemove(root, op.Path); err != nil {
			return ChangeDecision{}, err
		}
		return record, nil
	case "move":
		if err := validatePointer(op.From); err != nil {
			return ChangeDecision{}, err
		}
		value, existed, err := pointerGet(root, op.From)
		if err != nil {
			return ChangeDecision{}, err
		}
		if !existed {
			return ChangeDecision{}, errors.New("move source does not exist: " + op.From)
		}
		record.OldValue = value
		if _, err := pointerRemove(root, op.From); err != nil {
			return ChangeDecision{}, err
		}
		if err := pointerSet(root, op.Path, value); err != nil {
			return ChangeDecision{}, err
		}
		record.NewValue = value
		return record, nil
	case "copy":
		if err := validatePointer(op.From); err != nil {
			return ChangeDecision{}, err
		}
		value, existed, err := pointerGet(root, op.From)
		if err != nil {
			return ChangeDecision{}, err
		}
		if !existed {
			return ChangeDecision{}, errors.New("copy source does not exist: " + op.From)
		}
		record.OldValue = value
		record.NewValue = value
		return record, pointerSet(root, op.Path, deepClone(value))
	case "test":
		got, _, err := pointerGet(root, op.Path)
		if err != nil {
			return ChangeDecision{}, err
		}
		if !deepEqual(got, op.Value) {
			return ChangeDecision{}, errors.New("test failed at " + op.Path)
		}
		record.Verdict = VerdictAllow
		record.Basis = BasisNoOp
		return record, nil
	default:
		return ChangeDecision{}, errors.New("unsupported op: " + op.Op)
	}
}
