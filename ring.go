package shardgw

import (
	"hash/crc32"
	"sort"
	"strconv"
)

// hashRing 是一致性哈希环：节点上下线时仅影响相邻区间的 key，
// 保证集群内重分片时映射扰动最小（平滑重选）。
type hashRing struct {
	replicas int
	keys     []uint32         // 排序后的哈希点
	owners   map[uint32]*Node // 哈希点 → 节点
}

func newHashRing(replicas int) *hashRing {
	return &hashRing{replicas: replicas, owners: make(map[uint32]*Node)}
}

func (r *hashRing) rebuild(nodes []*Node) {
	r.keys = r.keys[:0]
	r.owners = make(map[uint32]*Node, len(nodes)*r.replicas)
	for _, n := range nodes {
		for i := 0; i < r.replicas; i++ {
			h := crc32.ChecksumIEEE([]byte(n.ID + "#" + strconv.Itoa(i)))
			r.keys = append(r.keys, h)
			r.owners[h] = n
		}
	}
	sort.Slice(r.keys, func(i, j int) bool { return r.keys[i] < r.keys[j] })
}

// get 返回 key 映射到的第一个健康节点；全不健康时返回 nil。
func (r *hashRing) get(key string) *Node {
	if len(r.keys) == 0 {
		return nil
	}
	h := crc32.ChecksumIEEE([]byte(key))
	i := sort.Search(len(r.keys), func(i int) bool { return r.keys[i] >= h })
	if i == len(r.keys) {
		i = 0
	}
	for visited := 0; visited < len(r.keys); visited++ {
		n := r.owners[r.keys[i]]
		if n.Healthy() {
			return n
		}
		i = (i + 1) % len(r.keys)
	}
	return nil
}
