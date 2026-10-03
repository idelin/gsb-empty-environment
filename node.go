// Package noderouter implements a heterogeneous node membership list router
// with labeled, versioned metadata, gossip-based dissemination and
// attribute-based topology subset filtering.
package noderouter

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Role describes the functional role of a node in the cluster.
type Role string

const (
	RoleWorker   Role = "worker"
	RoleMaster   Role = "master"
	RoleObserver Role = "observer"
)

// Metadata holds the dynamic, gossiped attributes of a node.
type Metadata struct {
	Zone    string // rack / availability zone, e.g. "us-east"
	Weight  int    // load weight, higher means more preferred
	Role    Role   // node role
	Healthy bool   // liveness flag maintained by the failure detector
}

// NodeState is the versioned record of a single cluster member.
// Version and UpdatedAt form the arbitration clock: an incoming state
// replaces the local one only when it is strictly newer.
type NodeState struct {
	ID        string
	Addr      string
	Labels    map[string]string // static, user-defined labels
	Meta      Metadata
	Version   uint64    // monotonically increasing per node, set by the owner
	UpdatedAt time.Time // wall clock of the owning node at update time
}

// Clone returns a deep copy of the state.
func (s NodeState) Clone() NodeState {
	out := s
	if s.Labels != nil {
		out.Labels = make(map[string]string, len(s.Labels))
		for k, v := range s.Labels {
			out.Labels[k] = v
		}
	}
	return out
}

// supersedes reports whether s should replace other under version arbitration.
// Higher Version wins; ties are broken by UpdatedAt, then by node ID to keep
// the comparison total and deterministic on every replica.
func (s NodeState) supersedes(other NodeState) bool {
	if s.Version != other.Version {
		return s.Version > other.Version
	}
	if !s.UpdatedAt.Equal(other.UpdatedAt) {
		return s.UpdatedAt.After(other.UpdatedAt)
	}
	return s.ID > other.ID
}

// Matches reports whether the node satisfies a parsed selector.
func (s NodeState) Matches(sel Selector) bool {
	for _, req := range sel {
		var val string
		switch req.Key {
		case "zone":
			val = s.Meta.Zone
		case "role":
			val = string(s.Meta.Role)
		case "healthy":
			val = fmt.Sprintf("%t", s.Meta.Healthy)
		default:
			v, ok := s.Labels[req.Key]
			if !ok {
				return false
			}
			val = v
		}
		if val != req.Value {
			return false
		}
	}
	return true
}

func (s NodeState) String() string {
	keys := make([]string, 0, len(s.Labels))
	for k := range s.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%s", k, s.Labels[k])
	}
	return fmt.Sprintf("Node{id:%s addr:%s zone:%s role:%s weight:%d healthy:%t v:%d labels:{%s}}",
		s.ID, s.Addr, s.Meta.Zone, s.Meta.Role, s.Meta.Weight, s.Meta.Healthy, s.Version, b.String())
}
