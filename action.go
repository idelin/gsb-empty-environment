package netsec

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ActionKind is one of the five action forms understood by the host.
type ActionKind string

const (
	KindRead    ActionKind = "read"
	KindWrite   ActionKind = "write"
	KindExec    ActionKind = "exec"
	KindConnect ActionKind = "connect"
	KindEnv     ActionKind = "env"
)

// Action is a declared or attempted capability. Only the fields relevant to
// Kind may be set.
type Action struct {
	Kind       ActionKind `json:"kind"`
	Path       string     `json:"path,omitempty"`
	ToolID     string     `json:"toolID,omitempty"`
	EndpointID string     `json:"endpointID,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// Read declares a source-root read.
func Read(path string) Action {
	return Action{Kind: KindRead, Path: path}
}

// Write declares an output-root write.
func Write(path string) Action {
	return Action{Kind: KindWrite, Path: path}
}

// Exec declares use of an exact host-registered tool ID.
func Exec(toolID string) Action {
	return Action{Kind: KindExec, ToolID: toolID}
}

// Connect declares use of an exact host-registered endpoint ID.
func Connect(endpointID string) Action {
	return Action{Kind: KindConnect, EndpointID: endpointID}
}

// Env declares access to an environment variable by exact name.
func Env(name string) Action {
	return Action{Kind: KindEnv, Name: name}
}

// Validate checks that the action has a known kind, exactly the selector for
// that kind, and a lexically safe path when applicable.
func (a Action) Validate() error {
	switch a.Kind {
	case KindRead, KindWrite:
		if a.ToolID != "" || a.EndpointID != "" || a.Name != "" {
			return fmt.Errorf("netsec: %s action has an unrelated selector", a.Kind)
		}
		return ValidatePackagePath(a.Path)
	case KindExec:
		if a.Path != "" || a.EndpointID != "" || a.Name != "" {
			return fmt.Errorf("netsec: exec action has an unrelated selector")
		}
		return validateOpaqueID("tool", a.ToolID)
	case KindConnect:
		if a.Path != "" || a.ToolID != "" || a.Name != "" {
			return fmt.Errorf("netsec: connect action has an unrelated selector")
		}
		return validateOpaqueID("endpoint", a.EndpointID)
	case KindEnv:
		if a.Path != "" || a.ToolID != "" || a.EndpointID != "" {
			return fmt.Errorf("netsec: env action has an unrelated selector")
		}
		return ValidateEnvName(a.Name)
	default:
		return fmt.Errorf("netsec: unknown action kind %q", a.Kind)
	}
}

func (a Action) key() string {
	switch a.Kind {
	case KindRead, KindWrite:
		return string(a.Kind) + "\x1f" + a.Path
	case KindExec:
		return string(a.Kind) + "\x1f" + a.ToolID
	case KindConnect:
		return string(a.Kind) + "\x1f" + a.EndpointID
	case KindEnv:
		return string(a.Kind) + "\x1f" + a.Name
	default:
		return string(a.Kind)
	}
}

func (a Action) selector() string {
	switch a.Kind {
	case KindRead, KindWrite:
		return a.Path
	case KindExec:
		return a.ToolID
	case KindConnect:
		return a.EndpointID
	case KindEnv:
		return a.Name
	default:
		return ""
	}
}

func (a Action) String() string {
	switch a.Kind {
	case KindRead:
		return fmt.Sprintf("Read(%q)", a.Path)
	case KindWrite:
		return fmt.Sprintf("Write(%q)", a.Path)
	case KindExec:
		return fmt.Sprintf("Exec(%q)", a.ToolID)
	case KindConnect:
		return fmt.Sprintf("Connect(%q)", a.EndpointID)
	case KindEnv:
		return fmt.Sprintf("Env(%q)", a.Name)
	default:
		return fmt.Sprintf("Action(%q)", string(a.Kind))
	}
}

// ActionSet is an immutable-by-convention set of exact actions.
type ActionSet struct {
	actions map[string]Action
}

// NewActionSet builds a set from actions. Duplicate actions collapse.
func NewActionSet(actions ...Action) ActionSet {
	s := ActionSet{actions: make(map[string]Action, len(actions))}
	for _, action := range actions {
		s.actions[action.key()] = action
	}
	return s
}

// Has reports exact membership. No tool, endpoint, environment, or path
// aliasing or normalization is performed.
func (s ActionSet) Has(action Action) bool {
	if s.actions == nil {
		return false
	}
	_, ok := s.actions[action.key()]
	return ok
}

// Len returns the number of actions.
func (s ActionSet) Len() int { return len(s.actions) }

// IsEmpty reports whether the set has no actions.
func (s ActionSet) IsEmpty() bool { return len(s.actions) == 0 }

// List returns actions in stable selector order.
func (s ActionSet) List() []Action {
	out := make([]Action, 0, len(s.actions))
	for _, action := range s.actions {
		out = append(out, action)
	}
	SortActions(out)
	return out
}

// Intersect returns actions present in both sets.
func (s ActionSet) Intersect(other ActionSet) ActionSet {
	result := ActionSet{actions: make(map[string]Action)}
	for key, action := range s.actions {
		if other.Has(action) {
			result.actions[key] = action
		}
	}
	return result
}

// Union returns actions present in either set.
func (s ActionSet) Union(other ActionSet) ActionSet {
	result := ActionSet{actions: make(map[string]Action, len(s.actions)+len(other.actions))}
	for key, action := range s.actions {
		result.actions[key] = action
	}
	for key, action := range other.actions {
		result.actions[key] = action
	}
	return result
}

// Without returns actions from s that are not in other.
func (s ActionSet) Without(other ActionSet) ActionSet {
	result := ActionSet{actions: make(map[string]Action)}
	for key, action := range s.actions {
		if !other.Has(action) {
			result.actions[key] = action
		}
	}
	return result
}

// MarshalJSON emits a stable action array rather than an implementation map.
func (s ActionSet) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.List())
}

// UnmarshalJSON accepts an action array.
func (s *ActionSet) UnmarshalJSON(data []byte) error {
	var actions []Action
	if err := json.Unmarshal(data, &actions); err != nil {
		return err
	}
	*s = NewActionSet(actions...)
	return nil
}

// SortActions orders actions by kind, then selector.
func SortActions(actions []Action) {
	sort.Slice(actions, func(i, j int) bool {
		if actions[i].Kind != actions[j].Kind {
			return actions[i].Kind < actions[j].Kind
		}
		if actions[i].selector() != actions[j].selector() {
			return actions[i].selector() < actions[j].selector()
		}
		return actions[i].key() < actions[j].key()
	})
}

func canonicalActions(actions []Action) []Action {
	set := NewActionSet(actions...)
	return set.List()
}
