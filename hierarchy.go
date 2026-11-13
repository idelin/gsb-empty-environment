package netsec

import "fmt"

// Well-known classes every hierarchy is expected to contain.
const (
	ObjectClass    = "Object"
	ThrowableClass = "Throwable"
)

// Hierarchy is the finite class tree handed to the verifier by the host.
// It is rooted at Object; every other class has exactly one parent.
type Hierarchy struct {
	parent map[string]string
}

// NewHierarchy builds a Hierarchy from a class -> parent-class map.
// Object is inserted as the root if absent. Every class must be able to
// reach Object by following parent links; cycles and dangling parents
// are rejected.
func NewHierarchy(parent map[string]string) (*Hierarchy, error) {
	h := &Hierarchy{parent: make(map[string]string, len(parent)+1)}
	for class, p := range parent {
		if class == "" {
			return nil, fmt.Errorf("netsec: empty class name")
		}
		h.parent[class] = p
	}
	if _, ok := h.parent[ObjectClass]; !ok {
		h.parent[ObjectClass] = ""
	}
	for class := range h.parent {
		if err := h.checkRooted(class); err != nil {
			return nil, err
		}
	}
	return h, nil
}

func (h *Hierarchy) checkRooted(class string) error {
	seen := map[string]bool{}
	for c := class; ; {
		if c == ObjectClass {
			return nil
		}
		if seen[c] {
			return fmt.Errorf("netsec: class hierarchy cycle at %q", c)
		}
		seen[c] = true
		p, ok := h.parent[c]
		if !ok || p == "" {
			return fmt.Errorf("netsec: class %q does not reach %s", class, ObjectClass)
		}
		c = p
	}
}

// Has reports whether class is known to the hierarchy.
func (h *Hierarchy) Has(class string) bool {
	_, ok := h.parent[class]
	return ok
}

// IsSubclass reports whether sub is sup itself or a descendant of sup.
func (h *Hierarchy) IsSubclass(sub, sup string) bool {
	for c := sub; c != ""; {
		if c == sup {
			return true
		}
		p, ok := h.parent[c]
		if !ok {
			return false
		}
		c = p
	}
	return false
}

// CommonAncestor returns the nearest common ancestor of a and b.
func (h *Hierarchy) CommonAncestor(a, b string) (string, bool) {
	ancestors := map[string]bool{}
	for c := a; c != ""; {
		ancestors[c] = true
		p, ok := h.parent[c]
		if !ok {
			break
		}
		c = p
	}
	for c := b; c != ""; {
		if ancestors[c] {
			return c, true
		}
		p, ok := h.parent[c]
		if !ok {
			break
		}
		c = p
	}
	return "", false
}
