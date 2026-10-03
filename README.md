# noderouter

带标签与元数据属性的异构节点成员列表路由（Go，仅标准库）。

## 功能

- **异构节点成员列表**：每个节点携带静态标签（`Labels`）与动态元数据
  （`Zone` 机房、`Weight` 负载权重、`Role` 角色、`Healthy` 健康位）。
- **并发元数据更新 + 版本号仲裁**：本地更新通过 `MemberList.UpdateLocal`
  串行化并单调递增版本号；副本间合并按 `(Version, UpdatedAt, ID)` 全序仲裁，
  过期状态一律丢弃，保证最终一致且确定性收敛。
- **Gossip 同步**：`Cluster` 模拟 push 式流行病广播，每轮每节点向
  fanout 个随机对等体推送全量状态视图，`Converge` 运行至全网无新状态。
- **属性子集筛选与路由**：选择器表达式如 `zone=us-east,role=worker,disk=ssd`，
  保留字 `zone/role/healthy` 匹配元数据，其余键匹配标签；
  `Router.Route` 返回健康且权重最高的节点，`Router.Subset` 返回按权重降序的子集。

## 结构

| 文件 | 职责 |
|---|---|
| `node.go` | 节点状态、元数据、版本仲裁比较 |
| `memberlist.go` | 并发安全的成员列表（本地副本） |
| `gossip.go` | 进程内 gossip 传输与收敛驱动 |
| `filter.go` | 选择器解析 |
| `router.go` | 拓扑子集筛选与最优节点路由 |
| `cmd/demo` | 验收场景可运行示例 |

## 运行

```sh
go test ./... -v   # 测试（含 -race 通过）
go run ./cmd/demo  # 示例：权重 10->80 经 gossip 同步后按 zone=us-east 路由
```
