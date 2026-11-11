package netsec

import (
	"bytes"
	"context"
	"sort"
)

// ManifestStore is the host interface that serves snapshot manifests by
// digest. Implementations must be safe for concurrent use if the host calls
// Verify concurrently.
type ManifestStore interface {
	// Manifest returns the manifest stored under digest. A nil manifest with
	// a nil error means the snapshot does not exist.
	Manifest(ctx context.Context, digest Digest) (*Manifest, error)
}

// ManifestStoreFunc adapts a function to the ManifestStore interface.
type ManifestStoreFunc func(ctx context.Context, digest Digest) (*Manifest, error)

// Manifest implements ManifestStore.
func (f ManifestStoreFunc) Manifest(ctx context.Context, digest Digest) (*Manifest, error) {
	return f(ctx, digest)
}

// Authorizer is the host interface that answers sharing questions.
//
// ShareAuthorized reports whether snapshot digest, owned by owner, is
// currently accessible to importer. A revoked share must return false. The
// method is called for every snapshot reached through parent edges, including
// snapshots owned by the importer itself and hidden incremental bases, not
// only for the top-level manifest. Verifying every node means a revoked
// hidden base fails the import even when the root itself is authorized.
type Authorizer interface {
	ShareAuthorized(ctx context.Context, importer TenantID, owner TenantID, digest Digest) (bool, error)
}

// AuthorizerFunc adapts a function to the Authorizer interface.
type AuthorizerFunc func(ctx context.Context, importer TenantID, owner TenantID, digest Digest) (bool, error)

// ShareAuthorized implements Authorizer.
func (f AuthorizerFunc) ShareAuthorized(ctx context.Context, importer TenantID, owner TenantID, digest Digest) (bool, error) {
	return f(ctx, importer, owner, digest)
}

// node holds the per-snapshot state accumulated during closure verification.
type node struct {
	manifest *Manifest
	depth    int
}

// Verify materializes and validates the full incremental closure of root for
// importer.
//
// Verification, in order:
//
//  1. Walk every snapshot reachable through parent edges (breadth first),
//     fetching each manifest from store and recording its minimum depth from
//     root. A missing manifest, bad digest, or structurally invalid manifest
//     fails the import.
//  2. Check sharing authorization for every reached snapshot. Snapshots
//     owned by the importer, foreign-tenant snapshots, and hidden bases alike
//     are passed to the Authorizer; a false result (for example a revoked
//     share) fails the import.
//  3. Reject any cycle in the parent graph.
//  4. Resolve each logical block to exactly one physical source. A snapshot
//     nearer to the root overrides the same logical block in deeper
//     incremental bases. Two distinct sources at equal depth are a conflict.
//     Every block source must resolve inside the authorized closure and be
//     owned by the tenant it claims.
func Verify(ctx context.Context, importer TenantID, root Digest, store ManifestStore, auth Authorizer) (*Closure, error) {
	if importer == "" {
		return nil, &InvalidManifestError{Reason: "empty importer tenant"}
	}
	if len(root) == 0 {
		return nil, &InvalidManifestError{Reason: "empty root digest"}
	}
	if store == nil {
		return nil, &InvalidManifestError{Reason: "nil manifest store"}
	}
	if auth == nil {
		return nil, &InvalidManifestError{Reason: "nil authorizer"}
	}

	nodes, err := discover(ctx, importer, root, store, auth)
	if err != nil {
		return nil, err
	}
	if err := detectCycle(root, nodes); err != nil {
		return nil, err
	}
	blocks, err := resolveBlocks(nodes)
	if err != nil {
		return nil, err
	}

	digests := make([]Digest, 0, len(nodes))
	for d := range nodes {
		digests = append(digests, Digest(d))
	}
	sort.Slice(digests, func(i, j int) bool {
		return bytes.Compare(digests[i], digests[j]) < 0
	})

	return &Closure{
		Importer:  importer,
		Root:      append(Digest(nil), root...),
		Snapshots: digests,
		Depths:    closureDepths(nodes),
		Blocks:    blocks,
	}, nil
}

