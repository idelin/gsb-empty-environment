package joinenum

import (
	"fmt"
	"sort"
)

// Operator is the immutable optimizer description of one syntax-tree join
// operator.
type Operator struct {
	ID   int
	Kind OpKind
	Pred *Predicate

	// Parent is the operator ID of the parent, or -1 for the root.
	Parent int
	// Side is the position of this operator (or relation subtree) inside its
	// parent.  For the root it is RootSide.
	Side Side

	// LeftRels and RightRels are all base relations below the original left
	// and right children.  LeftSchema and RightSchema are the schemas produced
	// by those children (semi/anti joins drop their right input's schema).
	LeftRels    RelationSet
	RightRels   RelationSet
	LeftSchema  RelationSet
	RightSchema RelationSet

	// Rels is LeftRels ∪ RightRels; Schema is the operator's output schema.
	Rels   RelationSet
	Schema RelationSet

	// TES is the total eligibility set: the minimal set of base relations
	// whose values are observed by this operator's predicate.  Side
	// constraints in the enumerator additionally require right-hand predicate
	// relations to be exposed by the protected right input.
	TES RelationSet

	node *Node
}

// ConflictKind tells which input of the operator rooted at T1 is forbidden for
// the operator/subtree rooted at T2.
type ConflictKind uint8

const (
	// ForbidLeft forbids placing the T2 operator in the left (preserved) input
	// of the T1 operator.
	ForbidLeft ConflictKind = iota
	// ForbidRight forbids placing the T2 operator in the right
	// (null-supplying/existential) input of the T1 operator.
	ForbidRight
)

func (k ConflictKind) String() string {
	if k == ForbidLeft {
		return "left"
	}
	return "right"
}

// ConflictRule is a CD-C style rule T1 -> T2.  T1 and T2 are relation sets of
// the original operator subtrees.  The rule says that the operator originally
// rooted at T2 must not be moved into the named input of the operator
// originally rooted at T1.  Rules are directional: the inverse placement may
// be legal and is represented by a separate rule if it is also forbidden.
type ConflictRule struct {
	ID         int
	T1         RelationSet
	T2         RelationSet
	Kind       ConflictKind
	Descendant int // operator ID rooted at T1
	Ancestor   int // operator ID rooted at T2
	Reason     string
}

// String renders the rule with relation IDs.  Query.RuleString renders the
// same rule with relation names.
func (r ConflictRule) String() string {
	return fmt.Sprintf("%v -> %v (forbid %s input)", r.T1.Elements(), r.T2.Elements(), r.Kind)
}

// Query is a validated, optimizer-ready join query.
type Query struct {
	relations []Relation
	names     []string
	root      *Node
	rootOp    int
	operators []*Operator
	byID      map[int]*Operator
	nodeOp    map[*Node]int

	originalSchema RelationSet
	allRels        RelationSet
	allOps         uint64
	model          CostModel
	graph          [64]RelationSet

	rules          []ConflictRule
	leftForbidden  map[[2]int]bool
	rightForbidden map[[2]int]bool
}

// NewQuery validates relations and root and builds the optimizer metadata
// (TES values, schemas and conflict rules).
func NewQuery(relations []Relation, root *Node) (*Query, error) {
	return NewQueryWithCost(relations, root, nil)
}

