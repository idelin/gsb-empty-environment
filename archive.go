package newsindex

import (
	"fmt"
	"sync"
)

// Archive is a concurrency-safe, in-memory archive of immutable versions.
type Archive struct {
	mu sync.RWMutex

	nextOrder uint64
	current   *snapshot
	snapshots map[uint64]*snapshot

	// tokenSnapshots maps a principal, authorization generation, and
	// visible-view token to a version whose view produced that token. The
	// mapping never includes hidden data.
	tokenSnapshots map[PrincipalID]map[viewTokenKey]uint64
}

type viewTokenKey struct {
	token          string
	authGeneration uint64
}

// New creates an empty archive. Version zero is an empty snapshot.
func New() *Archive {
	initial := newSnapshot(0)
	return &Archive{
		nextOrder:      1,
		current:        initial,
		snapshots:      map[uint64]*snapshot{0: initial},
		tokenSnapshots: make(map[PrincipalID]map[viewTokenKey]uint64),
	}
}

// NewArchive is an alias for New.
func NewArchive() *Archive {
	return New()
}

// Commit applies a batch atomically and returns the new version number.
func (a *Archive) Commit(batch *Batch) (CommitResult, error) {
	if batch == nil {
		return CommitResult{}, ErrNilBatch
	}
	if batch.err != nil {
		return CommitResult{}, batch.err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	next := a.current.clone()
	next.seq = a.current.seq + 1

	affectedUsers := make(map[PrincipalID]struct{})
	// If a fragment is removed and re-added inside one batch, keep its
	// original first-publication order. Across batches a re-added fragment
	// starts a new publication and receives a new order.
	batchOrders := make(map[fragmentKey]uint64)

	for _, op := range batch.ops {
		switch op.kind {
		case opPut:
			message := next.messages[op.id]
			if message == nil {
				message = &messageState{fragments: make(map[Group]*fragmentState)}
				next.messages[op.id] = message
			}
			if fragment := message.fragments[op.group]; fragment != nil {
				fragment.body = op.body
				continue
			}
			key := fragmentKey{group: op.group, id: op.id}
			order, ok := batchOrders[key]
			if ok {
				delete(batchOrders, key)
			} else {
				order = a.nextOrder
				a.nextOrder++
			}
			message.fragments[op.group] = &fragmentState{
				body:  op.body,
				order: order,
			}

		case opRemove:
			message := next.messages[op.id]
			if message == nil {
				continue
			}
			fragment := message.fragments[op.group]
			if fragment == nil {
				continue
			}
			key := fragmentKey{group: op.group, id: op.id}
			if _, exists := batchOrders[key]; !exists {
				batchOrders[key] = fragment.order
			}
			delete(message.fragments, op.group)
			if len(message.fragments) == 0 {
				delete(next.messages, op.id)
			}

		case opLink:
			key := edgeKey{parent: op.parent, child: op.child}
			scopes := next.edges[key]
			if scopes == nil {
				scopes = make(edgeState)
				next.edges[key] = scopes
			}
			if op.scoped {
				scopes[edgeScope{group: op.group}] = struct{}{}
			} else {
				scopes[edgeScope{any: true}] = struct{}{}
			}

		case opUnlink:
			key := edgeKey{parent: op.parent, child: op.child}
			if op.scoped {
				scopes := next.edges[key]
				if scopes != nil {
					delete(scopes, edgeScope{group: op.group})
					if len(scopes) == 0 {
						delete(next.edges, key)
					}
				}
			} else {
				delete(next.edges, key)
			}

		case opGrant:
			affectedUsers[op.user] = struct{}{}
			if op.group == PublicGroup {
				continue
			}
			groups := next.acl[op.user]
			if groups == nil {
				groups = make(map[Group]struct{})
				next.acl[op.user] = groups
			}
			groups[op.group] = struct{}{}

		case opRevoke:
			affectedUsers[op.user] = struct{}{}
			if op.group == PublicGroup {
				continue
			}
			groups := next.acl[op.user]
			if groups == nil {
				continue
			}
			delete(groups, op.group)
			if len(groups) == 0 {
				delete(next.acl, op.user)
			}
		}
	}

	// A message that no longer exists must not leave reply edges behind.
	// Edges whose endpoints both exist, even if they are only present in
	// unauthorized groups, remain in the version and are merely invisible.
	for key := range next.edges {
		if next.messages[key.parent] == nil || next.messages[key.child] == nil {
			delete(next.edges, key)
		}
	}

	for user := range affectedUsers {
		if !sameGroupSet(a.current.acl[user], next.acl[user]) {
			next.authGen[user] = a.current.generation(user) + 1
		}
	}

	a.snapshots[next.seq] = next
	a.current = next
	return CommitResult{Snapshot: next.seq}, nil
}

type fragmentKey struct {
	group Group
	id    MessageID
}

func (a *Archive) registerViewToken(user PrincipalID, token string, generation, seq uint64) {
	byToken := a.tokenSnapshots[user]
	if byToken == nil {
		byToken = make(map[viewTokenKey]uint64)
		a.tokenSnapshots[user] = byToken
	}
	key := viewTokenKey{token: token, authGeneration: generation}
	if _, exists := byToken[key]; !exists {
		byToken[key] = seq
	}
}

// CurrentSnapshot returns the latest committed version number.
func (a *Archive) CurrentSnapshot() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.current.seq
}

