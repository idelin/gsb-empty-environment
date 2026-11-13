package netsec

// Action is the result of an NACM rule or an applicable default policy.
type Action uint8

const (
	// Deny rejects the requested operation. It is the zero value so an
	// unconfigured or partially initialised policy fails closed.
	Deny Action = iota
	// Permit allows the requested operation.
	Permit
)

// Allow is an alias for Permit.
const Allow = Permit

// Decision is an alias for Action.
type Decision = Action

func (a Action) String() string {
	switch a {
	case Permit:
		return "permit"
	case Deny:
		return "deny"
	default:
		return "deny"
	}
}

// Operation identifies one NACM access operation.
type Operation uint8

const (
	// Read covers datastore reads and notification events.
	Read Operation = iota
	// Create covers adding a data node instance.
	Create
	// Update covers changing an existing data node instance.
	Update
	// Delete covers removing a data node instance.
	Delete
	// Exec covers invoking a protocol operation.
	Exec
)

func (o Operation) String() string {
	switch o {
	case Read:
		return "read"
	case Create:
		return "create"
	case Update:
		return "update"
	case Delete:
		return "delete"
	case Exec:
		return "exec"
	default:
		return "unknown"
	}
}

func (o Operation) valid() bool {
	return o <= Exec
}

// Bit returns the bit representing the operation in an Operations set.
func (o Operation) Bit() Operations {
	if !o.valid() {
		return 0
	}
	return Operations(1) << o
}

// Operations is a set of access operations. Its zero value means all
// operations, matching the default of the NACM "access-operations" leaf.
type Operations uint8

const (
	OpRead   Operations = 1 << Read
	OpCreate Operations = 1 << Create
	OpUpdate Operations = 1 << Update
	OpDelete Operations = 1 << Delete
	OpExec   Operations = 1 << Exec

	// OpAll contains every NACM access operation.
	OpAll = OpRead | OpCreate | OpUpdate | OpDelete | OpExec
)

// Has reports whether the set contains op. A zero-value set contains all
// operations because NACM treats an omitted access-operations leaf as
// "all".
func (s Operations) Has(op Operation) bool {
	bit := op.Bit()
	if bit == 0 {
		return false
	}
	if s == 0 {
		return true
	}
	return s&bit != 0
}

// Rule is one ordered NACM rule. Path covers the node at that path and its
// entire subtree. An empty Path is the root and therefore covers every node.
// An empty Operations set applies to all operations.
//
// The zero value is a deny-all rule for the root, which fails closed.
type Rule struct {
	Path       string
	Operations Operations
	Action     Action
	Comment    string
}

// Matches reports whether the rule applies to the given path and operation.
func (r Rule) Matches(path string, op Operation) bool {
	return r.Operations.Has(op) && rulePathCovers(r.Path, path)
}

// RuleList is an ordered collection of rules that applies to users in any of
// the listed groups. Rule lists are evaluated in slice order. Empty group
// names are ignored.
type RuleList struct {
	Name   string
	Groups []string
	Rules  []Rule
}

// AppliesTo reports whether the rule list belongs to at least one of the
// supplied user groups.
func (rl RuleList) AppliesTo(groups []string) bool {
	for _, member := range groups {
		if member == "" {
			continue
		}
		for _, group := range rl.Groups {
			if group == member {
				return true
			}
		}
	}
	return false
}

// Policy is an ordered NACM rule-list policy plus the three RFC 8341
// defaults. Create, update and delete are write operations and therefore use
// WriteDefault when no rule matches.
//
// Defaults are deny when left at their zero value.
type Policy struct {
	RuleLists    []RuleList
	ReadDefault  Action
	WriteDefault Action
	ExecDefault  Action
}

// Node describes the target of an authorization check. DefaultDenyAll
// disables all default permits for the node. DefaultDenyWrite disables the
// write default for create, update and delete.
type Node struct {
	Path             string
	DefaultDenyAll   bool
	DefaultDenyWrite bool
}

