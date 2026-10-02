package discovery

import (
	"math/rand"
	"sync"
)

// Ring 是 Gossip 洗牌环：维护一份随机打乱的对等节点列表，
// 每轮按序取 fanout 个目标，走完整环后重新洗牌，保证长期均匀覆盖。
type Ring struct {
	mu    sync.Mutex
	peers []string
	pos   int
	rnd   *rand.Rand
}

func NewRing() *Ring {
	return &Ring{rnd: rand.New(rand.NewSource(rand.Int63()))}
}

// Reset 用最新的对等地址重建并重新洗牌。
func (r *Ring) Reset(peers []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.peers = append([]string(nil), peers...)
	r.rnd.Shuffle(len(r.peers), func(i, j int) {
		r.peers[i], r.peers[j] = r.peers[j], r.peers[i]
	})
	r.pos = 0
}

// Next 取出本轮要 gossip 的 k 个目标；环耗尽时自动重洗。
func (r *Ring) Next(k int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.peers) == 0 {
		return nil
	}
	if r.pos >= len(r.peers) {
		r.rnd.Shuffle(len(r.peers), func(i, j int) {
			r.peers[i], r.peers[j] = r.peers[j], r.peers[i]
		})
		r.pos = 0
	}
	end := r.pos + k
	if end > len(r.peers) {
		end = len(r.peers)
	}
	out := append([]string(nil), r.peers[r.pos:end]...)
	r.pos = end
	return out
}

// Size 返回环内对等节点数。
func (r *Ring) Size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.peers)
}
