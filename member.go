package memberlist

import "time"

// Status 表示成员的健康状态。
type Status int

const (
	StatusAlive Status = iota
	StatusDead
)

func (s Status) String() string {
	if s == StatusAlive {
		return "alive"
	}
	return "dead"
}

// Meta 是可动态更新的节点元数据。
type Meta struct {
	Zone   string `json:"zone"`   // 机房 / 可用区
	Role   string `json:"role"`   // 角色，如 gateway / worker
	Weight int    `json:"weight"` // 负载权重
}

// Node 是集群中的一个异构成员：固定身份 + 标签 + 带版本的动态元数据。
type Node struct {
	ID        string            `json:"id"`
	Addr      string            `json:"addr"`
	Labels    map[string]string `json:"labels"`
	Meta      Meta              `json:"meta"`
	Version   Version           `json:"version"`   // 元数据版本（仅属主节点递增）
	Heartbeat uint64            `json:"heartbeat"` // 活性心跳（仅属主节点递增）
	Status    Status            `json:"status"`
	LastSeen  time.Time         `json:"-"` // 本地观测时间，不参与 gossip
}

// Clone 深拷贝节点，避免共享 Labels map 造成数据竞争。
func (n Node) Clone() Node {
	out := n
	if n.Labels != nil {
		out.Labels = make(map[string]string, len(n.Labels))
		for k, v := range n.Labels {
			out.Labels[k] = v
		}
	}
	return out
}
