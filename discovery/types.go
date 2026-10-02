// Package discovery 实现基于种子节点（Seed Nodes）的新节点冷启动自发现。
//
// 角色与流程：
//   - Seed：静态配置的种子节点，持有权威成员表，应答 Bootstrap 握手。
//   - Node：新节点启动时向 Seed 列表发起 Bootstrap 握手，拉取初始成员快照，
//     随后进入 Gossip 洗牌环，周期性随机选取若干对等节点交换成员摘要并逐步收敛。
package discovery

// NodeMeta 描述一个集群成员的元数据。
type NodeMeta struct {
	ID        string `json:"id"`
	Addr      string `json:"addr"`
	Heartbeat int64  `json:"heartbeat"` // 最近一次心跳，UnixNano
}

// Bootstrap 握手协议。
type bootstrapRequest struct {
	Meta NodeMeta `json:"meta"`
}

type bootstrapResponse struct {
	Members []NodeMeta `json:"members"`
}

// Gossip 反熵交换协议（双向合并）。
type gossipRequest struct {
	Members []NodeMeta `json:"members"`
}

type gossipResponse struct {
	Members []NodeMeta `json:"members"`
}
