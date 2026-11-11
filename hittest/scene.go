package hittest

// Node describes one surface in the compositor scene graph: a window, a
// control embedded in a window, or a container.
//
// Coordinate spaces are nested: Shape and Clip are expressed in the node's
// local space while Transform maps local points into the parent space.
// Children paint on top of their parent, and siblings later in Children paint
// on top of earlier siblings.
type Node struct {
	ID        string
	Transform Transform
	// Shape is the node's input region in local space. An empty shape means
	// the node itself never receives events (it may still clip and host
	// children, like a plain container).
	Shape Region
	// Clip restricts the node and all descendants. An empty clip means the
	// node does not introduce an additional clip.
	Clip Region
	// Secure marks a security confirmation control. Secure clicks require a
	// press and release that resolve against the same scene version.
	Secure   bool
	Children []*Node
}

// NodeInfo is an immutable view of a node captured in a Snapshot.
type NodeInfo struct {
	ID     string
	Secure bool
}

// Snapshot is an immutable capture of a scene at a single version. All
// hit-testing for one physical gesture must run against the same snapshot so
// that mid-gesture hierarchy changes cannot redirect input.
type Snapshot struct {
	version uint64
	root    *Node
	index   map[string]*Node
}

// Scene is the mutable compositor scene graph. Every mutation advances the
// version, invalidating every earlier snapshot for security purposes.
type Scene struct {
	root    *Node
	version uint64
}

// NewScene creates a scene rooted at root. A nil root is replaced by a
// non-interactive full-space container so that callers may attach top-level
// windows immediately.
func NewScene(root *Node) *Scene {
	if root == nil {
		root = &Node{ID: "root"}
	}
	return &Scene{root: root, version: 1}
}

// Snapshot captures the current scene graph at its current version. The
// snapshot deep-copies the graph, so later mutations cannot affect it;
// taking a snapshot is itself a read and does not advance the version.
func (s *Scene) Snapshot() *Snapshot {
	root := cloneNode(s.root)
	idx := map[string]*Node{}
	indexNodes(root, idx)
	return &Snapshot{version: s.version, root: root, index: idx}
}

// Version returns the current scene version. It advances on every mutating
// Scene method, so two snapshots with the same version are identical.
func (s *Scene) Version() uint64 {
	return s.version
}

// Root replaces the scene root and advances the version.
func (s *Scene) Root(root *Node) {
	if root == nil {
		root = &Node{ID: "root"}
	}
	s.root = root
	s.version++
}

// Find returns the live node with the given ID, or nil when it is absent.
func (s *Scene) Find(id string) *Node {
	return findNode(s.root, id)
}

// AddChild attaches child to parent and advances the version. The hierarchy
// change invalidates all in-flight secure gestures.
func (s *Scene) AddChild(parentID string, child *Node) bool {
	parent := findNode(s.root, parentID)
	if parent == nil || child == nil {
		return false
	}
	parent.Children = append(parent.Children, child)
	s.version++
	return true
}

// RemoveChild detaches the child with childID from parent and advances the
// version. It reports whether the child existed.
func (s *Scene) RemoveChild(parentID, childID string) bool {
	parent := findNode(s.root, parentID)
	if parent == nil {
		return false
	}
	for i, c := range parent.Children {
		if c.ID == childID {
			parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)
			s.version++
			return true
		}
	}
	return false
}

// Raise moves the named sibling above all of its siblings and advances the
// version.
func (s *Scene) Raise(parentID, childID string) bool {
	parent := findNode(s.root, parentID)
	if parent == nil {
		return false
	}
	for i, c := range parent.Children {
		if c.ID == childID {
			parent.Children = append(append(parent.Children[:i], parent.Children[i+1:]...), c)
			s.version++
			return true
		}
	}
	return false
}

// SetTransform replaces a node's local-to-parent transform and advances the
// version.
func (s *Scene) SetTransform(id string, t Transform) bool {
	n := findNode(s.root, id)
	if n == nil {
		return false
	}
	n.Transform = t
	s.version++
	return true
}

// SetShape replaces a node's shape and advances the version.
func (s *Scene) SetShape(id string, shape Region) bool {
	n := findNode(s.root, id)
	if n == nil {
		return false
	}
	n.Shape = shape
	s.version++
	return true
}

// SetClip replaces a node's clip and advances the version.
func (s *Scene) SetClip(id string, clip Region) bool {
	n := findNode(s.root, id)
	if n == nil {
		return false
	}
	n.Clip = clip
	s.version++
	return true
}

// Version reports the scene version captured by this snapshot.
func (snap *Snapshot) Version() uint64 {
	return snap.version
}

// Node returns the immutable descriptor for id, or nil when the node is not
// part of the snapshot.
func (snap *Snapshot) Node(id string) (NodeInfo, bool) {
	n, ok := snap.index[id]
	if !ok {
		return NodeInfo{}, false
	}
	return NodeInfo{ID: n.ID, Secure: n.Secure}, true
}