func closureDepths(nodes map[string]*node) map[string]int {
	depths := make(map[string]int, len(nodes))
	for key, current := range nodes {
		depths[key] = current.depth
	}
	return depths
}

// discover performs the BFS over parent edges, validates each manifest, and
// enforces sharing authorization for every reached snapshot.
func discover(ctx context.Context, importer TenantID, root Digest, store ManifestStore, auth Authorizer) (map[string]*node, error) {
	nodes := make(map[string]*node)
	queue := []Digest{append(Digest(nil), root...)}
	nodes[string(root)] = &node{depth: 0}

	for len(queue) > 0 {
		select {
		case <-ctx.Done():
			return nil, ErrCanceled
		default:
		}

		current := queue[0]
		queue = queue[1:]
		currentNode := nodes[string(current)]

		manifest, err := store.Manifest(ctx, current)
		if err != nil {
			return nil, err
		}
		if manifest == nil {
			return nil, &SnapshotNotFoundError{Digest: append(Digest(nil), current...)}
		}
		if err := validateManifest(current, manifest); err != nil {
			return nil, err
		}
		currentNode.manifest = manifest

		ok, err := auth.ShareAuthorized(ctx, importer, manifest.Owner, current)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &UnauthorizedError{
				Snapshot: append(Digest(nil), current...),
				Owner:    manifest.Owner,
				Importer: importer,
			}
		}

		for _, parent := range manifest.Parents {
			key := string(parent.Digest)
			if _, seen := nodes[key]; seen {
				// BFS discovers nodes at their minimum depth first; a later
				// encounter is always an equally deep or deeper path.
				continue
			}
			nodes[key] = &node{depth: currentNode.depth + 1}
			queue = append(queue, append(Digest(nil), parent.Digest...))
		}
	}
	return nodes, nil
}

// validateManifest enforces self-consistency of one manifest.
func validateManifest(requested Digest, manifest *Manifest) error {
	if len(manifest.Digest) == 0 {
		return &InvalidManifestError{Snapshot: requested, Reason: "empty declared digest"}
	}
	if !bytes.Equal(manifest.Digest, requested) {
		return &DigestMismatchError{
			Requested: append(Digest(nil), requested...),
			Declared:  append(Digest(nil), manifest.Digest...),
		}
	}
	if manifest.Owner == "" {
		return &InvalidManifestError{Snapshot: requested, Reason: "empty owner"}
	}

	seenBlocks := make(map[BlockID]struct{}, len(manifest.Blocks))
	for _, entry := range manifest.Blocks {
		if entry.Block == "" {
			return &InvalidManifestError{Snapshot: requested, Reason: "empty block id"}
		}
		if _, dup := seenBlocks[entry.Block]; dup {
			return &InvalidManifestError{
				Snapshot: requested,
				Reason:   "duplicate block " + string(entry.Block) + " within manifest",
			}
		}
		seenBlocks[entry.Block] = struct{}{}
		if entry.Source.Tenant == "" || len(entry.Source.Snapshot) == 0 {
			return &InvalidSourceError{
				Block:  entry.Block,
				Source: entry.Source,
				Reason: "source must name an owner tenant and a snapshot",
			}
		}
	}

	seenParents := make(map[string]struct{}, len(manifest.Parents))
	for _, parent := range manifest.Parents {
		if len(parent.Digest) == 0 {
			return &InvalidManifestError{Snapshot: requested, Reason: "empty parent digest"}
		}
		key := string(parent.Digest)
		if _, dup := seenParents[key]; dup {
			return &InvalidManifestError{
				Snapshot: requested,
				Reason:   "duplicate parent " + digestString(parent.Digest),
			}
		}
		seenParents[key] = struct{}{}
	}
	return nil
}

