package newsindex

import "sort"

type fragmentState struct {
	body  string
	order uint64
}

type messageState struct {
	fragments map[Group]*fragmentState
}

func (m *messageState) clone() *messageState {
	out := &messageState{fragments: make(map[Group]*fragmentState, len(m.fragments))}
	for group, fragment := range m.fragments {
		copyFragment := *fragment
		out.fragments[group] = &copyFragment
	}
	return out
}

type edgeKey struct {
	parent MessageID
	child  MessageID
}

type edgeScope struct {
	group Group
	any   bool
}

type edgeState map[edgeScope]struct{}

type snapshot struct {
	seq      uint64
	messages map[MessageID]*messageState
	edges    map[edgeKey]edgeState
	acl      map[PrincipalID]map[Group]struct{}
	authGen  map[PrincipalID]uint64
}

func newSnapshot(seq uint64) *snapshot {
	return &snapshot{
		seq:      seq,
		messages: make(map[MessageID]*messageState),
		edges:    make(map[edgeKey]edgeState),
		acl:      make(map[PrincipalID]map[Group]struct{}),
		authGen:  make(map[PrincipalID]uint64),
	}
}

func (s *snapshot) clone() *snapshot {
	out := &snapshot{
		seq:      s.seq,
		messages: make(map[MessageID]*messageState, len(s.messages)),
		edges:    make(map[edgeKey]edgeState, len(s.edges)),
		acl:      make(map[PrincipalID]map[Group]struct{}, len(s.acl)),
		authGen:  make(map[PrincipalID]uint64, len(s.authGen)),
	}
	for id, message := range s.messages {
		out.messages[id] = message.clone()
	}
	for key, scopes := range s.edges {
		copyScopes := make(edgeState, len(scopes))
		for scope := range scopes {
			copyScopes[scope] = struct{}{}
		}
		out.edges[key] = copyScopes
	}
	for user, groups := range s.acl {
		copyGroups := make(map[Group]struct{}, len(groups))
		for group := range groups {
			copyGroups[group] = struct{}{}
		}
		out.acl[user] = copyGroups
	}
	for user, generation := range s.authGen {
		out.authGen[user] = generation
	}
	return out
}

func (s *snapshot) generation(user PrincipalID) uint64 {
	return s.authGen[user]
}

func (s *snapshot) authorized(user PrincipalID) map[Group]struct{} {
	groups := make(map[Group]struct{}, len(s.acl[user])+1)
	groups[PublicGroup] = struct{}{}
	for group := range s.acl[user] {
		groups[group] = struct{}{}
	}
	return groups
}

func sameGroupSet(a, b map[Group]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for group := range a {
		if _, ok := b[group]; !ok {
			return false
		}
	}
	return true
}

func sortedMessageIDs(ids map[MessageID]struct{}) []MessageID {
	out := make([]MessageID, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