// NewQueryWithCost is NewQuery with an explicit cost model.  A nil model means
// DefaultCostModel.
func NewQueryWithCost(relations []Relation, root *Node, model CostModel) (*Query, error) {
	if root == nil {
		return nil, fmt.Errorf("%w: nil syntax tree", ErrInvalidInput)
	}
	if len(relations) == 0 {
		return nil, fmt.Errorf("%w: no base relations", ErrInvalidInput)
	}
	if len(relations) > 62 {
		return nil, fmt.Errorf("%w: at most 62 relations are supported", ErrInvalidInput)
	}
	if model == nil {
		model = DefaultCostModel{}
	}
	q := &Query{
		relations:      append([]Relation(nil), relations...),
		root:           root,
		rootOp:         -1,
		byID:           make(map[int]*Operator),
		nodeOp:         make(map[*Node]int),
		model:          model,
		leftForbidden:  make(map[[2]int]bool),
		rightForbidden: make(map[[2]int]bool),
	}
	seenNames := make(map[string]bool, len(relations))
	for i, rel := range relations {
		if rel.Name == "" {
			return nil, fmt.Errorf("%w: relation %d has no name", ErrInvalidInput, i)
		}
		if seenNames[rel.Name] {
			return nil, fmt.Errorf("%w: duplicate relation name %q", ErrInvalidInput, rel.Name)
		}
		seenNames[rel.Name] = true
		if rel.Cardinality < 0 {
			return nil, fmt.Errorf("%w: relation %q has negative cardinality", ErrInvalidInput, rel.Name)
		}
		q.names = append(q.names, rel.Name)
	}
	used := RelationSet(0)
	var buildErr error
	q.rootOp, _, _, buildErr = q.buildNode(root, &used)
	if buildErr != nil {
		return nil, buildErr
	}
	allRelations := RelationSet(0)
	for i := range relations {
		allRelations = allRelations.Add(i)
	}
	if used != allRelations {
		missing := allRelations.Difference(used)
		return nil, fmt.Errorf("%w: relations %v occur more than once or not at all", ErrInvalidInput, missing.Elements())
	}
	q.allRels = allRelations
	if len(q.operators) != len(relations)-1 {
		return nil, fmt.Errorf("%w: full binary tree needs %d operators, got %d", ErrInvalidInput, len(relations)-1, len(q.operators))
	}
	q.allOps = 0
	for _, op := range q.operators {
		q.allOps |= uint64(1) << uint(op.ID)
		q.byID[op.ID] = op
		refs := op.Pred.refSet()
		for _, r := range refs.Elements() {
			for _, s := range refs.Elements() {
				q.graph[r] = q.graph[r].Add(s)
			}
		}
	}
	if q.rootOp >= 0 {
		q.originalSchema = q.byID[q.rootOp].Schema
	} else {
		q.originalSchema = Of(root.Leaf)
	}
	q.buildConflictRules()
	return q, nil
}

