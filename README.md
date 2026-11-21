# DHCPv6 IA_PD 委派绑定包（`pdbind`）

模块 `prescreen.local/ps-739f741a69`，仅依赖 Go 标准库（`net/netip` 等）。
`pdbind` 是供 DHCPv6 服务端报文处理层调用的前缀委派（IA_PD）绑定子系统，
聚焦防伪造、防前缀抢占与路由表一致性。

## 安全保证

- 绑定键为客户端 DUID + IAID（`MakeKey`），不同 DUID 或不同 IAID 的委派互不影响。
- Solicit/Request 中的前缀提示仅当“在地址池内、长度对齐、未被任何现存或本次
  暂存绑定占用”时才采纳；否则忽略提示，从池中分配第一个空闲前缀，池满返回
  `StatusNoPrefixAvail`。客户端无法用提示抢占他人前缀。
- Renew / Rebind / Release 携带的前缀必须与该 DUID+IAID 的现存绑定完全一致
  （中继场景还核对链路地址），否则该 IA_PD 返回 `StatusNoBinding` 且不改动任何状态。
- 中继消息按 relay-forward 链校验：嵌套深度不超过 `MaxRelayDepth`（默认 32，
  解析阶段即硬性截断），逐跳校验 peer 地址，并核对最内层 relay 的 link-address
  是否为合法且落在 `AllowedLinks` 内的地址。
- 路由与绑定变更采用两阶段提交：`Process` 只生成暂存 `Decision`，调用方确认
  Reply 可发送后调用 `Decision.Commit` 才安装路由；`Abort` 丢弃全部变更。
  Release 提交后撤销路由并删除绑定。
- 租约到期后绑定惰性失效并可由 `SweepExpired` 统一清理，清理时撤销路由、回收前缀。

## 报文处理集成方式

1. `ParseMessage` 从 UDP 载荷解析出 `ClientMessage` 与 `[]RelayHop`，深度超限
   直接返回 `ErrRelayDepth`，不会进入后续处理。
2. `Manager.Process(msg, hops)` 做绑定/前缀/中继校验，返回 `Decision`。
3. 用 `Decision.Status` 与 `Decision.IAPDs` 组装 Reply。
4. Reply 确认发送后 `Decision.Commit()`；发送失败则 `Decision.Abort()`。

```go
rt := pdbind.NewMemoryRouteTable()
mgr := pdbind.NewManager(pdbind.Config{
    Pool:          pdbind.PrefixPool{CIDR: netip.MustParsePrefix("2001:db8:1::/48"), DelegatedLen: 50},
    AllowedLinks:  []netip.Prefix{netip.MustParsePrefix("2001:db8:ffff::/48")},
    MaxRelayDepth: pdbind.DefaultMaxRelayDepth,
}, rt)

msg, hops, err := pdbind.ParseMessage(packet, srcAddr, pdbind.DefaultMaxRelayDepth)
if err != nil { /* 丢弃报文，记录错误 */ }
decision, err := mgr.Process(msg, hops)
if err != nil { /* 中继/报文级错误 */ }
// 用 decision.Status / decision.IAPDs 构造 Reply …
if replySent {
    _ = decision.Commit() // 此刻才安装/撤销路由
} else {
    decision.Abort()
}
```

真实路由器通过实现 `RouteTable` 接口（`Install` / `Remove`）对接系统 FIB；
测试使用 `MemoryRouteTable`，可断言路由内容与操作历史。

## 文件结构

- `pdbind/types.go`：消息类型、状态码、绑定/键/配置等领域类型。
- `pdbind/pool.go`：前缀池切分、槽位索引与 128 位整数移位运算。
- `pdbind/manager.go`：绑定管理器与两阶段 `Decision` 提交。
- `pdbind/handlers.go`：中继校验及 Solicit/Renew/Rebind/Release/Confirm 处理。
- `pdbind/route.go`：`RouteTable` 接口与并发安全的内存实现。
- `pdbind/wire.go`：DHCPv6 报文、relay-forward 链、IA_PD/IAPREFIX 选项解析。

## 验证

`go test ./...` 覆盖题目要求的四类路由表验证场景：

- 提示冲突：占用前缀作为提示被忽略，池内空闲提示才采纳。
- 他人 Release/Renew/Rebind：返回 NoBinding，受害者路由与绑定不变。
- 深度超限：33 跳 relay 链在解析与处理两层均被拒绝，并覆盖链路地址校验。
- 租约到期：过期后 Renew/Release 为 NoBinding，清理撤销路由并可重新委派。
