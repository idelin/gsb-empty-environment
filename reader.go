package netsec

import (
	"errors"
	"fmt"
)

// Options configures a Reader.
type Options struct {
	// Version binds the reader to one file version; references carrying
	// any other version are rejected with VersionError.
	Version uint32
	// AllowRef is the caller-declared recursion whitelist: an edge
	// from -> to is followed only when AllowRef(from, to) is true.
	// A nil AllowRef permits every edge.
	AllowRef func(from, to TableType) bool
	// MaxTables caps the number of distinct tables parsed (0 = unlimited).
	MaxTables int
	// MaxBytes caps the total declared table bytes read (0 = unlimited).
	MaxBytes int64
}

// EdgeWhitelist returns an AllowRef func permitting exactly the listed
// from -> to type edges.
func EdgeWhitelist(edges ...[2]TableType) func(from, to TableType) bool {
	allowed := make(map[[2]TableType]struct{}, len(edges))
	for _, e := range edges {
		allowed[e] = struct{}{}
	}
	return func(from, to TableType) bool {
		_, ok := allowed[[2]TableType{from, to}]
		return ok
	}
}

// Reader expands a table graph from a versioned metadata image. It
// validates each table's local bounds before following its references,
// parses legitimately shared tables only once, and reports reference
// cycles on the current expansion stack with their shortest evidence.
type Reader struct {
	src   Source
	parse Parser
	opts  Options

	nodes   map[TableID]*Node // fully resolved tables (memo)
	onStack map[TableID]int   // tables on the expansion stack -> stack index
	stack   []TableID
	count   int   // distinct tables admitted (incl. in-progress)
	bytes   int64 // declared bytes read so far
}

// NewReader binds a Reader to src at the file version given in opts.
func NewReader(src Source, parse Parser, opts Options) *Reader {
	return &Reader{
		src:     src,
		parse:   parse,
		opts:    opts,
		nodes:   make(map[TableID]*Node),
		onStack: make(map[TableID]int),
	}
}

// Read expands the graph rooted at root. The memo is shared across
// calls, so tables already resolved by a previous Read are reused.
func (r *Reader) Read(root TableID) (*Node, error) {
	if len(r.stack) != 0 {
		return nil, errors.New("netsec: Read called while a previous expansion is unfinished")
	}
	return r.resolve(root)
}

func (r *Reader) resolve(id TableID) (*Node, error) {
	if id.Version != r.opts.Version {
		return nil, &VersionError{ID: id, Want: r.opts.Version}
	}
	if idx, ok := r.onStack[id]; ok {
		// id is already being expanded: the stack segment from its
		// first occurrence back to id is the shortest cycle evidence.
		path := make([]TableID, 0, len(r.stack)-idx+1)
		path = append(path, r.stack[idx:]...)
		path = append(path, id)
		return nil, &CycleError{Path: path}
	}
	if n, ok := r.nodes[id]; ok {
		return n, nil // legitimate sharing: parsed once, reused
	}
	if r.opts.MaxTables > 0 && int64(r.count) >= int64(r.opts.MaxTables) {
		return nil, &BudgetError{Kind: BudgetTables, Limit: int64(r.opts.MaxTables), Used: int64(r.count)}
	}
	r.count++

	desc, err := r.parse(id, r.src)
	if err != nil {
		return nil, fmt.Errorf("netsec: parse %s: %w", id, err)
	}

	// Validate local bounds before touching any reference.
	if err := r.checkBounds(id, desc.Size); err != nil {
		return nil, err
	}
	if r.opts.MaxBytes > 0 && r.bytes+int64(desc.Size) > r.opts.MaxBytes {
		return nil, &BudgetError{Kind: BudgetBytes, Limit: r.opts.MaxBytes, Used: r.bytes}
	}
	if err := r.readFull(id, desc.Size); err != nil {
		return nil, err
	}
	r.bytes += int64(desc.Size)

	node := &Node{ID: id, Desc: desc}
	r.onStack[id] = len(r.stack)
	r.stack = append(r.stack, id)
	defer func() {
		delete(r.onStack, id)
		r.stack = r.stack[:len(r.stack)-1]
	}()

	for _, ref := range desc.Refs {
		if r.opts.AllowRef != nil && !r.opts.AllowRef(id.Type, ref.Type) {
			return nil, &RefNotAllowedError{From: id, To: ref}
		}
		child, err := r.resolve(ref)
		if err != nil {
			return nil, err
		}
		node.Children = append(node.Children, child)
	}

	r.nodes[id] = node
	return node, nil
}

func (r *Reader) checkBounds(id TableID, size uint64) error {
	fileSize := r.src.Size()
	if fileSize < 0 {
		return fmt.Errorf("netsec: source reports negative size %d", fileSize)
	}
	end := id.Offset + size
	if end < id.Offset || end > uint64(fileSize) {
		return &BoundsError{ID: id, Size: size, FileSize: fileSize}
	}
	return nil
}

// readFull proves the declared extent is actually readable, defending
// against images whose declared size overstates the real data.
func (r *Reader) readFull(id TableID, size uint64) error {
	const chunk = 32 << 10
	buf := make([]byte, chunk)
	var off uint64
	for off < size {
		n := uint64(chunk)
		if rem := size - off; rem < n {
			n = rem
		}
		if _, err := r.src.ReadAt(buf[:n], int64(id.Offset+off)); err != nil {
			return &ShortReadError{ID: id, Want: size, Got: off, Err: err}
		}
		off += n
	}
	return nil
}
