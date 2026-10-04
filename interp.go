package docexec

import (
	"fmt"
	"os"
	"strings"
)

// namespace is the shared variable environment across blocks.
type namespace map[string]string

func (ns namespace) clone() namespace {
	out := make(namespace, len(ns))
	for k, v := range ns {
		out[k] = v
	}
	return out
}

// Output is one emitted line, mapped back to its source line.
type Output struct {
	Line int    // 1-based document line of the emit instruction
	Text string // expanded text
}

// instrError carries the document line where execution failed.
type instrError struct {
	Line int
	Msg  string
}

func (e *instrError) Error() string { return e.Msg }

// runBlock executes one block's instructions against ns, mutating it in
// place. The caller is responsible for transactional semantics (run against
// a clone, commit on success). It never calls external commands; the whole
// instruction set is interpreted locally:
//
//	set NAME rest...      define NAME (fails if already defined)
//	emit text...          print text with $NAME / ${NAME} expansion
//	expect NAME rest...   fail unless NAME's value equals rest
//	fail text...          unconditionally fail the block
//	# comment / blank     ignored
func runBlock(ns namespace, b *Block) ([]Output, error) {
	var outs []Output
	for _, in := range b.Lines {
		text := in.Text
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		op := fields[0]
		args := fields[1:]
		fail := func(format string, a ...interface{}) error {
			return &instrError{Line: in.Line, Msg: fmt.Sprintf(format, a...)}
		}
		switch op {
		case "set":
			if len(args) < 1 {
				return nil, fail("set requires a name")
			}
			name := args[0]
			if !isValidName(name) {
				return nil, fail("invalid variable name %q", name)
			}
			if _, exists := ns[name]; exists {
				return nil, fail("variable %q already defined", name)
			}
			value, err := expand(strings.Join(args[1:], " "), ns)
			if err != nil {
				return nil, fail("%v", err)
			}
			ns[name] = value
		case "emit":
			value, err := expand(strings.Join(args, " "), ns)
			if err != nil {
				return nil, fail("%v", err)
			}
			outs = append(outs, Output{Line: in.Line, Text: value})
		case "expect":
			if len(args) < 1 {
				return nil, fail("expect requires a name")
			}
			name := args[0]
			got, ok := ns[name]
			if !ok {
				return nil, fail("undefined variable %q", name)
			}
			want, err := expand(strings.Join(args[1:], " "), ns)
			if err != nil {
				return nil, fail("%v", err)
			}
			if got != want {
				return nil, fail("expect %q = %q, got %q", name, want, got)
			}
		case "fail":
			return nil, fail("explicit fail: %s", strings.Join(args, " "))
		default:
			return nil, fail("unknown instruction %q", op)
		}
	}
	return outs, nil
}

// expand substitutes $NAME and ${NAME} using ns. Undefined variables are an
// error (unlike shell expansion, which would silently yield empty strings).
func expand(s string, ns namespace) (string, error) {
	var missing []string
	out := os.Expand(s, func(name string) string {
		v, ok := ns[name]
		if !ok {
			missing = append(missing, name)
			return ""
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("undefined variable(s): %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func isValidName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
