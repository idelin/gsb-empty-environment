package strdict

import "hash/fnv"

// Dictionary is an immutable-after-freeze string pool identified by a
// version. The empty string is a valid entry. Hash collisions in the
// internal index are expected and handled by bucket comparison; the hash
// function is injectable so tests can force collisions.
type Dictionary struct {
	version uint32
	entries []string
	buckets map[uint64][]uint32
	hashFn  func(string) uint64
	frozen  bool
	bytes   int64
}

// DictOption customizes a Dictionary under construction.
type DictOption func(*Dictionary)

// WithHashFunc overrides the hash used by the internal index. Intended for
// tests that force hash collisions; fn must be deterministic.
func WithHashFunc(fn func(string) uint64) DictOption {
	return func(d *Dictionary) { d.hashFn = fn }
}

// NewDictionary creates an empty, mutable dictionary for the given version.
func NewDictionary(version uint32, opts ...DictOption) *Dictionary {
	d := &Dictionary{
		version: version,
		buckets: make(map[uint64][]uint32),
		hashFn:  defaultHash,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

func defaultHash(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// Intern adds s if absent and returns its index. The empty string is
// interned like any other. It fails with ErrFrozen once Freeze was called.
func (d *Dictionary) Intern(s string) (uint32, error) {
	if d.frozen {
		return 0, ErrFrozen
	}
	if idx, ok := d.Index(s); ok {
		return idx, nil
	}
	idx := uint32(len(d.entries))
	d.entries = append(d.entries, s)
	h := d.hashFn(s)
	d.buckets[h] = append(d.buckets[h], idx)
	d.bytes += int64(len(s))
	return idx, nil
}

// Index returns the index of s, resolving hash collisions by comparing the
// actual strings in the bucket.
func (d *Dictionary) Index(s string) (uint32, bool) {
	for _, idx := range d.buckets[d.hashFn(s)] {
		if d.entries[idx] == s {
			return idx, true
		}
	}
	return 0, false
}

// Lookup returns the entry at idx. ok is false for unknown indices; callers
// must not treat that as the empty string.
func (d *Dictionary) Lookup(idx uint32) (string, bool) {
	if int(idx) >= len(d.entries) {
		return "", false
	}
	return d.entries[idx], true
}

// Freeze seals the dictionary. A frozen dictionary is immutable and can be
// published to a Registry.
func (d *Dictionary) Freeze() { d.frozen = true }

// Frozen reports whether Freeze was called.
func (d *Dictionary) Frozen() bool { return d.frozen }

// Version returns the dictionary version.
func (d *Dictionary) Version() uint32 { return d.version }

// Len returns the number of entries.
func (d *Dictionary) Len() int { return len(d.entries) }

// Bytes returns the total payload bytes of all entries. This is what a
// decode session is charged against its budget when it binds the
// dictionary.
func (d *Dictionary) Bytes() int64 { return d.bytes }
