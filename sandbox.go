// Package netsec derives the effective filesystem sandbox view of a
// systemd-style service unit, so hardening review can flag directives
// whose ordering silently re-opens write access to system paths.
//
// Directives act on the path view in the exact order they appear in the
// unit: ProtectSystem=strict makes the whole root read-only, full makes
// /usr, /boot and /etc read-only, ReadWritePaths punches holes on top,
// InaccessiblePaths has the highest priority and hides all of its
// subpaths, and BindPaths targets inherit the read-only state of the
// view location they land on.
package netsec

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"strings"
)

// Access describes how a path is exposed to the service process.
type Access int

const (
	// ReadWrite means the process may modify the path.
	ReadWrite Access = iota
	// ReadOnly means the path is visible but cannot be modified.
	ReadOnly
	// Inaccessible means the path and everything below it is hidden.
	Inaccessible
)

func (a Access) String() string {
	switch a {
	case ReadWrite:
		return "read-write"
	case ReadOnly:
		return "read-only"
	case Inaccessible:
		return "inaccessible"
	}
	return "unknown"
}

// Kind identifies a sandbox directive.
type Kind int

const (
	KindProtectSystem Kind = iota
	KindReadOnlyPaths
	KindReadWritePaths
	KindInaccessiblePaths
	KindBindPaths
)

// Directive is a single sandbox directive as it appears in a unit, in
// order. Value is the raw right-hand side, e.g. "strict" for
// ProtectSystem or a whitespace-separated path list for the others.
type Directive struct {
	Kind  Kind
	Value string
}

// Bind is a parsed BindPaths entry.
type Bind struct {
	Source  string
	Target  string
	Options string
}

// rule is one view mutation: every path under prefix switches to mode.
type rule struct {
	prefix string
	mode   Access
}

// View is the derived sandbox view of a unit.
type View struct {
	rules []rule // applied in unit order
	binds []Bind
}

// NewView builds a View by applying directives in order.
func NewView(directives ...Directive) (*View, error) {
	v := &View{}
	for i, d := range directives {
		if err := v.apply(d); err != nil {
			return nil, fmt.Errorf("netsec: directive %d: %w", i+1, err)
		}
	}
	return v, nil
}

// ParseUnit builds a View from unit-file text, honoring directive order.
// Unrelated keys, comments and section headers are ignored.
func ParseUnit(r io.Reader) (*View, error) {
	v := &View{}
	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		var kind Kind
		switch strings.TrimSpace(key) {
		case "ProtectSystem":
			kind = KindProtectSystem
		case "ReadOnlyPaths":
			kind = KindReadOnlyPaths
		case "ReadWritePaths":
			kind = KindReadWritePaths
		case "InaccessiblePaths":
			kind = KindInaccessiblePaths
		case "BindPaths":
			kind = KindBindPaths
		default:
			continue
		}
		if err := v.apply(Directive{Kind: kind, Value: value}); err != nil {
			return nil, fmt.Errorf("netsec: line %d: %w", lineNo, err)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *View) apply(d Directive) error {
	switch d.Kind {
	case KindProtectSystem:
		switch strings.ToLower(strings.TrimSpace(d.Value)) {
		case "strict":
			v.rules = append(v.rules, rule{"/", ReadOnly})
		case "full":
			for _, p := range []string{"/usr", "/boot", "/etc"} {
				v.rules = append(v.rules, rule{p, ReadOnly})
			}
		case "yes", "true":
			for _, p := range []string{"/usr", "/boot"} {
				v.rules = append(v.rules, rule{p, ReadOnly})
			}
		case "no", "false", "":
		default:
			return fmt.Errorf("unknown ProtectSystem level %q", d.Value)
		}
	case KindReadOnlyPaths, KindReadWritePaths, KindInaccessiblePaths:
		mode := map[Kind]Access{
			KindReadOnlyPaths:     ReadOnly,
			KindReadWritePaths:    ReadWrite,
			KindInaccessiblePaths: Inaccessible,
		}[d.Kind]
		for _, p := range strings.Fields(d.Value) {
			v.rules = append(v.rules, rule{normalize(p), mode})
		}
	case KindBindPaths:
		for _, token := range strings.Fields(d.Value) {
			b, err := parseBind(token)
			if err != nil {
				return err
			}
			v.binds = append(v.binds, b)
		}
	}
	return nil
}

// Access resolves how path is exposed in the view. InaccessiblePaths
// wins over everything else regardless of order; among the remaining
// rules the last one covering the path decides.
func (v *View) Access(p string) Access {
	target := normalize(p)
	for _, r := range v.rules {
		if r.mode == Inaccessible && covers(r.prefix, target) {
			return Inaccessible
		}
	}
	mode := ReadWrite
	for _, r := range v.rules {
		if r.mode != Inaccessible && covers(r.prefix, target) {
			mode = r.mode
		}
	}
	return mode
}

// Binds returns the bind mounts declared by BindPaths. A bind target
// inherits the read-only state of its location in the view, so its
// effective access is Access(bind.Target).
func (v *View) Binds() []Bind {
	return append([]Bind(nil), v.binds...)
}

func parseBind(token string) (Bind, error) {
	parts := strings.SplitN(token, ":", 3)
	if parts[0] == "" {
		return Bind{}, fmt.Errorf("invalid BindPaths entry %q", token)
	}
	b := Bind{Source: normalize(parts[0])}
	b.Target = b.Source
	if len(parts) >= 2 && parts[1] != "" {
		b.Target = normalize(parts[1])
	}
	if len(parts) == 3 {
		b.Options = parts[2]
	}
	return b, nil
}

// normalize cleans a path and roots it at "/".
func normalize(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// covers reports whether prefix is p or one of its ancestors.
func covers(prefix, p string) bool {
	if prefix == "/" {
		return true
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}
