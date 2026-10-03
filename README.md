# memberlist：带标签与元数据属性的异构节点成员列表路由（Go 版）

仅使用 Go 标准库实现。提供：

- **异构成员列表**：节点 = 固定身份（ID/Addr）+ 标签（Labels）+ 动态元数据（Zone/Weight/Role）。
- **并发元数据更新与版本仲裁**：`Cluster.UpdateMeta` / `SetLabel` 互斥锁保护，版本号
  `(Epoch, Seq)` 单调递增；`Merge` 只接受更高版本的元数据，过期 gossip 消息自动丢弃。
- **Gossip 同步**：周期性反熵推送全量视图至随机对等节点（fanout=2），心跳 + 超时判死。
  传输层抽象为 `Transport`，内置 `UDPTransport`（部署用）与 `MemTransport`（测试用）。
- **动态拓扑子集筛选器**：选择器语法 `zone=us-east,role=worker,weight>=50,label.disk=ssd`，
  支持 `=`、`!=`、`>`、`>=`、`<`、`<=`，逗号为 AND 语义。
- **路由查询**：`Router.Query(selector)` 返回健康（Alive）且权重最高的节点；
  `Router.QueryAll(selector)` 返回按权重降序的完整子集。

## 目录结构

- `member.go` — 节点、元数据、状态定义
- `version.go` — 逻辑版本号与比较
- `cluster.go` — 成员列表：并发更新、版本仲裁合并、心跳判死
- `gossip.go` — Gossip 同步器、UDP/内存传输
- `filter.go` — 属性选择器解析与匹配
- `router.go` — 路由查询接口
- `cmd/demo/main.go` — 运行样例

## 运行样例

```sh
go run ./cmd/demo          # 进程内内存传输
go run ./cmd/demo -udp     # UDP 传输（127.0.0.1:7001-7003）
```

输出：

```
[ok] 成员列表收敛（每节点可见 3 个成员）
[route] 初始查询 zone=us-east -> n1 (weight=50)
[update] n2 负载权重 10 -> 80
[ok] gossip 同步：n1、n3 观测到 n2.weight=80
[route] 更新后查询 zone=us-east -> n2 (weight=80, zone=us-east, role=worker)
[route] 组合筛选 zone=us-east,weight>=60,label.disk=nvme -> n2
```

## 测试

```sh
go test ./...        # 单元 + 验收测试
go test -race ./...  # 竞态检测
```

验收测试 `TestAcceptanceWeightUpdateGossipAndRoute`（gossip_test.go）完整覆盖验收边界：
节点权重 10 -> 80 动态更新，经 Gossip 同步到全集群后，
`Query("zone=us-east")` 返回最新健康且权重最高的节点。
