package discovery

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Config controls a Node.
type Config struct {
	// ID uniquely identifies the node. Defaults to the bound address.
	ID string
	// Addr is the TCP listen address, e.g. "127.0.0.1:0".
	Addr string
	// Seeds is the static seed list dialed during the bootstrap
	// handshake. A node with no seeds acts as a seed itself.
	Seeds []string
	// GossipInterval is the period of the shuffle ring.
	GossipInterval time.Duration
	// BootstrapRetryInterval is how often an orphan node (one whose
	// initial bootstrap found no reachable seed) retries the
	// handshake in the background.
	BootstrapRetryInterval time.Duration
	// SampleSize caps how many member entries travel in each gossip
	// exchange.
	SampleSize int
	// DialTimeout bounds outbound handshake and gossip RPCs.
	DialTimeout time.Duration
	// Listen binds the server socket. Defaults to TCP. Tests and
	// demos can inject MemNetwork.Listen instead.
	Listen func(addr string) (net.Listener, error)
	// Dial opens a client connection. Defaults to TCP. Tests and
	// demos can inject MemNetwork.Dial instead.
	Dial func(addr string, timeout time.Duration) (net.Conn, error)
}

func (c Config) withDefaults() Config {
	if c.Addr == "" {
		c.Addr = "127.0.0.1:0"
	}
	if c.GossipInterval <= 0 {
		c.GossipInterval = 200 * time.Millisecond
	}
	if c.BootstrapRetryInterval <= 0 {
		c.BootstrapRetryInterval = 300 * time.Millisecond
	}
	if c.SampleSize <= 0 {
		c.SampleSize = 16
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = 2 * time.Second
	}
	if c.Listen == nil {
		c.Listen = func(addr string) (net.Listener, error) {
			return net.Listen("tcp", addr)
		}
	}
	if c.Dial == nil {
		c.Dial = func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("tcp", addr, timeout)
		}
	}
	return c
}

// Node is a cluster member able to cold-start from a static seed
// list and then take part in the gossip shuffle ring.
type Node struct {
	cfg   Config
	table *Table

	ln   net.Listener
	done chan struct{}

	stopOnce     sync.Once
	wg           sync.WaitGroup
	gossipRounds uint64 // successful gossip exchanges
	bootstrapped int32  // 1 once a seed handshake succeeded
}

// NewNode builds a node from the given config.
func NewNode(cfg Config) *Node {
	return &Node{
		cfg:  cfg.withDefaults(),
		done: make(chan struct{}),
	}
}

// Start binds the listener, runs the bootstrap handshake against the
// configured seed list and joins the gossip shuffle ring. The
// bootstrap handshake is synchronous: when Start returns, the node
// has either pulled a full member snapshot from a seed or, if no
// seed was reachable, scheduled background retries (orphan mode).
func (n *Node) Start() error {
	ln, err := n.cfg.Listen(n.cfg.Addr)
	if err != nil {
		return err
	}
	n.ln = ln

	id := n.cfg.ID
	if id == "" {
		id = ln.Addr().String()
	}
	n.table = NewTable(NodeMeta{
		ID:          id,
		Addr:        ln.Addr().String(),
		Incarnation: 1,
		LastSeen:    time.Now().UnixNano(),
	})

	n.wg.Add(1)
	go n.serve()

	// Cold-start handshake. Orphan nodes (no reachable seed yet)
	// keep retrying in the background until a seed appears.
	if err := n.Bootstrap(); err != nil {
		n.wg.Add(1)
		go n.bootstrapRetryLoop()
	}

	n.wg.Add(1)
	go n.gossipLoop()
	return nil
}

// Stop shuts the node down and waits for its goroutines.
func (n *Node) Stop() {
	n.stopOnce.Do(func() {
		close(n.done)
		if n.ln != nil {
			n.ln.Close()
		}
	})
	n.wg.Wait()
}

// ID returns the node identifier.
func (n *Node) ID() string { return n.table.Self().ID }

// Addr returns the bound listen address.
func (n *Node) Addr() string { return n.table.Self().Addr }

// Members returns the current membership view.
func (n *Node) Members() []NodeMeta { return n.table.Snapshot() }

