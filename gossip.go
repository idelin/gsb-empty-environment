package memberlist

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"
)

// Transport 抽象 gossip 报文传输，便于测试（内存实现）与部署（UDP 实现）。
type Transport interface {
	// Addr 返回本传输层地址。
	Addr() string
	// SendTo 向目标地址发送一个报文。
	SendTo(addr string, payload []byte) error
	// SetHandler 注册报文处理回调（在独立 goroutine 中调用）。
	SetHandler(func(payload []byte))
	// Close 释放资源。
	Close() error
}

// Gossiper 周期性把本地成员视图推送给随机对等节点，实现反熵同步。
type Gossiper struct {
	cluster     *Cluster
	tr          Transport
	interval    time.Duration
	deadTimeout time.Duration
	fanout      int

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewGossiper 创建 gossip 同步器。interval 为同步周期，deadTimeout 为心跳超时判死时间。
func NewGossiper(c *Cluster, tr Transport, interval, deadTimeout time.Duration) *Gossiper {
	g := &Gossiper{
		cluster:     c,
		tr:          tr,
		interval:    interval,
		deadTimeout: deadTimeout,
		fanout:      2,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}
	tr.SetHandler(func(payload []byte) {
		var view []Node
		if err := json.Unmarshal(payload, &view); err != nil {
			return
		}
		c.Merge(view)
	})
	return g
}

// Start 启动后台 gossip 循环。
func (g *Gossiper) Start() {
	go g.loop()
}

// Stop 停止 gossip 循环。
func (g *Gossiper) Stop() {
	close(g.stopCh)
	<-g.doneCh
}

// Join 通过种子地址引导加入集群：把本地视图推送给种子节点，
// 之后依靠周期性 gossip 完成双向收敛。
func (g *Gossiper) Join(seedAddr string) error {
	payload, err := json.Marshal(g.cluster.Snapshot())
	if err != nil {
		return err
	}
	return g.tr.SendTo(seedAddr, payload)
}

func (g *Gossiper) loop() {
	defer close(g.doneCh)
	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()
	for {
		select {
		case <-g.stopCh:
			return
		case <-ticker.C:
			g.round()
		}
	}
}

// round 执行一轮：心跳 + 判死清扫 + 向随机对等节点推送全量视图。
func (g *Gossiper) round() {
	g.cluster.Heartbeat()
	g.cluster.SweepDead(g.deadTimeout)

	snap := g.cluster.Snapshot()
	payload, err := json.Marshal(snap)
	if err != nil {
		return
	}
	selfAddr := g.tr.Addr()
	var peers []string
	for _, n := range snap {
		if n.Addr != "" && n.Addr != selfAddr {
			peers = append(peers, n.Addr)
		}
	}
	rand.Shuffle(len(peers), func(i, j int) { peers[i], peers[j] = peers[j], peers[i] })
	if len(peers) > g.fanout {
		peers = peers[:g.fanout]
	}
	for _, p := range peers {
		g.tr.SendTo(p, payload) // 尽力投递，忽略单次失败
	}
}

// ---------- UDP 传输（生产/演示用） ----------

// UDPTransport 基于 UDP 的 Transport 实现。
type UDPTransport struct {
	conn    *net.UDPConn
	handler func([]byte)
	doneCh  chan struct{}
}

// NewUDPTransport 在 addr（如 "127.0.0.1:7001"）上监听 UDP。
func NewUDPTransport(addr string) (*UDPTransport, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, err
	}
	t := &UDPTransport{conn: conn, doneCh: make(chan struct{})}
	go t.readLoop()
	return t, nil
}

func (t *UDPTransport) readLoop() {
	defer close(t.doneCh)
	buf := make([]byte, 1<<16)
	for {
		n, err := t.conn.Read(buf)
		if err != nil {
			return
		}
		if t.handler != nil {
			cp := make([]byte, n)
			copy(cp, buf[:n])
			go t.handler(cp)
		}
	}
}

// Addr 实现 Transport。
func (t *UDPTransport) Addr() string { return t.conn.LocalAddr().String() }

// SendTo 实现 Transport。
func (t *UDPTransport) SendTo(addr string, payload []byte) error {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	_, err = t.conn.WriteTo(payload, ua)
	return err
}

// SetHandler 实现 Transport。
func (t *UDPTransport) SetHandler(h func([]byte)) { t.handler = h }

// Close 实现 Transport。
func (t *UDPTransport) Close() error { return t.conn.Close() }

// ---------- 内存传输（测试用） ----------

// MemNetwork 是一个进程内虚拟网络，多个 MemTransport 通过它互连。
type MemNetwork struct {
	mu    sync.RWMutex
	peers map[string]*MemTransport
}

// NewMemNetwork 创建虚拟网络。
func NewMemNetwork() *MemNetwork {
	return &MemNetwork{peers: make(map[string]*MemTransport)}
}

// MemTransport 进程内 Transport 实现。
type MemTransport struct {
	net     *MemNetwork
	addr    string
	handler func([]byte)
}

// NewMemTransport 在虚拟网络上注册一个地址。
func (n *MemNetwork) NewMemTransport(addr string) (*MemTransport, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, dup := n.peers[addr]; dup {
		return nil, fmt.Errorf("memberlist: duplicate mem addr %q", addr)
	}
	t := &MemTransport{net: n, addr: addr}
	n.peers[addr] = t
	return t, nil
}

// Addr 实现 Transport。
func (t *MemTransport) Addr() string { return t.addr }

// SendTo 实现 Transport。
func (t *MemTransport) SendTo(addr string, payload []byte) error {
	t.net.mu.RLock()
	peer, ok := t.net.peers[addr]
	t.net.mu.RUnlock()
	if !ok {
		return fmt.Errorf("memberlist: unknown mem addr %q", addr)
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	go peer.handler(cp)
	return nil
}

// SetHandler 实现 Transport。
func (t *MemTransport) SetHandler(h func([]byte)) { t.handler = h }

// Close 实现 Transport。
func (t *MemTransport) Close() error {
	t.net.mu.Lock()
	defer t.net.mu.Unlock()
	delete(t.net.peers, t.addr)
	return nil
}
