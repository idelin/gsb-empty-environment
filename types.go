package taskpkg

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ValueKind describes the expanded form of a property value.
const (
	ValueKindLiteral = "literal"
	ValueKindIRI     = "iri"
	ValueKindList    = "list"

	KindLiteral = ValueKindLiteral
	KindIRI     = ValueKindIRI
	KindList    = ValueKindList
)

// Source identifies where a node or value occurred in the input.
//
// Script is the zero-based position of the script block in the caller's input.
// Path is a JSON Pointer rooted at that script block (for example
// "/@graph/0/author/0/name").
type Source struct {
	Script int    `json:"script"`
	Path   string `json:"path"`
}

// Value is an expanded JSON-LD value. Exactly one of IRI, Literal or List is
// populated according to Kind.
//
// IRI values represent node references. A list keeps the order from the
// source document; ordinary multi-valued properties are ordered deterministically
// only after the graph is complete.
type Value struct {
	Kind     string   `json:"kind"`
	IRI      string   `json:"id,omitempty"`
	Literal  any      `json:"literal,omitempty"`
	Type     string   `json:"type,omitempty"`
	Language string   `json:"language,omitempty"`
	List     []Value  `json:"list,omitempty"`
	Sources  []Source `json:"sources,omitempty"`
}

// Key returns a stable identity for the expanded value, excluding provenance.
// Two values with the same key denote the same expanded value.
func (v Value) Key() string {
	switch v.Kind {
	case ValueKindIRI:
		return "iri:" + v.IRI
	case ValueKindList:
		var b strings.Builder
		b.WriteString("list:[")
		for i, item := range v.List {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(item.Key())
		}
		b.WriteByte(']')
		return b.String()
	default:
		return "literal:" + v.Type + ":" + v.Language + ":" + canonicalJSON(v.Literal)
	}
}

// StringValue returns the literal string carried by the value, if it is a
// string literal.
func (v Value) StringValue() (string, bool) {
	if v.Kind != ValueKindLiteral {
		return "", false
	}
	s, ok := v.Literal.(string)
	return s, ok
}

// IsIRI reports whether the value is a node reference.
func (v Value) IsIRI() bool { return v.Kind == ValueKindIRI }

// IsLiteral reports whether the value is a literal.
func (v Value) IsLiteral() bool { return v.Kind == ValueKindLiteral }

// IsList reports whether the value is an ordered JSON-LD list.
func (v Value) IsList() bool { return v.Kind == ValueKindList }

// RefID returns the referenced IRI, or an empty string for non-IRI values.
func (v Value) RefID() string {
	if v.Kind != ValueKindIRI {
		return ""
	}
	return v.IRI
}

// Scalar returns the literal payload, or nil for a reference/list value.
func (v Value) Scalar() any {
	if v.Kind != ValueKindLiteral {
		return nil
	}
	return v.Literal
}

// Conflict records a property that the caller declared single-valued but that
// has more than one distinct expanded value after merging. Both values and
// their original sources are retained; no deterministic value is selected.
type Conflict struct {
	Property string  `json:"property"`
	Values   []Value `json:"values"`
}

// FieldState is the extraction view of a single-valued field.
//
// When Conflict is true, Value is nil and Values contains every conflicting
// value (with provenance). When Present is true and Conflict is false, Value is
// the one deterministic expanded value.
type FieldState struct {
	Property string  `json:"property"`
	Present  bool    `json:"present"`
	Conflict bool    `json:"conflict,omitempty"`
	Value    *Value  `json:"value,omitempty"`
	Values   []Value `json:"values,omitempty"`
}

// Node is a merged JSON-LD subject.
type Node struct {
	ID         string                `json:"id"`
	Types      []string              `json:"types"`
	Properties map[string][]Value    `json:"properties"`
	Sources    []Source              `json:"sources"`
	Conflicts  []Conflict            `json:"conflicts,omitempty"`
	Fields     map[string]FieldState `json:"fields,omitempty"`
}

// Property returns the values of an expanded property IRI.
func (n *Node) Property(iri string) []Value {
	if n == nil {
		return nil
	}
	return n.Properties[iri]
}

// Values is an alias for Property.
func (n *Node) Values(iri string) []Value { return n.Property(iri) }

// ResolvedField returns the deterministic value of a declared single-valued
// property. It returns nil when the property is absent or conflicting.
func (n *Node) ResolvedField(property string) *Value {
	if n == nil {
		return nil
	}
	if f, ok := n.Fields[property]; ok && f.Present && !f.Conflict {
		return f.Value
	}
	return nil
}

// Article is the article view produced by walking author, name and headline.
type Article struct {
	ID            string     `json:"id"`
	Types         []string   `json:"types,omitempty"`
	Headline      string     `json:"headline,omitempty"`
	Name          string     `json:"name,omitempty"`
	HeadlineField FieldState `json:"headlineField"`
	NameField     FieldState `json:"nameField"`
	Authors       []Author   `json:"authors,omitempty"`
	AuthorRefs    []Edge     `json:"authorRefs,omitempty"`
	Sources       []Source   `json:"sources,omitempty"`
}

