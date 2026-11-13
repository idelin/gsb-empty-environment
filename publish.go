package netsec

import (
	"fmt"
	"strconv"
)

// FunctionDefinition is the input to publication. SearchPath contains stable
// schema IDs only. AST is frozen before the definition becomes callable.
type FunctionDefinition struct {
	ID             FunctionID
	ObjectID       ObjectID
	Name           string
	SchemaID       SchemaID
	DefinerID      PrincipalID
	SearchPath     []SchemaID
	AST            AST
	AllowedCallers map[PrincipalID]bool
	Public         bool
	Version        int64
	Revoked        bool
}

// PublishedFunction is an immutable function definition plus its frozen
// dependencies. The exported view is diagnostic; execution uses private,
// cloned copies so later caller mutation cannot alter resolution.
type PublishedFunction struct {
	ID           FunctionID
	Definition   FunctionDefinition
	SearchPath   []SchemaID
	Dependencies []Dependency
	DynamicSites []DynamicSite

	frozen      map[string]FrozenRef
	dynamic     map[string]DynamicSite
	allowed     map[PrincipalID]bool
	definer     PrincipalID
	searchPath  []SchemaID
	deps        []Dependency
	dynamicList []DynamicSite
	body        AST
	revoked     bool
	public      bool
	functionObj ObjectID
	functionVer int64
	functionRef FrozenRef
}

// Publish freezes a SECURITY DEFINER definition against c.
func Publish(c *Catalog, def FunctionDefinition) (*PublishedFunction, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: nil catalog", ErrInvalidDefinition)
	}
	return c.Publish(def)
}

// Publish is the catalog-bound form of Publish.
func (c *Catalog) Publish(def FunctionDefinition) (*PublishedFunction, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: nil catalog", ErrInvalidDefinition)
	}
	if def.DefinerID == "" {
		return nil, fmt.Errorf("%w: empty definer ID", ErrInvalidDefinition)
	}
	if def.ID == "" {
		if def.Name == "" {
			return nil, fmt.Errorf("%w: function needs ID or name", ErrInvalidDefinition)
		}
		def.ID = FunctionID(string(def.SchemaID) + "." + def.Name)
	}

	path, err := c.CompileSearchPath(def.SearchPath, def.DefinerID)
	if err != nil {
		return nil, err
	}

	pub := &PublishedFunction{
		ID:         def.ID,
		Definition: cloneDefinition(def),
		SearchPath: append([]SchemaID(nil), path...),
		frozen:     make(map[string]FrozenRef),
		dynamic:    make(map[string]DynamicSite),
		allowed:    cloneAllowed(def.AllowedCallers),
		definer:    def.DefinerID,
		searchPath: append([]SchemaID(nil), path...),
		body:       cloneAST(def.AST),
		revoked:    def.Revoked,
		public:     def.Public || def.AllowedCallers[PublicPrincipal],
	}

	if def.ObjectID != "" {
		obj, ok := c.Object(def.ObjectID)
		if !ok {
			return nil, fmt.Errorf("%w: function object %q does not exist", ErrInvalidDefinition, def.ObjectID)
		}
		if obj.Kind != KindFunction {
			return nil, fmt.Errorf("%w: object %q is %s, not a function", ErrInvalidDefinition, def.ObjectID, obj.Kind)
		}
		pub.functionObj = obj.ID
		pub.functionVer = obj.Version
		pub.functionRef = FrozenRef{
			Kind:      obj.Kind,
			ObjectID:  obj.ID,
			Version:   obj.Version,
			SchemaID:  obj.SchemaID,
			Name:      obj.Name,
			Signature: obj.SignatureKey(),
		}
	}

	if err := c.freezeAST(pub, def.AST, "body"); err != nil {
		return nil, err
	}
	if pub.frozen == nil {
		pub.frozen = make(map[string]FrozenRef)
	}
	if pub.dynamic == nil {
		pub.dynamic = make(map[string]DynamicSite)
	}
	pub.deps = append([]Dependency(nil), pub.Dependencies...)
	for i := range pub.deps {
		pub.deps[i].Candidates = append([]ResolutionCandidate(nil), pub.Dependencies[i].Candidates...)
	}
	pub.dynamicList = append([]DynamicSite(nil), pub.DynamicSites...)
	return pub, nil
}

func (c *Catalog) freezeAST(pub *PublishedFunction, nodes AST, prefix string) error {
	for i, node := range nodes {
		path := prefix + "[" + strconv.Itoa(i) + "]"
		if err := c.freezeNode(pub, node, path); err != nil {
			return err
		}
	}
	return nil
}

