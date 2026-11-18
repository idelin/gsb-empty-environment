package netsec

// Policy is the subset of a Tetragon TracingPolicy needed to validate path
// coverage before the policy is saved or distributed.
type Policy struct {
	Hooks []Hook `json:"hooks"`
}

// Hook describes one tracing hook and the argument selectors attached to it.
type Hook struct {
	Call      string     `json:"call"`
	Args      []Arg      `json:"args,omitempty"`
	Selectors []Selector `json:"selectors,omitempty"`
	Action    Action     `json:"action"`

	// ReturnCode is the value used by an Override action. ArgError is the
	// Tetragon spelling; both are accepted, with ReturnCode taking precedence.
	ReturnCode *int `json:"returnCode,omitempty"`
	ArgError   *int `json:"argError,omitempty"`
}

// Arg declares the type of one tracing hook argument.
type Arg struct {
	Index int    `json:"index"`
	Type  string `json:"type"`
}

// Operator is a Tetragon path selector operator.
type Operator string

const (
	OperatorPrefix  Operator = "Prefix"
	OperatorEqual   Operator = "Equal"
	OperatorPostfix Operator = "Postfix"
)

// Selector applies an operator to one hook argument. Value is a convenience
// form for a single value; Values supports Tetragon-style value lists. When
// both are set, they are treated as one value list.
type Selector struct {
	ArgIndex int      `json:"argIndex"`
	Operator Operator `json:"operator"`
	Value    string   `json:"value,omitempty"`
	Values   []string `json:"values,omitempty"`
}

// ValuesFor returns all selector values after combining Value and Values.
func (s Selector) ValuesFor() []string {
	out := make([]string, 0, len(s.Values)+1)
	if s.Value != "" {
		out = append(out, s.Value)
	}
	out = append(out, s.Values...)
	return out
}

// Action is the action a hook takes when its selector matches.
type Action string

const (
	ActionSigkill  Action = "Sigkill"
	ActionOverride Action = "Override"
)

// Target is a path the organization requires to be protected.
//
// Directory targets are recursive: the reported coverage must protect the
// directory itself and paths below it. Set Directory explicitly, or end Path
// with "/" to request directory semantics.
type Target struct {
	Path      string `json:"path"`
	Access    Access `json:"access"`
	Directory bool   `json:"directory,omitempty"`
}

// FileTarget returns a non-directory target.
func FileTarget(path string, access Access) Target {
	return Target{Path: path, Access: access}
}

// DirectoryTarget returns a recursive directory target.
func DirectoryTarget(path string, access Access) Target {
	return Target{Path: path, Access: access, Directory: true}
}

// FamilyTable describes the same-family calls that must be considered for a
// target. Each path argument lists the operations the call can perform on that
// argument.
type FamilyTable []FamilyCall

// FamilyCall is one call in a same-family table.
type FamilyCall struct {
	Name  string     `json:"name"`
	Paths []PathSpec `json:"paths"`
}

// PathSpec identifies a path-typed argument and its applicable operations.
// DirFDIndex is -1 when the argument is not resolved relative to a dirfd.
type PathSpec struct {
	ArgIndex   int    `json:"argIndex"`
	DirFDIndex int    `json:"dirfdIndex,omitempty"`
	Access     Access `json:"access"`
	Role       string `json:"role,omitempty"`
}

// Severity classifies a validation issue.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Issue is a policy validation error or warning.
type Issue struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	// Hook is the zero-based policy hook index, or -1 for a global issue.
	Hook   int    `json:"hook"`
	Call   string `json:"call,omitempty"`
	Target string `json:"target,omitempty"`
}

// Gap reports a protected target that remains uncovered through one
// same-family call.
type Gap struct {
	Target    string `json:"target"`
	Directory bool   `json:"directory,omitempty"`
	Access    string `json:"access"`
	Call      string `json:"call"`
	PathArgs  []int  `json:"pathArgs,omitempty"`
	Reason    string `json:"reason"`
}

// Report is the result of a coverage validation.
type Report struct {
	// Accepted is false when at least one structural error requires the policy
	// to be rejected (for example, an Override hook without a return code).
	Accepted bool `json:"accepted"`
	// Complete is true when the policy is accepted and every applicable call
	// covers every target.
	Complete bool    `json:"complete"`
	Gaps     []Gap   `json:"gaps"`
	Issues   []Issue `json:"issues"`
}

// Errors returns all error-severity issues.
func (r *Report) Errors() []Issue {
	return r.filter(SeverityError)
}

// Warnings returns all warning-severity issues.
func (r *Report) Warnings() []Issue {
	return r.filter(SeverityWarning)
}

func (r *Report) filter(severity Severity) []Issue {
	if r == nil {
		return nil
	}
	out := make([]Issue, 0)
	for _, issue := range r.Issues {
		if issue.Severity == severity {
			out = append(out, issue)
		}
	}
	return out
}

// OK reports whether the policy may be saved: it must be accepted and cover
// every target through every applicable call.
func (r *Report) OK() bool {
	return r != nil && r.Accepted && r.Complete
}

// ValidationError is returned by Validate when the policy contains structural
// errors and must not be saved.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "policy validation failed"
	}
	first := e.Issues[0]
	if len(e.Issues) == 1 {
		return "policy validation failed: " + first.Message
	}
	return "policy validation failed: " + first.Message + " (and more errors)"
}
