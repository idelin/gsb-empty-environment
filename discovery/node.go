package discovery

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Node 是集群中的常规节点：冷启动时通过 Seed 拉取成员快照，
// 之后依靠 Gossip 洗牌环持续交换成员信息。
type Node struct {
	meta  NodeMeta
	seeds []string

	store  *Store
	ring   *Ring
	client *http.Client

	gossipInterval time.Duration
	fanout         int

	srv *http.Server

	stopOnce sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup
}

// Option 调整 Node 行为。
type Option func(*Node)

// WithGossipInterval 设置 Gossip 周期。
func WithGossipInterval(d time.Duration) Option {
	return func(n *Node) { n.gossipInterval = d }
}

// WithFanout 设置每轮 Gossip 的对等节点数量。
func WithFanout(k int) Option {
	return func(n *Node) { n.fanout = k }
}

// NewNode 创建一个尚未入网的新节点，seeds 为静态配置的 Seed 地址列表。
func NewNode(id string, seeds []string, opts ...Option) *Node {
	n := &Node{
		meta:           NodeMeta{ID: id},
		seeds:          append([]string(nil), seeds...),
		store:          NewStore(),
		ring:           NewRing(),
		client:         defaultClient(),
		gossipInterval: 100 * time.Millisecond,
		fanout:         3,
		stop:           make(chan struct{}),
	}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

// ID 返回节点标识。
func (n *Node) ID() string { return n.meta.ID }

// Addr 返回节点入网后的实际服务地址（Bootstrap 前为空）。
func (n *Node) Addr() string { return n.meta.Addr }

// Members 返回节点当前已知的成员快照。
func (n *Node) Members() []NodeMeta { return n.store.Snapshot() }

// MemberCount 返回已知成员数。
func (n *Node) MemberCount() int { return n.store.Size() }

// Bootstrap 执行冷启动入网：
//  1. 启动本地 Gossip 端点；
//  2. 依次向 Seed 列表发起握手，登记自身并拉取初始成员快照；
//  3. 构建洗牌环并启动 Gossip 主循环，融入常规格网。
func (n *Node) Bootstrap(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/gossip", n.handleGossip)
	srv, addr, err := serve(mux)
	if err != nil {
		return fmt.Errorf("node %s: listen: %v", n.meta.ID, err)
	}
	n.srv = srv
	n.meta.Addr = addr
	n.touch()

	var lastErr error
	joined := false
	for _, seed := range n.seeds {
		req := bootstrapRequest{Meta: n.meta}
		var resp bootstrapResponse
		if err := postJSON(ctx, n.client, "http://"+seed+"/bootstrap", &req, &resp); err != nil {
			lastErr = err
			continue
		}
		n.store.Merge(resp.Members)
		joined = true
	}
	if !joined {
		n.srv.Shutdown(context.Background())
		if lastErr == nil {
			lastErr = fmt.Errorf("no seed configured")
		}
		return fmt.Errorf("node %s: bootstrap failed: %v", n.meta.ID, lastErr)
	}

	n.rebuildRing()
	n.wg.Add(1)
	go n.gossipLoop()
	return nil
}

// Close 停止 Gossip 循环并关闭服务端点。
func (n *Node) Close() {
	n.stopOnce.Do(func() { close(n.stop) })
	n.wg.Wait()
	if n.srv != nil {
		n.srv.Shutdown(context.Background())
	}
}

func (n *Node) touch() {
	n.meta.Heartbeat = time.Now().UnixNano()
	n.store.Upsert(n.meta)
}

// rebuildRing 用当前成员表（排除自身与 Seed 之外的地址也保留，Seed 同样参与交换）重建洗牌环。
func (n *Node) rebuildRing() {
	snap := n.store.Snapshot()
	peers := make([]string, 0, len(snap))
	for _, m := range snap {
		if m.ID != n.meta.ID && m.Addr != "" {
			peers = append(peers, m.Addr)
		}
	}
	n.ring.Reset(peers)
}

// gossipLoop 周期性沿洗牌环选取对等节点做反熵交换。
func (n *Node) gossipLoop() {
	defer n.wg.Done()
	ticker := time.NewTicker(n.gossipInterval)
	defer ticker.Stop()
	for {
		select {
		case <-n.stop:
			return
		case <-ticker.C:
			n.gossipRound()
		}
	}
}

func (n *Node) gossipRound() {
	n.touch()
	targets := n.ring.Next(n.fanout)
	if len(targets) == 0 {
		return
	}
	req := gossipRequest{Members: n.store.Snapshot()}
	changed := false
	for _, addr := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), n.gossipInterval)
		var resp gossipResponse
		if err := postJSON(ctx, n.client, "http://"+addr+"/gossip", &req, &resp); err == nil {
			if n.store.Merge(resp.Members) > 0 {
				changed = true
			}
		}
		cancel()
	}
	if changed {
		n.rebuildRing()
	}
}

// handleGossip 接收对等节点的成员摘要，合并后回传本地快照（双向收敛）。
func (n *Node) handleGossip(w http.ResponseWriter, r *http.Request) {
	var req gossipRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if n.store.Merge(req.Members) > 0 {
		n.rebuildRing()
	}
	writeJSON(w, gossipResponse{Members: n.store.Snapshot()})
}