func (c *Catalog) freezeNode(pub *PublishedFunction, node Node, path string) error {
	if node == nil {
		return fmt.Errorf("%w: nil AST node at %s", ErrInvalidDefinition, path)
	}
	switch n := node.(type) {
	case Ref:
		return c.freezeRef(pub, n, path)
	case *Ref:
		if n == nil {
			return fmt.Errorf("%w: nil AST node at %s", ErrInvalidDefinition, path)
		}
		return c.freezeRef(pub, *n, path)
	case Seq:
		return c.freezeAST(pub, n.Nodes, path)
	case *Seq:
		if n == nil {
			return fmt.Errorf("%w: nil AST node at %s", ErrInvalidDefinition, path)
		}
		return c.freezeAST(pub, n.Nodes, path)
	case Const, *Const, Param, *Param:
		// Caller-supplied parameter values were evaluated before this body
		// starts; they contain no catalog references to freeze.
		return nil
	case DynamicSQL:
		return c.recordDynamic(pub, n.SQL, n.Key, path)
	case *DynamicSQL:
		if n == nil {
			return fmt.Errorf("%w: nil AST node at %s", ErrInvalidDefinition, path)
		}
		return c.recordDynamic(pub, n.SQL, n.Key, path)
	default:
		return fmt.Errorf("%w: unsupported AST node %T at %s", ErrInvalidDefinition, node, path)
	}
}

func (c *Catalog) freezeRef(pub *PublishedFunction, n Ref, path string) error {
	resolved, err := c.resolveReference(pub.SearchPath, n, path)
	if err != nil {
		return err
	}
	o := resolved.object
	frozen := FrozenRef{
		Path:        path,
		Kind:        o.Kind,
		ObjectID:    o.ID,
		Version:     o.Version,
		SchemaID:    o.SchemaID,
		Name:        o.Name,
		Signature:   o.SignatureKey(),
		SearchIndex: resolved.searchIdx,
		Qualified:   resolved.qualified,
	}
	if _, exists := pub.frozen[path]; exists {
		return fmt.Errorf("%w: duplicate AST path %s", ErrInvalidDefinition, path)
	}
	pub.frozen[path] = frozen
	pub.Dependencies = append(pub.Dependencies, Dependency{
		Ref:        frozen,
		Candidates: append([]ResolutionCandidate(nil), resolved.candidates...),
	})
	return c.freezeAST(pub, n.Args, path+".args")
}

func (c *Catalog) recordDynamic(pub *PublishedFunction, sql, key, path string) error {
	if key == "" {
		key = sql
	}
	site := DynamicSite{Path: path, SQL: sql, Key: key}
	pub.dynamic[key] = site
	pub.DynamicSites = append(pub.DynamicSites, site)
	return nil
}

// Manifest returns a defensive copy of the dependency manifest.
func (p *PublishedFunction) Manifest() DependencyManifest {
	if p == nil {
		return DependencyManifest{}
	}
	m := DependencyManifest{
		FunctionID:   p.ID,
		DefinerID:    p.definer,
		SearchPath:   append([]SchemaID(nil), p.searchPath...),
		Dependencies: make([]Dependency, 0, len(p.deps)),
		DynamicSites: append([]DynamicSite(nil), p.dynamicList...),
	}
	for _, dep := range p.deps {
		m.Dependencies = append(m.Dependencies, Dependency{
			Ref:        dep.Ref,
			Candidates: append([]ResolutionCandidate(nil), dep.Candidates...),
		})
	}
	return m
}

// FrozenDependency returns the frozen reference at a specific AST path.
func (p *PublishedFunction) FrozenDependency(path string) (FrozenRef, bool) {
	if p == nil {
		return FrozenRef{}, false
	}
	ref, ok := p.frozen[path]
	return ref, ok
}

func (p *PublishedFunction) allowsCaller(caller PrincipalID) bool {
	if caller == "" {
		return false
	}
	if caller == p.definer {
		return true
	}
	if p.public || p.allowed[PublicPrincipal] {
		return true
	}
	return p.allowed[caller]
}

func cloneDefinition(def FunctionDefinition) FunctionDefinition {
	def.SearchPath = append([]SchemaID(nil), def.SearchPath...)
	def.AST = cloneAST(def.AST)
	def.AllowedCallers = cloneAllowed(def.AllowedCallers)
	return def
}

func cloneAllowed(in map[PrincipalID]bool) map[PrincipalID]bool {
	out := make(map[PrincipalID]bool, len(in))
	for p, allowed := range in {
		if allowed {
			out[p] = true
		}
	}
	return out
}

func cloneAST(in AST) AST {
	if in == nil {
		return nil
	}
	out := make(AST, len(in))
	for i, node := range in {
		out[i] = cloneNode(node)
	}
	return out
}

func cloneNode(node Node) Node {
	switch n := node.(type) {
	case Ref:
		n.ArgTypes = append([]string(nil), n.ArgTypes...)
		n.Args = cloneAST(n.Args)
		return n
	case *Ref:
		if n == nil {
			return n
		}
		copied := *n
		copied.ArgTypes = append([]string(nil), copied.ArgTypes...)
		copied.Args = cloneAST(copied.Args)
		return &copied
	case Seq:
		return Seq{Nodes: cloneAST(n.Nodes)}
	case *Seq:
		if n == nil {
			return n
		}
		return &Seq{Nodes: cloneAST(n.Nodes)}
	case Const:
		return n
	case *Const:
		if n == nil {
			return n
		}
		copied := *n
		return &copied
	case Param:
		return n
	case *Param:
		if n == nil {
			return n
		}
		copied := *n
		return &copied
	case DynamicSQL:
		return n
	case *DynamicSQL:
		if n == nil {
			return n
		}
		copied := *n
		return &copied
	default:
		return node
	}
}
