# 有界重放去重收件箱（Bounded Replay-Deduplicating Inbox）

消息按 `(sender, seq)` 标识，`seq` 为每个发送者严格递增的正整数。消息可乱序到达，
但只接受距已提交游标 `C` 不超过窗口 `W` 的消息。业务状态（每发送者累加值 `total`）
与去重游标 `cursor` 在同一个 SQLite 事务中持久化，实现"效果恰好一次"。

仅依赖 Python 3.11 标准库（`sqlite3`、`argparse`、`unittest`、`dataclasses`）。

## 判定规则

设当前已提交连续游标为 `C`，窗口为 `W`：

| 条件 | 结果 | 效果 |
| --- | --- | --- |
| `seq <= C` | `duplicate` | 幂等忽略，游标与业务值不变 |
| 已在窗口缓存中（`pending`） | `duplicate` | 幂等忽略（即使重放携带不同 `value`） |
| `C < seq <= C + W` 且不连续 | `cached` | 写入窗口缓存，同事务原子持久化 |
| 缓存补齐后产生连续前缀 | `committed` | 删除缓存、写账本、推进游标、累加业务值 |
| `seq > C + W` | `rejected` | 显式拒绝，不写任何数据、游标不动 |

缓存补齐在每次投递的同一事务内完成：例如 `C=0` 已缓存 `3`，收到 `1` 提交 `1`，
再收到 `2` 时连同 `3` 一起提交（`committed_seqs=[2,3]`）。

## 运行

```bash
# 单条投递
python3.11 -m dedup_inbox --db inbox.db --window 3 deliver \
    --sender alice --seq 3 --value 30

# 查询状态（可带 0..N 个 sender；不带则列出全部）
python3.11 -m dedup_inbox --db inbox.db --window 3 state alice

# 批量 JSONL（每行一个请求，空行与 # 注释行忽略）
python3.11 -m dedup_inbox --db inbox.db --window 3 batch <<'JSONL'
{"op": "deliver", "sender": "alice", "seq": 3, "value": 30}
{"op": "deliver", "sender": "alice", "seq": 4, "value": 40}
{"op": "state", "senders": ["alice"]}
JSONL
```

## 输入输出格式

全局参数：`--db PATH`（SQLite 文件，不存在自动建库）、`--window N`（默认 3，
重开既有库时必须与创建时一致，否则报错退出）。

`deliver` 参数：`--sender 字符串`、`--seq 正整数`、`--value 整数`，
可选 `--crash {before_write,after_write,after_commit}` 用于故障注入。

所有输出为单行 JSON（stdout）。投递结果：

```json
{"status": "cached", "sender": "alice", "seq": 3, "committed_seqs": [],
 "cursor": 0, "total": 0, "value": 30}
{"status": "rejected", "sender": "alice", "seq": 4, "committed_seqs": [],
 "cursor": 0, "total": 0,
 "rejected_reason": "seq 4 is beyond window W=3 of cursor 0 (max acceptable 3)"}
{"status": "committed", "sender": "alice", "seq": 2, "committed_seqs": [2, 3],
 "cursor": 3, "total": 60, "value": 20}
{"status": "duplicate", "sender": "alice", "seq": 1, "committed_seqs": [],
 "cursor": 3, "total": 60, "rejected_reason": "seq 1 <= committed cursor 3"}
```

`state` 输出：

```json
{"states": [{"sender": "alice", "window": 3, "cursor": 3, "total": 60,
             "max_acceptable": 6, "pending": []}]}
```

字段：`cursor` 为已提交连续最大序号；`total` 为已提交消息 `value` 之和；
`max_acceptable = cursor + window`；`pending` 为窗口内按序号排列的缓存消息。

退出码：

- `0`：成功（`committed` / `cached` / `duplicate` / `state`）
- `2`：消息因超出窗口被拒绝（`rejected`；batch 模式下继续处理后续行，最终返回 2）
- `3`：参数或输入错误
- `99`：被故障注入立即终止进程（`os._exit(99)`，模拟掉电）

## 故障注入与恰好一次

`deliver` 内的顺序与三个注入点：

1. `before_write`：重复/窗口校验之后、任何写操作之前；
2. `after_write`：插入 `pending`（以及补齐连续前缀、更新游标）之后，`COMMIT` 之前；
3. `after_commit`：事务已持久化提交之后、向客户端返回结果之前。

进程被 `os._exit(99)` 杀死后重新打开数据库（`journal_mode=DELETE`、
`synchronous=FULL`，SQLite 自动回滚恢复）：

- 在 `before_write` 或 `after_write` 崩溃：整个事务回滚，无部分写入，游标不动，
  重放安全；
- 在 `after_commit` 崩溃：游标与业务值已一同落盘，客户端重放时命中
  `seq <= cursor`，返回 `duplicate`，业务值不会二次累加。

因此"提交前崩溃 → 重放成功一次；提交后崩溃 → 重放为重复"，效果恰好一次。

## 验收边界示例（W=3，初始 cursor=0）

```
seq=3 -> cached   （窗口内，可缓存）
seq=4 -> rejected （超窗，游标仍为 0）
seq=1 -> committed（cursor=1, total=10）
seq=2 -> committed（连同样例 seq=3 一起提交，committed_seqs=[2,3]，cursor=3, total=60）
seq=1 -> duplicate（重复，total 仍为 60）
```

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

15 个用例覆盖：验收边界、超窗拒绝不推进游标、缓存内重复幂等、发送者隔离、
窗口边界、窗口一致性校验、非法序号、重开持久化，以及用真实子进程 +
`os._exit(99)` 验证三个故障注入点的回滚/保留与重放幂等。

## 存储结构

- `meta(key,value)`：窗口大小等元数据；
- `senders(sender PK, cursor, total)`：每发送者的去重游标与业务状态，同一行同事务更新；
- `pending(sender, seq, value, PK(sender,seq))`：有界乱序窗口缓存；
- `ledger(id, sender, seq, value, UNIQUE(sender,seq))`：已提交消息账本（审计/复核）。

## 已知边界（未声明为最优的部分）

- 本实现面向单写者顺序投递；多进程并发写未做压力验证（SQLite `BEGIN IMMEDIATE`
  会串行化写事务，但未在此仓库中测试）。
- 未实现发送者删除/TTL、`value` 使用整数而非通用负载；消息保留策略以游标与账本为准。
- 吞吐/延迟未做基准测试，不主张性能最优。
