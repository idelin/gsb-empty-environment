package lsmwatch

// SSTable 表示一个不可变的排序字符串表（此处用 map 模拟其内容）。
type SSTable struct {
	ID           int
	entries      map[string]string
	bloom        *BloomFilter
	BloomEnabled bool // 关闭后 Get 必须真实读取该表（模拟过滤器失效）
}

// NewSSTable 用给定 entries 构建 SSTable，并按 bitsPerKey 建立 Bloom Filter。
func NewSSTable(id int, entries map[string]string, bitsPerKey int) *SSTable {
	t := &SSTable{ID: id, entries: entries, BloomEnabled: true}
	t.RebuildFilter(bitsPerKey)
	return t
}

// RebuildFilter 以新的 bitsPerKey 重建 Bloom Filter。
func (t *SSTable) RebuildFilter(bitsPerKey int) {
	t.bloom = NewBloomFilter(len(t.entries), bitsPerKey)
	for k := range t.entries {
		t.bloom.Add(k)
	}
}

// mayContain 返回是否"可能"包含 key。过滤器被关闭时只能返回 true，
// 迫使引擎真实读取该表，从而抬升读放大。
func (t *SSTable) mayContain(key string) bool {
	if !t.BloomEnabled || t.bloom == nil {
		return true
	}
	return t.bloom.Test(key)
}

func (t *SSTable) get(key string) (string, bool) {
	v, ok := t.entries[key]
	return v, ok
}

func (t *SSTable) filterActive() bool {
	return t.BloomEnabled && t.bloom != nil
}
