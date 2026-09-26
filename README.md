# 限额共享信用预留（Shared-Limit Credit Reservation）

多个子账户共用父账户总额度，同时各自拥有子额度。提供预留（reserve）、提交（commit）、
取消（cancel）三类幂等操作，单进程内可线性化（linearizable）。纯 Python 3.11 标准库实现，
无第三方依赖。所有示例数据均为合成数据。

## 模型与规则

- 父占用 = 父已提交金额 + 全部活跃预留；父可用 = `parent_limit - 父占用`。
- 子占用 = 子已提交金额 + 该子活跃预留；子可用同理。预留需同时满足父、子可用额。
- `commit` 金额可少于预留（差额自动释放），不得超过预留，否则整体失败。
- `set_parent_limit` 下调**不追溯撤销**已提交金额与既有预留；占用可暂时高于额度，
  期间只减不增，新增预留按当前可用额拒绝。
- 幂等：`reserve`/`commit`/`cancel` 均携带幂等键 `key`。同一键重放返回首次执行的结果
  （包括失败结果），不产生重复分录。预留 ID 由键派生：`rsv_<key>`。
- 原子性：每个操作在单把可重入锁内完成“校验 + 生效”；校验失败时不修改任何状态，
  失败请求不会留下半条分录。
- 金额为非负整数语义（可理解为最小货币单位）；预留金额必须为正，提交金额可为 0。

## 运行测试

```sh
python3 -m unittest test_credit_ledger -v
```

## 命令行

```sh
python3 credit_ledger.py --parent-limit 10 < commands.jsonl
```

- 输入：stdin，每行一个 JSON 对象（JSON Lines）。
- 输出：stdout，每行一个 JSON 结果，与输入一一对应；`ok: true` 成功，`ok: false` 附
  `error`（机器可读错误码）与 `message`。

### 命令格式

| op | 字段 | 说明 |
| --- | --- | --- |
| `add_child` | `child`, `limit` | 新增子账户及其额度 |
| `reserve` | `child`, `amount`, `key` | 预留，返回 `reservation`（即 `rsv_<key>`） |
| `commit` | `reservation`, `amount`, `key` | 提交；返回 `committed` 与 `released`（释放差额） |
| `cancel` | `reservation`, `key` | 取消预留，全额释放 |
| `set_parent_limit` | `limit` | 调整父额度（不追溯撤销） |
| `state` | — | 返回当前完整状态快照 |

### 错误码

`invalid_amount`、`invalid_limit`、`unknown_child`、`duplicate_child`、
`child_limit_exceeded`、`parent_limit_exceeded`、`reservation_not_found`、
`commit_exceeds_reserved`、`unknown_op`、`bad_request`。

### 示例（合成数据）

```sh
printf '%s\n' \
  '{"op":"add_child","child":"a","limit":10}' \
  '{"op":"add_child","child":"b","limit":10}' \
  '{"op":"reserve","child":"a","amount":6,"key":"r1"}' \
  '{"op":"reserve","child":"b","amount":6,"key":"r2"}' \
  '{"op":"commit","reservation":"rsv_r1","amount":4,"key":"c1"}' \
  '{"op":"reserve","child":"b","amount":6,"key":"r3"}' \
  '{"op":"state"}' | python3 credit_ledger.py --parent-limit 10
```

输出要点：`r2` 因父可用不足失败；`c1` 提交 4 并释放 2；随后 `r3` 成功；
最终 `parent_committed=4`、`parent_reserved=6`、`parent_available=0`。

## 文件

- `credit_ledger.py` — 核心 `CreditLedger` 类与 CLI。
- `test_credit_ledger.py` — 20 个 unittest 用例，含并发竞争测试与失败原子性测试。