// Request is a complete authorization request. DefaultDenyAll and
// DefaultDenyWrite carry the YANG security extensions of the target node.
type Request struct {
	Groups           []string
	Path             string
	Operation        Operation
	DefaultDenyAll   bool
	DefaultDenyWrite bool
}

// RuleListsFor returns the rule lists that apply to the supplied groups,
// preserving policy order. The returned slice is a shallow copy and callers
// cannot reorder the policy's rule lists through it.
func (p Policy) RuleListsFor(groups []string) []RuleList {
	applicable := make([]RuleList, 0, len(p.RuleLists))
	for _, list := range p.RuleLists {
		if list.AppliesTo(groups) {
			applicable = append(applicable, list)
		}
	}
	return applicable
}

// Check evaluates an authorization request. It returns the action of the
// first matching rule in the first applicable rule list, or the applicable
// default when no rule matches.
//
// The defaultDenyAll argument corresponds to the YANG nacm:default-deny-all
// extension on the target node.
func (p Policy) Check(groups []string, path string, op Operation, defaultDenyAll bool) Action {
	return p.Decide(Request{
		Groups:         groups,
		Path:           path,
		Operation:      op,
		DefaultDenyAll: defaultDenyAll,
	})
}

// Authorize is an alias for Check.
func (p Policy) Authorize(groups []string, path string, op Operation, defaultDenyAll bool) Action {
	return p.Check(groups, path, op, defaultDenyAll)
}

// CheckNode evaluates an authorization request for a Node.
func (p Policy) CheckNode(groups []string, node Node, op Operation) Action {
	return p.Decide(Request{
		Groups:           groups,
		Path:             node.Path,
		Operation:        op,
		DefaultDenyAll:   node.DefaultDenyAll,
		DefaultDenyWrite: node.DefaultDenyWrite,
	})
}

// Allows is the boolean form of Check.
func (p Policy) Allows(groups []string, path string, op Operation, defaultDenyAll bool) bool {
	return p.Check(groups, path, op, defaultDenyAll) == Permit
}

// AllowsNode is the boolean form of CheckNode.
func (p Policy) AllowsNode(groups []string, node Node, op Operation) bool {
	return p.CheckNode(groups, node, op) == Permit
}

// Decide evaluates a complete authorization request. First applicable rule
// list in policy order wins, then first matching rule in that list wins.
// Rules that do not match the operation or path do not affect the result.
func (p Policy) Decide(req Request) Action {
	if !req.Operation.valid() {
		return Deny
	}
	if _, ok := parsePath(req.Path); !ok {
		return Deny
	}

	for _, list := range p.RuleLists {
		if !list.AppliesTo(req.Groups) {
			continue
		}
		for _, rule := range list.Rules {
			if !rule.Matches(req.Path, req.Operation) {
				continue
			}
			if rule.Action == Permit {
				return Permit
			}
			return Deny
		}
	}

	return p.defaultAction(req)
}

func (p Policy) defaultAction(req Request) Action {
	if req.DefaultDenyAll {
		return Deny
	}

	switch req.Operation {
	case Read:
		return permitOnly(p.ReadDefault)
	case Create, Update, Delete:
		if req.DefaultDenyWrite {
			return Deny
		}
		return permitOnly(p.WriteDefault)
	case Exec:
		return permitOnly(p.ExecDefault)
	default:
		return Deny
	}
}

func permitOnly(action Action) Action {
	if action == Permit {
		return Permit
	}
	return Deny
}

// Check evaluates a policy using the package-level form.
func Check(p Policy, groups []string, path string, op Operation, defaultDenyAll bool) Action {
	return p.Check(groups, path, op, defaultDenyAll)
}

// Allows evaluates a policy using the package-level boolean form.
func Allows(p Policy, groups []string, path string, op Operation, defaultDenyAll bool) bool {
	return p.Allows(groups, path, op, defaultDenyAll)
}
