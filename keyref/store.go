// Package keyref resolves key-alias references inside a multi-account key
// host. Aliases are scoped to (account, region) and are never treated as a
// global identity: the same alias name may bind different key material in
// different accounts.
package keyref

import "sync"

// Ref identifies an alias within a specific account and region.
type Ref struct {
	Account string
	Region  string
	Alias   string
}

// AliasTarget is the destination of an alias binding: either concrete key
// material (KeyID, resolved within the alias's own account and region) or
// another alias, enabling multi-hop chains.
type AliasTarget struct {
	KeyID string
	Alias *Ref
}

// AliasBinding binds an alias to a target at a specific version.
type AliasBinding struct {
	Ref     Ref
	Version int64
	Target  AliasTarget
}

// KeyMaterial is a versioned public key record.
type KeyMaterial struct {
	Account   string
	Region    string
	KeyID     string
	Version   int64
	PublicKey []byte // PKIX DER
}

type versionedBinding struct {
	seq     int64
	binding AliasBinding
}

type versionedKey struct {
	seq int64
	key KeyMaterial
}

// Store is an in-memory MVCC key host. Every mutation advances a commit
// sequence; a Tx pins the sequence at Begin time so all reads inside one
// transaction observe a consistent snapshot, even if aliases are rebound
// concurrently.
type Store struct {
	mu      sync.Mutex
	seq     int64
	aliases map[Ref][]versionedBinding
	keys    map[keyRef][]versionedKey

	// OnAliasRead, if set, is invoked (without the store lock held) after
	// each alias read. It exists so tests can simulate a concurrent
	// rebinding in the middle of a resolution.
	OnAliasRead func(ref Ref)
}

type keyRef struct {
	account string
	region  string
	keyID   string
}

func NewStore() *Store {
	return &Store{
		aliases: make(map[Ref][]versionedBinding),
		keys:    make(map[keyRef][]versionedKey),
	}
}

// BindAlias appends a new version of the alias binding and returns the
// binding with its assigned version.
func (s *Store) BindAlias(ref Ref, target AliasTarget) AliasBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	version := int64(1)
	if entries := s.aliases[ref]; len(entries) > 0 {
		version = entries[len(entries)-1].binding.Version + 1
	}
	b := AliasBinding{Ref: ref, Version: version, Target: target}
	s.aliases[ref] = append(s.aliases[ref], versionedBinding{seq: s.seq, binding: b})
	return b
}

// PutKey stores a new version of the key material and returns it with its
// assigned version.
func (s *Store) PutKey(account, region, keyID string, publicKey []byte) KeyMaterial {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	kr := keyRef{account: account, region: region, keyID: keyID}
	version := int64(1)
	if entries := s.keys[kr]; len(entries) > 0 {
		version = entries[len(entries)-1].key.Version + 1
	}
	k := KeyMaterial{
		Account:   account,
		Region:    region,
		KeyID:     keyID,
		Version:   version,
		PublicKey: append([]byte(nil), publicKey...),
	}
	s.keys[kr] = append(s.keys[kr], versionedKey{seq: s.seq, key: k})
	return k
}

// Tx is a read snapshot of the Store. All resolutions performed with the
// same Tx are pinned to the same commit sequence.
type Tx struct {
	store *Store
	snap  int64
}

// Begin opens a new snapshot transaction.
func (s *Store) Begin() *Tx {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &Tx{store: s, snap: s.seq}
}

// GetAlias returns the newest binding of ref visible to this transaction.
func (t *Tx) GetAlias(ref Ref) (AliasBinding, bool) {
	t.store.mu.Lock()
	entries := t.store.aliases[ref]
	var out AliasBinding
	found := false
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].seq <= t.snap {
			out = entries[i].binding
			found = true
			break
		}
	}
	hook := t.store.OnAliasRead
	t.store.mu.Unlock()
	if hook != nil {
		hook(ref)
	}
	return out, found
}

// GetKey returns the newest version of the key visible to this transaction.
func (t *Tx) GetKey(account, region, keyID string) (KeyMaterial, bool) {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	entries := t.store.keys[keyRef{account: account, region: region, keyID: keyID}]
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].seq <= t.snap {
			return entries[i].key, true
		}
	}
	return KeyMaterial{}, false
}