// detectCycle runs an iterative DFS over the discovered parent graph. Node
// colors are white (unvisited), gray (on the current DFS stack), black
// (finished). A gray target means a back edge and therefore a cycle.
func detectCycle(root Digest, nodes map[string]*node) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(nodes))

	type frame struct {
		digest  Digest
		parents []Digest
		index   int
	}

	stack := []frame{{
		digest:  append(Digest(nil), root...),
		parents: sortedParents(nodes[string(root)].manifest),
	}}
	color[string(root)] = gray

	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.index >= len(top.parents) {
			color[string(top.digest)] = black
			stack = stack[:len(stack)-1]
			continue
		}
		parent := top.parents[top.index]
		top.index++

		switch color[string(parent)] {
		case gray:
			return &CycleError{
				On:     append(Digest(nil), top.digest...),
				Parent: append(Digest(nil), parent...),
			}
		case black:
			continue
		default:
			color[string(parent)] = gray
			stack = append(stack, frame{
				digest:  append(Digest(nil), parent...),
				parents: sortedParents(nodes[string(parent)].manifest),
			})
		}
	}
	return nil
}

// sortedParents returns a manifest's parent digests in digest order so that
// cycle detection and diagnostics are deterministic.
func sortedParents(manifest *Manifest) []Digest {
	parents := make([]Digest, len(manifest.Parents))
	for i, parent := range manifest.Parents {
		parents[i] = append(Digest(nil), parent.Digest...)
	}
	sort.Slice(parents, func(i, j int) bool {
		return bytes.Compare(parents[i], parents[j]) < 0
	})
	return parents
}

// candidate records one source offer for a logical block.
type candidate struct {
	source BlockSource
	depth  int
}

// resolveBlocks enforces the single-provider rule across the whole closure.
func resolveBlocks(nodes map[string]*node) (map[BlockID]ResolvedBlock, error) {
	offers := make(map[BlockID][]candidate)

	digests := make([]string, 0, len(nodes))
	for key := range nodes {
		digests = append(digests, key)
	}
	sort.Strings(digests)

	for _, key := range digests {
		current := nodes[key]
		for _, entry := range current.manifest.Blocks {
			sourceNode, ok := nodes[string(entry.Source.Snapshot)]
			if !ok {
				return nil, &InvalidSourceError{
					Block:  entry.Block,
					Source: entry.Source,
					Reason: "source snapshot " + digestString(entry.Source.Snapshot) +
						" is outside the authorized closure",
				}
			}
			if sourceNode.manifest.Owner != entry.Source.Tenant {
				return nil, &InvalidSourceError{
					Block:  entry.Block,
					Source: entry.Source,
					Reason: "source claims tenant " + string(entry.Source.Tenant) +
						" but snapshot is owned by " + string(sourceNode.manifest.Owner),
				}
			}
			offers[entry.Block] = append(offers[entry.Block], candidate{
				source: copySource(entry.Source),
				depth:  current.depth,
			})
		}
	}

	resolved := make(map[BlockID]ResolvedBlock, len(offers))
	blockIDs := make([]BlockID, 0, len(offers))
	for block := range offers {
		blockIDs = append(blockIDs, block)
	}
	sort.Slice(blockIDs, func(i, j int) bool { return blockIDs[i] < blockIDs[j] })

	for _, block := range blockIDs {
		list := offers[block]
		minDepth := list[0].depth
		for _, offer := range list[1:] {
			if offer.depth < minDepth {
				minDepth = offer.depth
			}
		}

		var winner candidate
		winners := 0
		for _, offer := range list {
			if offer.depth != minDepth {
				continue
			}
			if winners == 0 {
				winner = offer
				winners = 1
				continue
			}
			if sameSource(winner.source, offer.source) {
				continue
			}
			return nil, &BlockConflictError{
				Block:  block,
				First:  winner.source,
				Second: offer.source,
				Depth:  minDepth,
			}
		}

		resolved[block] = ResolvedBlock{
			Block:  block,
			Source: winner.source,
			Depth:  minDepth,
		}
	}
	return resolved, nil
}

func sameSource(a, b BlockSource) bool {
	return a.Tenant == b.Tenant && bytes.Equal(a.Snapshot, b.Snapshot)
}

func copySource(source BlockSource) BlockSource {
	return BlockSource{
		Tenant:   source.Tenant,
		Snapshot: append(Digest(nil), source.Snapshot...),
	}
}
