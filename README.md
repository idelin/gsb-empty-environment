# ARP邻居可信迁移

虚拟交换机需要允许合法地址漂移，同时阻止未经授权的地址接管。实现供Go交换宿主调用的邻居状态机，绑定更新只能由可信控制面授予带递增代次的迁移票据；数据面ARP只确认票据允许的MAC，冲突进入隔离并保留证据。旧票据及迟到确认不得覆盖新绑定。以事件序列穷举测试控制面撤销、探测超时、两个MAC竞争及迁移期间数据帧去向。

## 设计

包 `netsec` 暴露一个线程安全的 `Switch`，控制面与数据面可并发调用。

- **迁移票据（控制面）**：`Grant(Ticket)` 安装授权，`Ticket.Generation` 对同一 IP 必须严格递增；相等或更旧的代次返回 `ErrStaleTicket` 且不改变任何状态（旧票据不能覆盖新绑定）。`Revoke` 与 TTL 到期撤销授权。
- **ARP 确认（数据面）**：`ObserveARP` 仅接受当前票据授权 MAC 的确认并安装/刷新绑定；无票据或 MAC 不符的 ARP 判定为接管，邻居进入 `quarantined` 并写入证据。旧代 MAC 在新票据之后的“迟到确认”归类为 `late_confirm`，同样被拒绝、不覆盖新绑定。
- **隔离与证据**：`quarantined` 下所有数据帧 `drop`，数据面无法自行解除，只能由控制面用更新代次票据解除；证据按每 IP 环形缓冲保留，撤销/重新授权不清空，可用 `DrainEvidence` 取证。
- **邻居状态**：`empty → incomplete → reachable → stale → probe`，外加 `quarantined`。探测由宿主时钟 `Tick` 驱动，`MaxProbes` 次无应答判定解析失败或撤销陈旧绑定。
- **帧去向**：`Forward` 对 `reachable` 单播到绑定端口，`quarantined` 丢弃，其余（未确认/陈旧/探测中/未知）泛洪。迁移窗口内新票据已到、新 MAC 未确认前，流量仍走旧绑定端口。

## 主要 API

| 调用 | 面 | 作用 |
| --- | --- | --- |
| `Grant(Ticket)` / `Revoke(ip)` | 控制面 | 授予/撤销带代次的迁移票据 |
| `ObserveARP(ip, mac, port)` | 数据面 | 上报 ARP 声明，返回拒绝原因 `Reason` |
| `Forward(ip)` | 数据面 | 返回 `forward`/`flood`/`drop` 及出端口 |
| `StartResolution` / `Revalidate` / `Tick` | 宿主 | 驱动解析、重探测与超时 |
| `Snapshot` / `Evidence` / `Events` | 审计 | 状态、取证与事件流 |

## 测试

`go test ./... -race`。`TestEventPermutationExhaustive` 对 7 类事件（三次授权、一次旧代票据、两种 MAC 的 ARP、时钟超时）做 7! = 5040 个全排列，在每个前缀上用独立 oracle 重放授权规则并断言安全不变量；其余用例覆盖撤销、票据 TTL、探测超时、双 MAC 竞争、迁移期间帧去向、证据环与并发。
