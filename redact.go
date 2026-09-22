package safeframe

import (
	"bytes"
	"errors"
	"strings"
)

var (
	ErrEmptyPath      = errors.New("safeframe: empty redaction path")
	ErrDuplicatePath  = errors.New("safeframe: duplicate redaction path")
	ErrPathNotFound   = errors.New("safeframe: redaction path not found")
	ErrPathNotScalar  = errors.New("safeframe: redaction path is not a scalar field")
	ErrPathInUnknown  = errors.New("safeframe: redaction path enters unknown extension")
	ErrSecretLeakage  = errors.New("safeframe: original sensitive bytes found in redacted output")
	ErrBadReplacement = errors.New("safeframe: invalid redaction replacement")
)

type Path []string

type RedactOptions struct {
	Strict      bool
	Replacement []byte
	MaxPayload  int
}

func ParsePath(value string) (Path, error) {
	if value == "" {
		return nil, ErrEmptyPath
	}
	parts := strings.Split(value, ".")
	path := make(Path, len(parts))
	for i, part := range parts {
		if part == "" {
			return nil, ErrEmptyPath
		}
		path[i] = part
	}
	return path, nil
}

func Redact(frame *Frame, paths []Path, options RedactOptions) (*Frame, []byte, error) {
	if frame == nil {
		return nil, nil, ErrPathNotFound
	}

	maxPayload := options.MaxPayload
	if maxPayload <= 0 {
		maxPayload = MaxPayloadSize
	}
	maxPayload = normalizePayloadLimit(maxPayload)

	replacement := options.Replacement
	if replacement == nil {
		replacement = []byte("***")
	}
	if len(replacement) > maxPayload {
		return nil, nil, ErrBadReplacement
	}

	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if len(path) == 0 {
			return nil, nil, ErrEmptyPath
		}
		key := strings.Join(path, "\x00")
		if seen[key] {
			return nil, nil, ErrDuplicatePath
		}
		seen[key] = true
	}

	for _, path := range paths {
		_, _, _, err := selectScalarTargets(nil, frame.Root, path, options.Strict)
		if err != nil {
			return nil, nil, err
		}
	}

	out := cloneFrame(frame)
	out.Flags |= FlagRedacted
	secrets := make([][]byte, 0)

	for _, path := range paths {
		found, targets, changed, err := selectScalarTargets(nil, out.Root, path, options.Strict)
		if err != nil {
			return nil, nil, err
		}
		if !found && options.Strict {
			return nil, nil, ErrPathNotFound
		}
		for _, target := range targets {
			replaceScalar(target, replacement)
		}
		secrets = append(secrets, changed...)
	}

	wire, err := out.MarshalWithPayloadLimit(maxPayload)
	if err != nil {
		return nil, nil, err
	}
	if len(wire)-HeaderLength > maxPayload {
		return nil, nil, ErrPayloadLimit
	}

	for _, secret := range secrets {
		if len(secret) == 0 {
			continue
		}
		if bytes.Contains(replacement, secret) || rootContainsSecret(out.Root, secret) {
			return nil, nil, ErrSecretLeakage
		}
	}

	return out, wire, nil
}

func rootContainsSecret(fields Root, secret []byte) bool {
	for _, field := range fields {
		if field == nil {
			continue
		}
		if bytes.Contains([]byte(field.Name), secret) {
			return true
		}
		switch field.Type {
		case TypeBytes:
			if bytes.Contains(field.Bytes, secret) {
				return true
			}
		case TypeString:
			if bytes.Contains([]byte(field.Text), secret) {
				return true
			}
		case TypeMessage:
			if rootContainsSecret(field.Root, secret) {
				return true
			}
		default:
			if bytes.Contains(field.Extra, secret) {
				return true
			}
		}
	}
	return false
}

func selectScalarTargets(_ []*Field, fields Root, path Path, strict bool) (bool, []*Field, [][]byte, error) {
	if len(path) == 0 {
		return false, nil, nil, ErrEmptyPath
	}

	name := path[0]
	rest := path[1:]
	found := false
	var targets []*Field
	var secrets [][]byte

	for _, field := range fields {
		if field == nil || field.Name != name {
			continue
		}

		switch {
		case len(rest) == 0:
			found = true
			if field.Type != TypeBytes && field.Type != TypeString {
				return true, nil, nil, ErrPathNotScalar
			}
			targets = append(targets, field)
			secrets = append(secrets, scalarBytes(field))
		case field.Type == TypeMessage:
			childFound, childTargets, childSecrets, err := selectScalarTargets(nil, field.Root, rest, strict)
			if err != nil {
				return true, nil, nil, err
			}
			found = found || childFound
			targets = append(targets, childTargets...)
			secrets = append(secrets, childSecrets...)
		case field.Type == TypeBytes || field.Type == TypeString:
			if strict {
				return found, nil, nil, ErrPathNotFound
			}
		default:
			return found, nil, nil, ErrPathInUnknown
		}
	}

	return found, targets, secrets, nil
}

func scalarBytes(field *Field) []byte {
	if field.Type == TypeString {
		return []byte(field.Text)
	}
	return append([]byte(nil), field.Bytes...)
}

func replaceScalar(field *Field, replacement []byte) {
	value := append([]byte(nil), replacement...)
	field.Bytes = nil
	field.Text = ""
	field.Extra = nil
	if field.Type == TypeString {
		field.Text = string(value)
		return
	}
	field.Bytes = value
}

func cloneFrame(frame *Frame) *Frame {
	return &Frame{
		Version:       frame.Version,
		Flags:         frame.Flags,
		Sequence:      frame.Sequence,
		TimestampNano: frame.TimestampNano,
		Root:          cloneRoot(frame.Root),
	}
}

func cloneRoot(fields Root) Root {
	out := make(Root, 0, len(fields))
	for _, field := range fields {
		cloned := &Field{Name: field.Name, Type: field.Type}
		switch field.Type {
		case TypeBytes:
			cloned.Bytes = append([]byte(nil), field.Bytes...)
		case TypeString:
			cloned.Text = field.Text
		case TypeMessage:
			cloned.Root = cloneRoot(field.Root)
		default:
			cloned.Extra = append([]byte(nil), field.Extra...)
		}
		out = append(out, cloned)
	}
	return out
}