func (q *Query) buildNode(n *Node, used *RelationSet) (int, RelationSet, RelationSet, error) {
	if n == nil {
		return -1, 0, 0, fmt.Errorf("%w: nil child", ErrInvalidInput)
	}
	if n.IsLeaf() {
		if n.Leaf < 0 || n.Leaf >= len(q.relations) {
			return -1, 0, 0, fmt.Errorf("%w: relation index %d out of range", ErrInvalidInput, n.Leaf)
		}
		if used.Contains(n.Leaf) {
			return -1, 0, 0, fmt.Errorf("%w: relation %q occurs more than once", ErrInvalidInput, q.names[n.Leaf])
		}
		*used = used.Add(n.Leaf)
		bit := Of(n.Leaf)
		q.allRels = q.allRels.Add(n.Leaf)
		return -1, bit, bit, nil
	}
	if n.Left == nil || n.Right == nil {
		return -1, 0, 0, fmt.Errorf("%w: internal node needs two children", ErrInvalidInput)
	}
	if !n.Kind.valid() {
		return -1, 0, 0, fmt.Errorf("%w: unknown operator kind %d", ErrInvalidInput, n.Kind)
	}
	if n.Pred == nil {
		return -1, 0, 0, fmt.Errorf("%w: operator without predicate", ErrInvalidInput)
	}
	if err := n.Pred.Selectivity.validate(); err != nil {
		return -1, 0, 0, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	for _, r := range n.Pred.Refs {
		if r < 0 || r >= len(q.relations) {
			return -1, 0, 0, fmt.Errorf("%w: predicate %q references relation %d", ErrInvalidInput, n.Pred.Name, r)
		}
	}
	refs := n.Pred.refSet()
	if refs.IsEmpty() {
		return -1, 0, 0, fmt.Errorf("%w: predicate %q has no referenced relations", ErrInvalidInput, n.Pred.Name)
	}
	leftID, leftRels, leftSchema, err := q.buildNode(n.Left, used)
	if err != nil {
		return -1, 0, 0, err
	}
	rightID, rightRels, rightSchema, err := q.buildNode(n.Right, used)
	if err != nil {
		return -1, 0, 0, err
	}
	if !refs.Subset(leftSchema.Union(rightSchema)) {
		return -1, 0, 0, fmt.Errorf("%w: predicate %q references relations outside its children's schemas", ErrInvalidInput, n.Pred.Name)
	}
	if refs.Intersect(leftSchema).IsEmpty() || refs.Intersect(rightSchema).IsEmpty() {
		return -1, 0, 0, fmt.Errorf("%w: join predicate %q does not connect both children", ErrInvalidInput, n.Pred.Name)
	}
	rels := leftRels.Union(rightRels)
	schema := leftSchema.Union(rightSchema)
	if n.Kind == SemiJoin || n.Kind == AntiJoin {
		schema = leftSchema
	}
	id := len(q.operators)
	op := &Operator{
		ID:          id,
		Kind:        n.Kind,
		Pred:        n.Pred,
		Parent:      -1,
		Side:        RootSide,
		LeftRels:    leftRels,
		RightRels:   rightRels,
		LeftSchema:  leftSchema,
		RightSchema: rightSchema,
		Rels:        rels,
		Schema:      schema,
		TES:         refs,
		node:        n,
	}
	q.operators = append(q.operators, op)
	q.nodeOp[n] = id
	if leftID >= 0 {
		q.operators[leftID].Parent = id
		q.operators[leftID].Side = LeftSide
	}
	if rightID >= 0 {
		q.operators[rightID].Parent = id
		q.operators[rightID].Side = RightSide
	}
	return id, rels, schema, nil
}

// buildConflictRules creates rules for every ancestor/descendant pair whose
// inversion is not covered by commutativity or L-/R-ASSCOM.
func (q *Query) buildConflictRules() {
	next := 0
	for _, a := range q.operators {
		cur := a
		aSide := a.Side
		for cur.Parent >= 0 {
			b := q.byID[cur.Parent]
			if b == nil {
				break
			}
			// Left-input inversion: b is moved below a into a's left input.
			leftAllowed := false
			switch aSide {
			case LeftSide:
				// L-ASSCOM is unconditionally legal when the descendant was
				// originally the ancestor's left child.
				leftAllowed = true
			case RightSide:
				// Mirror L-ASSCOM is legal only for inner/inner or
				// left-outer/left-outer combinations.
				leftAllowed = b.Kind == InnerJoin ||
					(b.Kind == LeftOuterJoin && a.Kind == LeftOuterJoin)
			}
			if !leftAllowed {
				q.addRule(ForbidLeft, a, b, &next)
			}

			// Right-input inversion: b is moved below a into a's protected
			// (null-supplying/existential) input.
			rightAllowed := false
			switch aSide {
			case LeftSide:
				rightAllowed = a.Kind == InnerJoin ||
					(a.Kind == LeftOuterJoin && b.Kind == LeftOuterJoin &&
						predicateNullRejectingOn(b.Pred, a.RightRels))
			case RightSide:
				rightAllowed = a.Kind == InnerJoin && b.Kind == InnerJoin
			}
			if !rightAllowed {
				q.addRule(ForbidRight, a, b, &next)
			}
			aSide = b.Side
			cur = b
		}
	}
}

func (q *Query) addRule(kind ConflictKind, desc, anc *Operator, next *int) {
	rule := ConflictRule{
		ID:         *next,
		T1:         desc.Rels,
		T2:         anc.Rels,
		Kind:       kind,
		Descendant: desc.ID,
		Ancestor:   anc.ID,
		Reason:     fmt.Sprintf("%s (T2) cannot be moved into the %s input of %s (T1)", anc.Kind, kind, desc.Kind),
	}
	(*next)++
	q.rules = append(q.rules, rule)
	key := [2]int{desc.ID, anc.ID}
	if kind == ForbidLeft {
		q.leftForbidden[key] = true
	} else {
		q.rightForbidden[key] = true
	}
}

// predicateNullRejectingOn reports whether p is null-rejecting for every
// referenced relation in rels.  If p references none of rels there is nothing
// that can become null-sensitive, so the answer is true.
func predicateNullRejectingOn(p *Predicate, rels RelationSet) bool {
	if p == nil {
		return false
	}
	if p.refSet().Intersect(rels).IsEmpty() {
		return true
	}
	return !p.NullPreserving
}

// Relations returns a copy of the base-relation descriptors.
func (q *Query) Relations() []Relation {
	return append([]Relation(nil), q.relations...)
}

// Names returns the relation names indexed by relation ID.
func (q *Query) Names() []string { return append([]string(nil), q.names...) }

// Operators returns the original-tree operators ordered by ID.  The returned
// slice must be treated as read-only.
func (q *Query) Operators() []*Operator {
	return append([]*Operator(nil), q.operators...)
}

// Operator returns the operator with id, or nil.
func (q *Query) Operator(id int) *Operator { return q.byID[id] }

// RootOperator returns the original root operator ID.
func (q *Query) RootOperator() int { return q.rootOp }

// Schema returns the output schema of the original syntax tree.
func (q *Query) Schema() RelationSet { return q.originalSchema }

// RelationsSet returns the set of all base relations.
func (q *Query) RelationsSet() RelationSet { return q.allRels }

// CostModel returns the configured cost model.
func (q *Query) CostModel() CostModel { return q.model }

// TES returns the total eligibility set of operator id.
func (q *Query) TES(id int) RelationSet {
	if op, ok := q.byID[id]; ok {
		return op.TES
	}
	return 0
}

// ConflictRules returns a copy of the generated conflict rules.
func (q *Query) ConflictRules() []ConflictRule {
	return append([]ConflictRule(nil), q.rules...)
}

// RuleString renders a rule with relation names.
func (q *Query) RuleString(r ConflictRule) string {
	return fmt.Sprintf("%s -> %s (forbid %s input: %s)",
		r.T1.String(q.names), r.T2.String(q.names), r.Kind, r.Reason)
}

// String renders the original tree in canonical pre-order form.
func (q *Query) String() string {
	if q.root == nil {
		return ""
	}
	return syntaxString(q.root, q.names)
}

func syntaxString(n *Node, names []string) string {
	if n.IsLeaf() {
		return names[n.Leaf]
	}
	return fmt.Sprintf("%c(%s,%s)", n.Kind.Token(), syntaxString(n.Left, names), syntaxString(n.Right, names))
}

// isConnected reports whether the induced predicate graph on s is connected.
func (q *Query) isConnected(s RelationSet) bool {
	if s.Len() <= 1 {
		return true
	}
	first := -1
	for _, r := range s.Elements() {
		first = r
		break
	}
	seen := Of(first)
	stack := []int{first}
	for len(stack) > 0 {
		r := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		next := q.graph[r].Intersect(s).Difference(seen)
		for _, nr := range next.Elements() {
			seen = seen.Add(nr)
			stack = append(stack, nr)
		}
	}
	return seen == s
}

func sortedRuleStrings(q *Query) []string {
	out := make([]string, 0, len(q.rules))
	for _, r := range q.rules {
		out = append(out, q.RuleString(r))
	}
	sort.Strings(out)
	return out
}
