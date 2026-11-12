# BGP路径与ROA联合准入

路由服务器收到成员的UPDATE，攻击者可伪造AS_PATH前缀，或让末段使用AS_SET来绕开起源校验。给Go路由服务器写供导入策略调用的AS_PATH与ROA联合准入包：首个AS必须等于会话对端AS，同一AS预置最多三次，含私有或保留AS号拒绝，eBGP会话上的空AS_PATH拒绝；末段为AS_SET时按RFC 6811起源为NONE，被ROA覆盖即无效，AS0的ROA不能让任何路由有效。用合法起源、更具体前缀、AS_SET聚合和预置超限样例验证结论与原因。

## admission 包

`admission` 子包在导入策略入口处联合执行 AS_PATH 结构校验与 RPKI 起源校验：

- 首个 AS（AS_SEQUENCE 首元素）必须等于会话对端 AS，否则 `FIRST_AS_MISMATCH` 拒绝；
- eBGP 会话上的空 AS_PATH 拒绝（`EMPTY_AS_PATH_ON_EBGP`），iBGP 不适用此规则；
- 任意非联盟段（含 AS_SET 内部）出现私有/保留 AS 号拒绝（`PRIVATE_AS_IN_PATH`、`RESERVED_AS_IN_PATH`），覆盖 RFC 6996 两段私有范围、0、23456(AS_TRANS)、65535、65552-131071、4294967295；
- 同一 AS 在 AS_SEQUENCE 中连续出现超过 3 次拒绝（`PREPEND_LIMIT_EXCEEDED`），AS_SET 打断预置计数；
- ROA 起源校验按 RFC 6483 输出 `VALID`/`NOT_FOUND`/`INVALID`；末段为 AS_SET 时按 RFC 6811 起源视为 NONE，只要被任何 ROA 覆盖即 `INVALID`，无覆盖为 `NOT_FOUND`；
- AS 0 的 ROA 只能使路由失效，永远不能让路由 `VALID`。

调用方式：

```go
table := admission.NewROATable(
    admission.MustROA(6502, "192.0.2.0/24", 24),
)
decision := table.Admit(
    admission.Session{PeerAS: 6501, EBGP: true},
    "192.0.2.0/24",
    admission.Path(admission.Seq(6501, 6502)),
)
if decision.Rejected {
    // 拒绝导入，decision.Reasons / decision.Details 给出全部触发原因
}
```

`go test ./... -v` 中的样例结论：

| 样例 | 结论 | 原因 |
| --- | --- | --- |
| 合法起源 `192.0.2.0/24`，路径 `6501 6502`，ROA 授权 AS6502 | 接受，VALID | ROA 精确匹配起源 AS |
| 更具体 `192.0.2.128/25`，ROA maxLen=24 | 接受，NOT_FOUND | 前缀长度超出 maxLen，无覆盖 ROA |
| 更具体前缀被其他起源 AS 的 ROA 覆盖 | 拒绝，INVALID | 起源 AS 不在任何覆盖 ROA 中 |
| AS_SET 聚合 `6501 [6502,6503]` 且被 ROA 覆盖 | 拒绝，INVALID | RFC 6811 起源 NONE，覆盖即无效 |
| 同样 AS_SET 聚合但无 ROA 覆盖 | 接受，NOT_FOUND | 起源 NONE 且无覆盖 ROA |
| 对端 AS 连续 3 次 | 接受 | 达到预置上限但未超限 |
| 对端 AS 连续 4 次 | 拒绝，PREPEND_LIMIT_EXCEEDED | 连续出现 4 次，上限 3 次 |
| AS 0 ROA 覆盖的路由 | 拒绝，INVALID | AS 0 显式声明无合法起源 |
