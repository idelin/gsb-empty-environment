package netsec

import (
	"crypto/sha256"
	"sync"
)

// keyedMutex hands out one lock per string key, sharded to keep unrelated
// keys from contending. It gives the in-memory store the same per-credential
// serialization a SQL deployment gets from SELECT ... FOR UPDATE.
type keyedMutex struct {
	shards [64]struct {
		mu    sync.Mutex
		locks map[string]*sync.Mutex
	}
}

func (k *keyedMutex) lock(key []byte) func() {
	sum := sha256.Sum256(key)
	index := int(sum[0]) % len(k.shards)
	shard := &k.shards[index]

	shard.mu.Lock()
	if shard.locks == nil {
		shard.locks = make(map[string]*sync.Mutex)
	}
	entry, ok := shard.locks[string(key)]
	if !ok {
		entry = &sync.Mutex{}
		shard.locks[string(key)] = entry
	}
	shard.mu.Unlock()

	entry.Lock()
	return entry.Unlock
}
