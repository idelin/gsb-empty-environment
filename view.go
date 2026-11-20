package newsindex

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"sort"
)

type materializedView struct {
	token          string
	items          []MessageView
	index          map[MessageID]int
	authGeneration uint64
}

type orderedMessage struct {
	order uint64
	view  MessageView
}

func buildView(snap *snapshot, user PrincipalID) *materializedView {
	authorized := snap.authorized(user)
	candidates := make([]orderedMessage, 0)

	for id, message := range snap.messages {
		fragments := make([]FragmentView, 0, len(message.fragments))
		var firstOrder uint64
		hasOrder := false
		for group, fragment := range message.fragments {
			if _, ok := authorized[group]; !ok {
				continue
			}
			fragments = append(fragments, FragmentView{Group: group, Body: fragment.body})
			if !hasOrder || fragment.order < firstOrder {
				firstOrder = fragment.order
				hasOrder = true
			}
		}
		if !hasOrder {
			continue
		}
		sort.Slice(fragments, func(i, j int) bool {
			return fragments[i].Group < fragments[j].Group
		})
		candidates = append(candidates, orderedMessage{
			order: firstOrder,
			view:  MessageView{ID: id, Fragments: fragments},
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].order != candidates[j].order {
			return candidates[i].order < candidates[j].order
		}
		return candidates[i].view.ID < candidates[j].view.ID
	})

	items := make([]MessageView, len(candidates))
	index := make(map[MessageID]int, len(candidates))
	for i, candidate := range candidates {
		items[i] = candidate.view
		index[candidate.view.ID] = i
	}

	replies := make(map[MessageID]map[MessageID]struct{})
	referencedBy := make(map[MessageID]map[MessageID]struct{})
	for key, scopes := range snap.edges {
		if len(scopes) == 0 {
			continue
		}
		if _, ok := index[key.parent]; !ok {
			continue
		}
		if _, ok := index[key.child]; !ok {
			continue
		}
		addPair(replies, key.parent, key.child)
		addPair(referencedBy, key.child, key.parent)
	}

	for i := range items {
		id := items[i].ID
		items[i].Replies = sortedMessageIDs(replies[id])
		items[i].ReferencedBy = sortedMessageIDs(referencedBy[id])
		items[i].ReplyCount = len(items[i].Replies)
		items[i].ReferencedByCount = len(items[i].ReferencedBy)
	}

	return &materializedView{
		token:          viewToken(items),
		items:          items,
		index:          index,
		authGeneration: snap.generation(user),
	}
}

func addPair(pairs map[MessageID]map[MessageID]struct{}, from, to MessageID) {
	set := pairs[from]
	if set == nil {
		set = make(map[MessageID]struct{})
		pairs[from] = set
	}
	set[to] = struct{}{}
}

// viewToken hashes only the visible view. In particular, it does not contain
// absolute publication sequence numbers, hidden fragments, edges touching
// hidden messages, ACLs of other principals, or commit numbers. Two archives
// whose authorized views are identical therefore produce the same token.
func viewToken(items []MessageView) string {
	digest := sha256.New()
	writeString(digest, "newsindex.view.v1")
	for _, message := range items {
		writeString(digest, string(message.ID))
		writeUint(digest, uint64(len(message.Fragments)))
		for _, fragment := range message.Fragments {
			writeString(digest, string(fragment.Group))
			writeString(digest, fragment.Body)
		}
		writeStrings(digest, message.Replies)
		writeStrings(digest, message.ReferencedBy)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writeStrings(writer io.Writer, values []MessageID) {
	writeUint(writer, uint64(len(values)))
	for _, value := range values {
		writeString(writer, string(value))
	}
}

func writeString(writer io.Writer, value string) {
	writeUint(writer, uint64(len(value)))
	_, _ = io.WriteString(writer, value)
}

func writeUint(writer io.Writer, value uint64) {
	var buffer [8]byte
	binary.BigEndian.PutUint64(buffer[:], value)
	_, _ = writer.Write(buffer[:])
}