// AuthorizationGeneration returns the principal's current authorization
// generation.
func (a *Archive) AuthorizationGeneration(user PrincipalID) uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.current.generation(user)
}

// Page returns a page from the latest snapshot, or continues the immutable
// visible snapshot identified by req.Cursor.
func (a *Archive) Page(user PrincipalID, req PageRequest) (Page, error) {
	if req.Limit <= 0 {
		return Page{}, ErrInvalidPageSize
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	var (
		snap    *snapshot
		view    *materializedView
		payload cursorPayload
	)

	if req.Cursor == "" {
		snap = a.current
		view = buildView(snap, user)
		a.registerViewToken(user, view.token, view.authGeneration, snap.seq)
	} else {
		var err error
		payload, err = decodeCursor(req.Cursor)
		if err != nil {
			return Page{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		if payload.Principal != user {
			return Page{}, ErrCursorPrincipalMismatch
		}
		if payload.AuthGeneration != a.current.generation(user) {
			return Page{}, ErrStaleCursor
		}
		seq, ok := a.tokenSnapshots[user][viewTokenKey{
			token:          payload.Snapshot,
			authGeneration: payload.AuthGeneration,
		}]
		if !ok {
			return Page{}, ErrInvalidCursor
		}
		snap = a.snapshots[seq]
		if snap == nil {
			return Page{}, ErrInvalidCursor
		}
		view = buildView(snap, user)
		if view.token != payload.Snapshot || view.authGeneration != payload.AuthGeneration {
			return Page{}, ErrInvalidCursor
		}
	}

	start := 0
	if req.Cursor != "" {
		index, ok := view.index[payload.After]
		if !ok {
			return Page{}, ErrInvalidCursor
		}
		start = index + 1
	}

	end := len(view.items)
	if req.Limit < len(view.items)-start {
		end = start + req.Limit
	}
	items := cloneMessages(view.items[start:end])

	next := ""
	if end < len(view.items) && len(items) > 0 {
		next = encodeCursor(user, view.authGeneration, view.token, items[len(items)-1].ID)
	}

	return Page{
		Items:          items,
		Next:           next,
		Snapshot:       view.token,
		AuthGeneration: view.authGeneration,
	}, nil
}

// Get returns the latest visible view of a message. A message that exists only
// in unauthorized groups returns exactly the same result as an unknown ID:
// the zero MessageView and ErrMessageNotFound.
func (a *Archive) Get(user PrincipalID, id MessageID) (MessageView, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	view := buildView(a.current, user)
	if index, ok := view.index[id]; ok {
		return cloneMessage(view.items[index]), nil
	}
	return MessageView{}, ErrMessageNotFound
}

// GetAt is Get against a previously returned principal-specific snapshot
// token. It is useful for rendering links from a page without mixing versions.
func (a *Archive) GetAt(user PrincipalID, snapshotToken string, id MessageID) (MessageView, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	seq, ok := a.tokenSnapshots[user][viewTokenKey{
		token:          snapshotToken,
		authGeneration: a.current.generation(user),
	}]
	if !ok {
		return MessageView{}, ErrInvalidCursor
	}
	snap := a.snapshots[seq]
	if snap == nil {
		return MessageView{}, ErrInvalidCursor
	}
	view := buildView(snap, user)
	if view.token != snapshotToken || view.authGeneration != a.current.generation(user) {
		return MessageView{}, ErrInvalidCursor
	}
	if index, ok := view.index[id]; ok {
		return cloneMessage(view.items[index]), nil
	}
	return MessageView{}, ErrMessageNotFound
}

func cloneMessage(message MessageView) MessageView {
	out := message
	out.Fragments = append([]FragmentView(nil), message.Fragments...)
	out.Replies = append([]MessageID(nil), message.Replies...)
	out.ReferencedBy = append([]MessageID(nil), message.ReferencedBy...)
	return out
}

func cloneMessages(messages []MessageView) []MessageView {
	out := make([]MessageView, len(messages))
	for i, message := range messages {
		out[i] = cloneMessage(message)
	}
	return out
}
