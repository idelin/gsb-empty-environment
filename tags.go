package netsec

import (
	"sort"
)

// Tag is one session tag. Session tags are unordered for identity purposes.
type Tag struct {
	Key   string
	Value string
}

const sessionTagsDigestDomain = "netsec.credential-cache.session-tags.v1"

// CanonicalSessionTags returns a sorted copy of tags. Exact duplicate entries
// are preserved; tags with the same key are ordered by value.
func CanonicalSessionTags(tags []Tag) []Tag {
	out := append([]Tag(nil), tags...)
	sort.Slice(out, func(a, b int) bool {
		if out[a].Key != out[b].Key {
			return out[a].Key < out[b].Key
		}
		return out[a].Value < out[b].Value
	})
	return out
}

// SessionTagsDigest returns the canonical digest of a set of session tags.
// Reordering the input does not change the digest.
func SessionTagsDigest(tags []Tag) Digest {
	canonical := CanonicalSessionTags(tags)
	var buf []byte
	buf = appendTaggedBytes(buf, "domain", []byte(sessionTagsDigestDomain))
	buf = appendTaggedBytes(buf, "tag-count", uint64Bytes(uint64(len(canonical))))
	for _, tag := range canonical {
		buf = appendTaggedBytes(buf, "key", []byte(tag.Key))
		buf = appendTaggedBytes(buf, "value", []byte(tag.Value))
	}
	return DigestBytes(buf)
}

// CanonicalSessionTagsDigest is an alias for SessionTagsDigest.
func CanonicalSessionTagsDigest(tags []Tag) Digest { return SessionTagsDigest(tags) }

// SessionTagsDigestFromMap returns the canonical digest of a tag map.
func SessionTagsDigestFromMap(tags map[string]string) Digest {
	list := make([]Tag, 0, len(tags))
	for k, v := range tags {
		list = append(list, Tag{Key: k, Value: v})
	}
	return SessionTagsDigest(list)
}

func uint64Bytes(v uint64) []byte {
	var b [8]byte
	for i := 7; i >= 0; i-- {
		b[i] = byte(v)
		v >>= 8
	}
	return b[:]
}
