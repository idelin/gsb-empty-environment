# ARP邻居可信迁移

虚拟交换机需要允许合法地址漂移，同时阻止未经授权的地址接管。实现供Go交换宿主调用的邻居状态机，绑定更新只能由可信控制面授予带递增代次的迁移票据；数据面ARP只确认票据允许的MAC，冲突进入隔离并保留证据。旧票据及迟到确认不得覆盖新绑定。以事件序列穷举测试控制面撤销、探测超时、两个MAC竞争及迁移期间数据帧去向。

## 设计

包 `netsec` 暴露一个并发安全的 `Machine`，按 IP 维护邻居条目。绑定变更只能来自可信控制面签发的 `Ticket`（含严格递增的 `Generation`）；数据面只能上报 ARP、查询转发去向，无权自行改写绑定。

### 条目状态

| 状态 | 含义 | 数据帧去向 |
| --- | --- | --- |
| `empty` | 无绑定、无票据 | 丢弃 |
| `bound` | 已确认的稳定绑定（MAC + 代次） | 投递到绑定 MAC |
| `migrating` | 新票据待 ARP 确认，旧绑定仍有效 | 继续投递到旧 MAC |
| `quarantined` | 检测到未授权接管，条目冻结 | 全部丢弃，等待控制面处置 |

### 核心不变量

- 票据代次按 IP 严格递增：任何小于等于已观测最大代次（`MaxGen`）的票据返回 `ErrStaleTicket` 并记录 `ticket_rejected` 证据，`MaxGen` 在超时、撤销、隔离后仍然保留，旧票据无法重放。
- 数据面 ARP 只能确认 `migrating` 状态下挂起票据指定的 MAC；`bound` 下来自现任 MAC 的 ARP 仅作刷新，其他 MAC 一律拒绝并将条目置入 `quarantined`，同时写入 `arp_conflict` 与 `quarantined` 证据。
- 迟到确认不能回滚绑定：迁移确认后旧 MAC 的 ARP、或迁移窗口内旧 MAC 的重宣告，都按未授权接管处理。
- 探测超时（`ProbeTimeout`）只是放弃未确认的票据：有旧绑定则回退到旧绑定，首张票据超时则回到 `empty`；失败代次依旧被消耗。
- 控制面撤销（`Revoke`）支持撤销活动绑定（清空）、撤销挂起迁移（回退旧绑定）、迁移中撤销现任绑定（取消回退目标）；隔离中的条目只能 `Release`，证据全程保留。
- `Lookup` 在 `bound`/`migrating` 时只返回当前绑定 MAC，其余状态返回 `drop`，数据帧永远不会被送往未授权 MAC。

### API

| 方法 | 调用面 | 作用 |
| --- | --- | --- |
| `Grant(Ticket)` | 控制面 | 授予迁移票据；代次必须更大 |
| `Revoke(ip, gen)` | 控制面 | 撤销指定代次的活动/挂起票据 |
| `Release(ip)` | 控制面 | 解除隔离并清空条目，保留证据 |
| `HandleArpClaim(ip, mac)` | 数据面 | 处理 ARP 宣告，确认迁移或触发隔离 |
| `ProbeTimeout(ip)` | 数据面/宿主定时器 | 宣告挂起票据探测超时 |
| `Lookup(ip)` | 数据面 | 返回 `deliver <MAC>` 或 `drop` 的转发决策 |
| `Snapshot(ip)` / `Evidence(ip)` | 控制面/审计 | 只读状态与完整事件链 |

宿主定时器如何调度由交换宿主决定：状态机不内置定时器，超时通过显式调用 `ProbeTimeout` 注入，因此所有转移都是确定、可测试的。

## 测试

```
go test -race -cover ./...
```

测试以事件序列方式穷举关键路径：

- `TestExhaustiveTransitions`：4 个状态 × 全部控制面/数据面事件的完整转移矩阵（31 行），逐行断言错误、状态、绑定、代次与审计事件。
- `TestTwoMACCompetition`：两个 MAC 竞争同一 IP，覆盖无票抢占、迁移窗口竞争、迟到确认与旧票据重放。
- `TestRevocation`：活动绑定撤销、挂起迁移撤销、迁移中撤销现任绑定、非法撤销与隔离拒绝撤销。
- `TestProbeTimeout`：首张票据超时回空、迁移超时回退旧绑定、超时后票据不可重放。
- `TestFrameForwardingMatrix`：每个状态下数据帧的精确去向。
- `TestRandomSequenceInvariants`：固定种子的 6 万步随机事件流，逐步校验代次单调、状态/挂起票据一致、转发不越权、隔离必有证据四条不变量。
- `TestConcurrentAccess`：16 个 goroutine 并发驱动全部入口，配合 `-race` 验证锁策略。
- `ExampleMachine`：宿主集成的端到端调用示例。
