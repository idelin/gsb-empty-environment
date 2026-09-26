# 限额共享信用预留（Shared-Limit Credit Reservation）

多个子账户共用父账户总额度、同时各自拥有独立额度的信用预留账本。
单进程实现：所有变更操作经同一把可重入锁串行化，满足线性化（linearizable）语义；
每个请求要么完整生效、要么完整失败，失败请求不留下任何半条分录。

- 运行环境：Python 3.11+，仅使用标准库。
- 所有金额为非负整数（如最小货币单位），示例数据均为合成数据。

## 语义

- `reserve`：同时校验子账户可用额与父账户可用额，任一不足则拒绝。
- `commit`：可少于预留额（差额自动释放回父/子可用额），不得超过预留额。
- `cancel`：释放全部预留额。
- 三个操作均携带幂等键 `key`。成功结果按 `(op, key)` 记忆，重放返回相同结果且不重复生效；
  失败请求不记录任何内容，可在额度释放后用同一键安全重试。
- 预留 ID 即其 `reserve` 的幂等键。
- 下调父额度不追溯撤销已提交金额；可用额可因此为负，新增预留将被拒绝，
  直至已提交+已预留回落到当前额度以内。

## 命令行

```
python3 credit_ledger.py < input.jsonl
```

从标准输入读取 JSON Lines（每行一个请求对象），逐行向标准输出写入一个 JSON 响应。
成功响应含 `"ok": true`；失败响应为 `{"ok": false, "error": "<code>"}`，不产生任何状态变更。

### 请求格式

| op | 字段 | 说明 |
|---|---|---|
| `init` | `parent_limit` | 设置父账户总额度（仅可在空账本上调用一次） |
| `add_child` | `child`, `limit` | 新增子账户及其独立额度 |
| `set_parent_limit` | `limit` | 调整父额度（不追溯撤销已提交金额） |
| `set_child_limit` | `child`, `limit` | 调整子账户额度 |
| `reserve` | `child`, `amount`, `key` | 预留；预留 ID 即 `key` |
| `commit` | `reservation`, `amount`, `key` | 提交，`amount <= 预留额`，差额释放 |
| `cancel` | `reservation`, `key` | 取消，释放全部预留额 |
| `state` | — | 输出父/子账户与全部预留的快照 |

### 错误码

`parent_limit_exceeded`、`child_limit_exceeded`、`exceeds_reserved`、
`unknown_reservation`、`reservation_not_active`、`unknown_child`、`child_exists`、
`invalid_amount`、`invalid_child`、`already_initialized`、`unknown_op`、`bad_request`。

### 示例（合成数据）

```
$ printf '%s\n' \
  '{"op":"init","parent_limit":10}' \
  '{"op":"add_child","child":"a","limit":100}' \
  '{"op":"add_child","child":"b","limit":100}' \
  '{"op":"reserve","child":"a","amount":6,"key":"r1"}' \
  '{"op":"reserve","child":"b","amount":6,"key":"r2"}' \
  '{"op":"commit","reservation":"r1","amount":4,"key":"c1"}' \
  '{"op":"reserve","child":"b","amount":6,"key":"r2"}' \
  '{"op":"state"}' | python3 credit_ledger.py
{"ok": true, "parent_limit": 10, "op": "init"}
{"ok": true, "child": "a", "op": "add_child"}
{"ok": true, "child": "b", "op": "add_child"}
{"ok": true, "reservation": "r1", "reserved": 6, "op": "reserve"}
{"ok": false, "error": "parent_limit_exceeded"}
{"ok": true, "reservation": "r1", "committed": 4, "released": 2, "op": "commit"}
{"ok": true, "reservation": "r2", "reserved": 6, "op": "reserve"}
{"parent": {"limit": 10, "reserved": 6, "committed": 4, "available": 0}, ...}
```

## 测试

```
python3 -m unittest -v
```

覆盖：验收边界（父额 10，两子账户并发各预留 6 至多一个成功；提交 4 释放 2 后另一方可预留 6）、
幂等重放、失败请求原子性（无半条分录、可安全重试）、父额度下调不追溯、
取消/零额提交释放、8 线程随机操作压力测试后的账本不变量、CLI 端到端。
