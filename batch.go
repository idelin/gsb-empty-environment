package newsindex

// Batch is an ordered, atomic set of archive mutations. Operations are applied
// in order to construct the post-commit state, but no intermediate state is
// observable and notifications are computed only from the complete pre-commit
// and post-commit visible views.
type Batch struct {
	ops []operation
	err error
}

type opKind uint8

const (
	opPut opKind = iota
	opRemove
	opLink
	opUnlink
	opGrant
	opRevoke
)

type operation struct {
	kind  opKind
	group Group
	id    MessageID
	body  string

	parent MessageID
	child  MessageID
	scoped bool

	user PrincipalID
}

// NewBatch returns an empty batch.
func NewBatch() *Batch {
	return &Batch{}
}

func (b *Batch) invalid(err error) *Batch {
	if b.err == nil {
		b.err = err
	}
	return b
}

// Put publishes or replaces the message's content fragment in group.
// Replacing content preserves the fragment's original first-publication order.
func (b *Batch) Put(group Group, id MessageID, body string) *Batch {
	if group == "" || id == "" {
		return b.invalid(ErrInvalidOperation)
	}
	b.ops = append(b.ops, operation{kind: opPut, group: group, id: id, body: body})
	return b
}

// Publish is an alias for Put.
func (b *Batch) Publish(group Group, id MessageID, body string) *Batch {
	return b.Put(group, id, body)
}

// Remove removes the message's fragment from group. If that was the message's
// last fragment, the message ceases to exist.
func (b *Batch) Remove(group Group, id MessageID) *Batch {
	if group == "" || id == "" {
		return b.invalid(ErrInvalidOperation)
	}
	b.ops = append(b.ops, operation{kind: opRemove, group: group, id: id})
	return b
}

// Link records one directed reply edge. Linking an existing pair again is
// idempotent. The edge is visible only while both endpoints are visible.
func (b *Batch) Link(parent, child MessageID) *Batch {
	if parent == "" || child == "" {
		return b.invalid(ErrInvalidOperation)
	}
	b.ops = append(b.ops, operation{
		kind:   opLink,
		parent: parent,
		child:  child,
	})
	return b
}

// LinkIn records a reply edge in a group's published edge set. Multiple group
// associations of the same pair are deliberately collapsed when reply counts
// are computed.
func (b *Batch) LinkIn(group Group, parent, child MessageID) *Batch {
	if group == "" || parent == "" || child == "" {
		return b.invalid(ErrInvalidOperation)
	}
	b.ops = append(b.ops, operation{
		kind:   opLink,
		group:  group,
		parent: parent,
		child:  child,
		scoped: true,
	})
	return b
}

// Unlink removes all published associations of a directed reply pair.
func (b *Batch) Unlink(parent, child MessageID) *Batch {
	if parent == "" || child == "" {
		return b.invalid(ErrInvalidOperation)
	}
	b.ops = append(b.ops, operation{
		kind:   opUnlink,
		parent: parent,
		child:  child,
	})
	return b
}

// UnlinkIn removes only one group's association of a directed reply pair.
// The pair remains visible if another association still exists.
func (b *Batch) UnlinkIn(group Group, parent, child MessageID) *Batch {
	if group == "" || parent == "" || child == "" {
		return b.invalid(ErrInvalidOperation)
	}
	b.ops = append(b.ops, operation{
		kind:   opUnlink,
		group:  group,
		parent: parent,
		child:  child,
		scoped: true,
	})
	return b
}

// Grant grants a private group to a principal. Granting PublicGroup is a
// no-op because the public group is implicit for every principal.
func (b *Batch) Grant(user PrincipalID, group Group) *Batch {
	if user == "" || group == "" {
		return b.invalid(ErrInvalidOperation)
	}
	b.ops = append(b.ops, operation{kind: opGrant, user: user, group: group})
	return b
}

// Revoke revokes a private group from a principal. Revoking PublicGroup is a
// no-op.
func (b *Batch) Revoke(user PrincipalID, group Group) *Batch {
	if user == "" || group == "" {
		return b.invalid(ErrInvalidOperation)
	}
	b.ops = append(b.ops, operation{kind: opRevoke, user: user, group: group})
	return b
}

// Len returns the number of queued operations.
func (b *Batch) Len() int {
	if b == nil {
		return 0
	}
	return len(b.ops)
}