// Hit is the result of a hit test.
type Hit struct {
	// Node is the topmost surface whose visible region contains the point.
	Node NodeInfo
	// Local is the point expressed in the receiver's local coordinate space.
	Local Point
	// Visible is the receiver's region after every ancestor clip, in screen
	// space.
	Visible Region
}

// HitTest resolves a screen-space point against the immutable snapshot.
// Transparent portions of non-rectangular windows and clipped descendants
// pass input through to whatever lies beneath. The bool is false when no
// node receives the point.
func (snap *Snapshot) HitTest(p Point) (Hit, bool) {
	return snap.hit(snap.root, Identity(), infiniteRegion(), p)
}

// VisibleRegion returns the on-screen region where id is capable of receiving
// input: its transformed shape intersected with every ancestor clip (and its
// own). The region is empty when the node is absent, shapeless, or fully
// clipped.
func (snap *Snapshot) VisibleRegion(id string) Region {
	type frame struct {
		node     *Node
		toScreen Transform
		clip     Region
	}
	stack := []frame{{node: snap.root, toScreen: Identity(), clip: infiniteRegion()}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nodeToScreen := f.toScreen.Then(f.node.Transform)
		clip := applyClip(f.clip, f.node, nodeToScreen)
		if f.node.ID == id {
			if f.node.Shape.IsEmpty() || clip.IsEmpty() {
				return EmptyRegion
			}
			return f.node.Shape.Transform(nodeToScreen).Intersect(clip)
		}
		for i := len(f.node.Children) - 1; i >= 0; i-- {
			stack = append(stack, frame{node: f.node.Children[i], toScreen: nodeToScreen, clip: clip})
		}
	}
	return EmptyRegion
}

// LocalPoint maps a screen-space point into the local space of id through the
// full transform chain. The bool is false when id is absent.
func (snap *Snapshot) LocalPoint(id string, p Point) (Point, bool) {
	chain, ok := snap.transformChain(id)
	if !ok {
		return Point{}, false
	}
	return chain.Invert().Apply(p), true
}

func (snap *Snapshot) hit(n *Node, toScreen Transform, parentClip Region, p Point) (Hit, bool) {
	// toScreen is the accumulated transform of n's ancestors; n's own
	// transform is applied exactly once here and handed to children.
	nodeToScreen := toScreen.Then(n.Transform)
	clip := applyClip(parentClip, n, nodeToScreen)

	// Children and the node's own surface share the same clipped space. Later
	// siblings paint on top, so walk children in reverse stacking order.
	for i := len(n.Children) - 1; i >= 0; i-- {
		if hit, ok := snap.hit(n.Children[i], nodeToScreen, clip, p); ok {
			return hit, true
		}
	}

	if n.Shape.IsEmpty() || !clip.Contains(p) {
		return Hit{}, false
	}
	screenShape := n.Shape.Transform(nodeToScreen)
	if !screenShape.Contains(p) {
		return Hit{}, false
	}
	return Hit{
		Node:    NodeInfo{ID: n.ID, Secure: n.Secure},
		Local:   nodeToScreen.Invert().Apply(p),
		Visible: screenShape.Intersect(clip),
	}, true
}

// applyClip folds the node's own clip into the clip inherited from its
// ancestors. Both are kept in screen space so nested clips compose by plain
// region intersection.
func applyClip(inherited Region, n *Node, toScreen Transform) Region {
	if n.Clip.IsEmpty() {
		return inherited
	}
	if toScreen.IsZero() {
		return inherited.Intersect(n.Clip)
	}
	return inherited.Intersect(n.Clip.Transform(toScreen))
}

func (snap *Snapshot) transformChain(id string) (Transform, bool) {
	type frame struct {
		node     *Node
		toScreen Transform
	}
	stack := []frame{{node: snap.root, toScreen: Identity()}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nodeToScreen := f.toScreen.Then(f.node.Transform)
		if f.node.ID == id {
			return nodeToScreen, true
		}
		for _, c := range f.node.Children {
			stack = append(stack, frame{node: c, toScreen: nodeToScreen})
		}
	}
	return Identity(), false
}

func cloneNode(n *Node) *Node {
	if n == nil {
		return nil
	}
	out := &Node{
		ID:        n.ID,
		Transform: n.Transform,
		Shape:     Region{rects: append([]Rect(nil), n.Shape.rects...)},
		Clip:      Region{rects: append([]Rect(nil), n.Clip.rects...)},
		Secure:    n.Secure,
	}
	for _, c := range n.Children {
		out.Children = append(out.Children, cloneNode(c))
	}
	return out
}

func indexNodes(n *Node, idx map[string]*Node) {
	if n == nil {
		return
	}
	idx[n.ID] = n
	for _, c := range n.Children {
		indexNodes(c, idx)
	}
}

func findNode(n *Node, id string) *Node {
	if n == nil {
		return nil
	}
	if n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if found := findNode(c, id); found != nil {
			return found
		}
	}
	return nil
}

// infiniteRegion is the identity element for clipping. It is represented by
// a region large enough to contain every finite coordinate the compositor
// can produce while keeping all region math ordinary rectangle math.
func infiniteRegion() Region {
	const bound = 1e12
	return RectRegion(R(-bound, -bound, bound, bound))
}