// Author is a Person view. A missing ID denotes an inline literal author name.
type Author struct {
	ID        string     `json:"id"`
	Types     []string   `json:"types,omitempty"`
	Name      string     `json:"name,omitempty"`
	NameField FieldState `json:"nameField"`
	Sources   []Source   `json:"sources,omitempty"`
}

// Edge status values.
const (
	EdgeResolved   = "resolved"
	EdgeUnresolved = "unresolved"
	EdgeCycle      = EdgeUnresolved
)

// Edge is an author traversal result. Cycles and dangling references are
// marked unresolved so callers can report them without recursively expanding
// the graph forever.
type Edge struct {
	From     string   `json:"from"`
	Property string   `json:"property"`
	To       string   `json:"to,omitempty"`
	Status   string   `json:"status"`
	Reason   string   `json:"reason,omitempty"`
	Sources  []Source `json:"sources,omitempty"`
}

// Resolved reports whether the edge reached a suitable Person node.
func (e Edge) Resolved() bool { return e.Status == EdgeResolved }

// UnresolvedContext records a remote or otherwise unusable @context. The
// processor never performs network access for these entries.
type UnresolvedContext struct {
	Context string `json:"context"`
	Script  int    `json:"script"`
	Path    string `json:"path"`
	Reason  string `json:"reason"`
}

// ScriptInfo describes one input script block. Hash is the SHA-256 digest of
// the canonical JSON tree and is stable when scripts are reordered.
type ScriptInfo struct {
	Script int    `json:"script"`
	Hash   string `json:"hash,omitempty"`
}

// Graph is the merged metadata graph plus the article/author extraction view.
type Graph struct {
	Nodes              map[string]*Node    `json:"nodes"`
	Articles           []Article           `json:"articles"`
	Authors            []Author            `json:"authors"`
	Edges              []Edge              `json:"edges,omitempty"`
	UnresolvedEdges    []Edge              `json:"unresolvedEdges,omitempty"`
	Unresolved         []Edge              `json:"unresolved,omitempty"`
	UnresolvedContexts []UnresolvedContext `json:"unresolvedContexts,omitempty"`
	Scripts            []ScriptInfo        `json:"scripts,omitempty"`
}

func canonicalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(b)
}

func sourceKey(s Source) string {
	return fmt.Sprintf("%d\x00%s", s.Script, s.Path)
}

func mergeSources(dst []Source, src ...Source) []Source {
	seen := make(map[string]struct{}, len(dst)+len(src))
	for _, s := range dst {
		seen[sourceKey(s)] = struct{}{}
	}
	for _, s := range src {
		k := sourceKey(s)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		dst = append(dst, s)
	}
	return dst
}

func sortSources(src []Source) {
	sort.SliceStable(src, func(i, j int) bool {
		if src[i].Script != src[j].Script {
			return src[i].Script < src[j].Script
		}
		return src[i].Path < src[j].Path
	})
}

func sortedTypes(types []string) []string {
	out := append([]string(nil), types...)
	sort.Strings(out)
	return out
}

// Descriptive aliases for hosts that use metadata-oriented type names.
type MetadataGraph = Graph
type ExpandedGraph = Graph
type Metadata = Graph
type Result = Graph
type MetadataNode = Node
type MetadataArticle = Article
type MetadataAuthor = Author
type MetadataValue = Value
type MetadataSource = Source
type ArticleView = Article
type AuthorView = Author

// Script is a raw JSON script block.
type Script = json.RawMessage
type JSONScript = json.RawMessage

// ArticleByID returns one extracted article view.
func (g *Graph) ArticleByID(id string) (Article, bool) {
	if g == nil {
		return Article{}, false
	}
	for _, article := range g.Articles {
		if article.ID == id {
			return article, true
		}
	}
	return Article{}, false
}

// NodeByID returns a merged node.
func (g *Graph) NodeByID(id string) *Node {
	if g == nil {
		return nil
	}
	return g.Nodes[id]
}

// Node is an alias for NodeByID.
func (g *Graph) Node(id string) *Node { return g.NodeByID(id) }

// BlockHash returns the stable content hash of the script block referenced by
// a source. It returns an empty string when the source index is unknown.
func (g *Graph) BlockHash(source Source) string {
	if g == nil || source.Script < 0 {
		return ""
	}
	for _, info := range g.Scripts {
		if info.Script == source.Script {
			return info.Hash
		}
	}
	return ""
}

// AuthorByID returns one extracted Person view.
func (g *Graph) AuthorByID(id string) (Author, bool) {
	if g == nil {
		return Author{}, false
	}
	for _, author := range g.Authors {
		if author.ID == id {
			return author, true
		}
	}
	return Author{}, false
}
