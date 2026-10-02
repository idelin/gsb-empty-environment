package swimtrust

// Behavior describes how a node handles protocol duties.
type Behavior int

const (
	// BehaviorHonest relays PingReqs and reports truthfully.
	BehaviorHonest Behavior = iota
	// BehaviorSilentRelay accepts PingReqs but never relays them
	// (Byzantine silent node).
	BehaviorSilentRelay
	// BehaviorForgeAlive always claims probe targets are Alive, even for
	// dead peers.
	BehaviorForgeAlive
)

func (b Behavior) String() string {
	switch b {
	case BehaviorHonest:
		return "honest"
	case BehaviorSilentRelay:
		return "silent-relay"
	case BehaviorForgeAlive:
		return "forge-alive"
	}
	return "unknown"
}

// Node is a cluster member. Each node owns a local TrustScorer.
type Node struct {
	ID       string
	Scorer   *TrustScorer
	behavior Behavior

	// up is the ground truth: whether the process is alive at all.
	up bool
	// directProbeable is the ground truth for direct Ping reachability.
	// A node can be up but partitioned from direct probes, forcing
	// observers onto the indirect (PingReq) path.
	directProbeable bool

	cluster *Cluster
}

// crash kills the node (ground truth).
func (n *Node) crash() { n.up = false }

// handlePing answers a direct Ping. Returns true if an Ack would come back.
func (n *Node) handlePing() bool {
	return n.up && n.directProbeable
}

// handlePingReq processes an indirect-probe request. The returned bool is
// whether the helper sends any relay response at all; the string is the
// claimed status of the target ("alive"/"dead").
func (n *Node) handlePingReq(target *Node) (respond bool, claim string) {
	if !n.up {
		return false, ""
	}
	switch n.behavior {
	case BehaviorSilentRelay:
		// Byzantine silence: swallow the request, never relay.
		return false, ""
	case BehaviorForgeAlive:
		return true, "alive"
	default: // honest: actually ping the target and relay the outcome
		if target.handlePing() || target.up {
			return true, "alive"
		}
		return true, "dead"
	}
}
