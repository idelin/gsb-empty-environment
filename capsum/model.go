// Package capsum computes importable permission summaries for a Go host that
// executes Java-style plugin tasks with captured protection domains.
//
// The analysis is a whole-program abstract interpretation over a finite domain
// of path-component DFA generators. Recursion has no artificial stack bound:
// summaries are solved to a monotone fixpoint. Asynchronous posts freeze the
// effective domains at the post site; consumption intersects the frozen
// restriction with the consumer's current stack, each handle is consumed at
// most once, and tasks spawned inside a task cannot escape the parent task's
// frozen restriction.
package capsum

// GrantKind selects one of the three resource shapes modeled for every grant.
type GrantKind string

const (
	GrantSelf  GrantKind = "self"
	GrantChild GrantKind = "child"
	GrantDesc  GrantKind = "desc"
)

// Root is a physical root or a tenant-root alias bound to a version.
type Root struct {
	Name    string `json:"name"`
	AliasOf string `json:"alias_of,omitempty"`
	Version string `json:"version"`
}

// Grant is a single domain authorization: one shape over one normalized path.
type Grant struct {
	Root       string    `json:"root"`
	Components []string  `json:"components"`
	Kind       GrantKind `json:"kind"`
}

// Domain is a named protection domain whose grants are unioned into one path
// language when the domain frames a stack frame.
type Domain struct {
	Name   string  `json:"name"`
	Grants []Grant `json:"grants"`
}

// EdgeKind enumerates the call-graph edge types.
type EdgeKind string

const (
	EdgeLocal      EdgeKind = "local"
	EdgeCall       EdgeKind = "call"
	EdgePriv       EdgeKind = "priv"
	EdgePost       EdgeKind = "post"
	EdgeConsume    EdgeKind = "consume"
	EdgeReturn     EdgeKind = "return"
	EdgeTaskReturn EdgeKind = "task_return"
)

// Edge is a directed call-graph edge. Node identifiers are unique program-wide.
type Edge struct {
	ID   string   `json:"id"`
	From string   `json:"from"`
	To   string   `json:"to"`
	Kind EdgeKind `json:"kind"`

	// Domains frame synchronous and privileged callee frames.
	Domains []string `json:"domains,omitempty"`

	// Handle identifies the asynchronous queue slot used by post/consume.
	Handle string `json:"handle,omitempty"`

	// PairID matches a call/priv edge with its return edge.
	PairID string `json:"pair_id,omitempty"`

	// ConsumePairID matches a consume edge with its task_return edge.
	ConsumePairID string `json:"consume_pair_id,omitempty"`

	// Action optionally labels a file operation at the source node.
	Action *FileAction `json:"action,omitempty"`
}

// FileAction is a requested file operation at a program point.
type FileAction struct {
	Name string   `json:"name"`
	Root string   `json:"root"`
	Path []string `json:"path"`
}

// Program is the fully resolved analysis input.
type Program struct {
	Name         string   `json:"name"`
	Roots        []Root   `json:"roots"`
	Domains      []Domain `json:"domains"`
	EntryNode    string   `json:"entry_node"`
	EntryDomains []string `json:"entry_domains"`
	Nodes        []string `json:"nodes"`
	Edges        []Edge   `json:"edges"`
	MaxHandles   int      `json:"max_handles"`
}
