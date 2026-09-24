package enumkit

import "fmt"

// ChainStep is one hop in a conflict chain: two stable IDs linked by a
// shared name in the merged alias graph.
type ChainStep struct {
	FromID      int64
	Name        string
	ToID        int64
	FromVersion string
	ToVersion   string
	FromActive  bool
	ToActive    bool
}

func role(active bool) string {
	if active {
		return "active"
	}
	return "alias"
}

// String renders one hop, e.g. #1 --"LIGHT"(v2:alias, v3:alias)--> #2.
func (s ChainStep) String() string {
	return fmt.Sprintf("#%d --%q(%s:%s, %s:%s)--> #%d",
		s.FromID, s.Name, s.FromVersion, role(s.FromActive),
		s.ToVersion, role(s.ToActive), s.ToID)
}

// RenderChain renders a whole chain of hops.
func RenderChain(steps []ChainStep) string {
	out := ""
	for i, s := range steps {
		if i > 0 {
			out += "  "
		}
		out += s.String()
	}
	return out
}

// ConflictError reports an illegal mapping inside a single version:
// one name bound to two different stable IDs.
type ConflictError struct {
	Enum    string
	Version string
	Name    string
	IDs     []int64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("enum %s: version %q: name %q illegally maps to two stable IDs %v",
		e.Enum, e.Version, e.Name, e.IDs)
}

// MergeConflictError reports a hard conflict found while merging
// version graphs: two different stable IDs share one active name.
type MergeConflictError struct {
	Enum  string
	Name  string
	IDs   []int64
	Chain []ChainStep
}

func (e *MergeConflictError) Error() string {
	return fmt.Sprintf("enum %s: active name %q is shared by different stable IDs %v; shortest conflict chain: %s",
		e.Enum, e.Name, e.IDs, RenderChain(e.Chain))
}

// AmbiguityError is returned by migration when a token maps to more
// than one stable ID in the merged graph. Migration never picks one
// arbitrarily; it reports the candidates and the shortest chain.
type AmbiguityError struct {
	Enum  string
	Token string
	IDs   []int64
	Chain []ChainStep
}

func (e *AmbiguityError) Error() string {
	return fmt.Sprintf("enum %s: token %q is ambiguous between stable IDs %v; shortest conflict chain: %s",
		e.Enum, e.Token, e.IDs, RenderChain(e.Chain))
}

// PathError attaches the original field path of a document to an error.
type PathError struct {
	Path string
	Err  error
}

func (e *PathError) Error() string { return e.Path + ": " + e.Err.Error() }
func (e *PathError) Unwrap() error { return e.Err }

// VerifyError reports one inconsistent migration path found by Verify.
type VerifyError struct {
	Enum  string
	From  string
	To    string
	Token string
	Err   error
}

func (e *VerifyError) Error() string {
	return fmt.Sprintf("enum %s: migration path %s->%s token %q: %v", e.Enum, e.From, e.To, e.Token, e.Err)
}
func (e *VerifyError) Unwrap() error { return e.Err }
