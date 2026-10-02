package checksumdiff

import "sync"

// Store 是一个线程安全的内存行存储，内置滚动校验和索引。
// 源库与目标库各持有一个实例；任何写入（包括绕过复制链路的篡改）
// 都会即时反映到校验和上，从而被巡检器发现。
//
// 行按分桶组织，使得失配分桶的行级下钻成本为 O(桶内行数) 而非 O(全表)。
type Store struct {
	mu      sync.RWMutex
	name    string
	buckets [NumBuckets]map[int64]Row
	idx     Index
	size    int
}

func NewStore(name string) *Store {
	s := &Store{name: name}
	for b := range s.buckets {
		s.buckets[b] = make(map[int64]Row)
	}
	return s
}

func (s *Store) Name() string { return s.name }

// Upsert 插入或更新一行，并增量维护校验和索引。
func (s *Store) Upsert(r Row) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := bucketOf(r.ID)
	if old, ok := s.buckets[b][r.ID]; ok {
		s.idx.Update(r.ID, old.Hash(), r.Hash())
	} else {
		s.idx.Add(r.ID, r.Hash())
		s.size++
	}
	s.buckets[b][r.ID] = r
}

// Delete 删除一行，返回该行是否存在。
func (s *Store) Delete(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := bucketOf(id)
	old, ok := s.buckets[b][id]
	if !ok {
		return false
	}
	s.idx.Remove(id, old.Hash())
	delete(s.buckets[b], id)
	s.size--
	return true
}

func (s *Store) Get(id int64) (Row, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.buckets[bucketOf(id)][id]
	return r, ok
}

func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.size
}

// Root 返回当前整库根签名。
func (s *Store) Root() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.idx.Root()
}

// BucketSig 返回指定分桶的当前签名。
func (s *Store) BucketSig(b int) BucketSig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.idx.BucketSig(b)
}

// BucketRows 返回指定分桶内所有行的快照（按主键排序）。
func (s *Store) BucketRows(b int) []Row {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Row, 0, len(s.buckets[b]))
	for _, r := range s.buckets[b] {
		out = append(out, r)
	}
	sortRows(out)
	return out
}

func sortRows(rows []Row) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].ID < rows[j-1].ID; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}