// MemberCount returns the number of known members, self included.
func (n *Node) MemberCount() int { return n.table.Size() }

// Knows reports whether the given member ID is in the local view.
func (n *Node) Knows(id string) bool { return n.table.Has(id) }

// GossipRounds counts successful gossip exchanges so far.
func (n *Node) GossipRounds() uint64 { return atomic.LoadUint64(&n.gossipRounds) }

// Bootstrapped reports whether a seed handshake has succeeded.
func (n *Node) Bootstrapped() bool { return atomic.LoadInt32(&n.bootstrapped) == 1 }

// Bootstrap performs the cold-start handshake against the static
// seed list: it registers the local node with every reachable seed
// and merges the returned membership snapshots. It succeeds as soon
// as one seed answers. A node without seeds is itself a seed and
// succeeds trivially.
func (n *Node) Bootstrap() error {
	self := n.table.Self()
	var errs []string
	joined := false
	for _, seed := range n.cfg.Seeds {
		if seed == self.Addr {
			continue
		}
		resp, err := n.rpc(seed, Envelope{
			Type:    MsgBootstrap,
			From:    self,
			Members: n.table.Snapshot(),
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", seed, err))
			continue
		}
		n.table.Merge(resp.Members)
		joined = true
	}
	if joined || len(errs) == 0 {
		atomic.StoreInt32(&n.bootstrapped, 1)
		return nil
	}
	return fmt.Errorf("bootstrap: no reachable seed (%s)", strings.Join(errs, "; "))
}

// bootstrapRetryLoop lets an orphan node join as soon as a seed
// becomes reachable.
func (n *Node) bootstrapRetryLoop() {
	defer n.wg.Done()
	ticker := time.NewTicker(n.cfg.BootstrapRetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-n.done:
			return
		case <-ticker.C:
			if n.Bootstrapped() {
				return
			}
			n.Bootstrap()
		}
	}
}

// gossipLoop is the shuffle ring: every interval the node exchanges
// a random member sample with a random peer (push-pull).
func (n *Node) gossipLoop() {
	defer n.wg.Done()
	ticker := time.NewTicker(n.cfg.GossipInterval)
	defer ticker.Stop()
	for {
		select {
		case <-n.done:
			return
		case <-ticker.C:
			n.gossipOnce()
		}
	}
}

func (n *Node) gossipOnce() {
	peer, ok := n.table.RandomPeer()
	if !ok {
		return
	}
	resp, err := n.rpc(peer.Addr, Envelope{
		Type:    MsgGossip,
		From:    n.table.Self(),
		Members: n.table.Sample(n.cfg.SampleSize),
	})
	if err != nil {
		return // peer may be down; the ring retries next round
	}
	n.table.Merge(resp.Members)
	atomic.AddUint64(&n.gossipRounds, 1)
}

// serve accepts handshake and gossip connections until shutdown.
func (n *Node) serve() {
	defer n.wg.Done()
	for {
		conn, err := n.ln.Accept()
		if err != nil {
			return // listener closed
		}
		n.wg.Add(1)
		go n.handleConn(conn)
	}
}

func (n *Node) handleConn(conn net.Conn) {
	defer n.wg.Done()
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * n.cfg.DialTimeout))

	var env Envelope
	if err := json.NewDecoder(conn).Decode(&env); err != nil {
		return
	}
	// Every exchange registers the sender and merges its view.
	n.table.Merge([]NodeMeta{env.From})
	n.table.Merge(env.Members)

	var resp Response
	switch env.Type {
	case MsgBootstrap:
		// Cold-start handshake: answer with the full snapshot so the
		// newcomer can join the mesh in a single round trip.
		resp.Members = n.table.Snapshot()
	case MsgGossip:
		resp.Members = n.table.Sample(n.cfg.SampleSize)
	default:
		return
	}
	json.NewEncoder(conn).Encode(resp)
}

// rpc performs a single request/response exchange with a peer.
func (n *Node) rpc(addr string, env Envelope) (*Response, error) {
	conn, err := n.cfg.Dial(addr, n.cfg.DialTimeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * n.cfg.DialTimeout))
	if err := json.NewEncoder(conn).Encode(env); err != nil {
		return nil, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
