package docexec

import (
	"fmt"
	"strings"
)

// Namespace is the shared variable environment across blocks.
type Namespace struct {
	vars map[string]string
}

// NewNamespace returns an empty shared namespace.
func NewNamespace() *Namespace {
	return &Namespace{vars: make(map[string]string)}
}

// Get looks up a variable in the shared namespace.
func (n *Namespace) Get(name string) (string, bool) {
	v, ok := n.vars[name]
	return v, ok
}

// Snapshot returns a copy of the visible variables (used by tests and reports).
func (n *Namespace) Snapshot() map[string]string {
	out := make(map[string]string, len(n.vars))
	for k, v := range n.vars {
		out[k] = v
	}
	return out
}

// tx is a per-block transaction overlay: writes land in pending and are
// committed to the shared namespace only if the whole block succeeds,
// so a failed block cannot leak partial state.
type tx struct {
	base    *Namespace
	pending map[string]string
}

func (t *tx) get(name string) (string, bool) {
	if v, ok := t.pending[name]; ok {
		return v, true
	}
	return t.base.Get(name)
}

func (t *tx) set(name, value string) error {
	if _, ok := t.get(name); ok {
		return fmt.Errorf("duplicate definition of %q", name)
	}
	t.pending[name] = value
	return nil
}

func (t *tx) commit() {
	for k, v := range t.pending {
		t.base.vars[k] = v
	}
}

// runInstructions interprets code against ns. On success it returns the
// printed output lines and commits all writes; on failure it returns the
// output produced so far plus the error, and no write is committed.
func runInstructions(code string, ns *Namespace) ([]string, error) {
	t := &tx{base: ns, pending: make(map[string]string)}
	var output []string
	for lineno, raw := range strings.Split(code, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		op, args := fields[0], fields[1:]
		var err error
		switch op {
		case "set":
			err = opSet(t, args)
		case "print":
			var text string
			text, err = expand(t, strings.Join(args, " "))
			if err == nil {
				output = append(output, text)
			}
		case "fail":
			msg := strings.Join(args, " ")
			if msg == "" {
				msg = "explicit fail"
			}
			err = fmt.Errorf("%s", msg)
		default:
			err = fmt.Errorf("unknown instruction %q", op)
		}
		if err != nil {
			return output, fmt.Errorf("line %d: %v", lineno+1, err)
		}
	}
	t.commit()
	return output, nil
}

func opSet(t *tx, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("set requires a name")
	}
	name := args[0]
	if !isIdent(name) {
		return fmt.Errorf("invalid variable name %q", name)
	}
	value, err := expand(t, strings.Join(args[1:], " "))
	if err != nil {
		return err
	}
	return t.set(name, value)
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// expand substitutes $name and ${name} references from the transaction view.
// An undefined reference is an error, which fails the enclosing block.
func expand(t *tx, s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '$' {
			b.WriteByte('$')
			i += 2
			continue
		}
		var name string
		var next int
		if i+1 < len(s) && s[i+1] == '{' {
			j := strings.IndexByte(s[i+2:], '}')
			if j < 0 {
				return "", fmt.Errorf("unterminated ${...} reference")
			}
			name = s[i+2 : i+2+j]
			next = i + 2 + j + 1
		} else {
			j := i + 1
			for j < len(s) && isIdentByte(s[j], j == i+1) {
				j++
			}
			name = s[i+1 : j]
			next = j
		}
		if name == "" {
			b.WriteByte('$')
			i++
			continue
		}
		v, ok := t.get(name)
		if !ok {
			return "", fmt.Errorf("undefined variable %q", name)
		}
		b.WriteString(v)
		i = next
	}
	return b.String(), nil
}

func isIdentByte(c byte, first bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		return true
	case c >= '0' && c <= '9':
		return !first
	}
	return false
}
