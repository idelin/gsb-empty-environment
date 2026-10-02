# 用户旅程去重归因

Python 3.11 标准库实现，无第三方依赖。对成交前窗口内最后一个渠道触点归因；
支持事件版本覆盖、迟到触点增量重算、版本化跨设备身份图（边增删导致主体合并/分裂）、
全量重算独立对照、持久日志恢复与幂等键。所有示例数据均为合成数据。

## 归因规则

- 事件：事件ID、主体、业务时间（整数）、渠道（触点）或金额（成交，整数）。
- 成交 `c` 的归因窗口为 `[c.time - window, c.time]`，**左右边界均闭**。
  例：触点 a 在时间 2、成交在时间 5、窗口 3 → 窗口 [2,5] 包含 a。
- 取窗口内业务时间最后的触点；时间相同取触点ID最小者。
- 事件修订按版本覆盖：同一事件ID仅最高版本生效，低版本/重复版本忽略。
- 迟到触点只重算窗口覆盖其（新旧）业务时间的成交，输出归因变化清单。
- 同主体多个成交各自独立计算；金额为整数（非整数拒绝）。

## 身份图

- 主体 = 带版本无向身份图的连通分量；边新增合并主体，边删除可拆分主体。
- 每次身份变更精准重算被合并/拆开主体的全部成交，输出 `identity_change` 记录：
  `before`/`after` 分渠道金额、`added`（新增）、`revoked`（撤销）、`conserved`（守恒）。
  未归因金额计入 `<unattributed>` 桶，保证变更前后总金额守恒。
- `query_at seq` 按历史序号回放，旧版本查询保持当时的身份关系与事件版本。

## 输入格式（JSONL，每行一个操作）

```json
{"op": "event", "key": "k1", "event_id": "t1", "version": 1, "subject": "u1", "time": 2, "kind": "touchpoint", "channel": "a"}
{"op": "event", "key": "k2", "event_id": "c1", "version": 1, "subject": "u1", "time": 5, "kind": "conversion", "amount": 100}
{"op": "add_edge", "key": "k3", "a": "u1", "b": "u2"}
{"op": "remove_edge", "key": "k4", "a": "u1", "b": "u2"}
{"op": "query"}
{"op": "query_at", "seq": 2}
```

- `key` 为幂等键（缺省按行号生成）；重复键的操作不再生效。
- `#` 开头与空行忽略。

## 输出格式（JSONL）

- `attribution_change`：`conversion`、`amount`、`old_channel`/`new_channel`、
  `old_touchpoint`/`new_touchpoint`、`seq`。
- `identity_change`：`edge`、`before`/`after`、`added`/`revoked`、`conserved`、`seq`。
- `query_result`：`attributions` = 成交ID → `{touchpoint_id, channel, amount}`。

## 命令行

```bash
python3.11 -m attribution.cli demo                          # 验收边界示例
python3.11 -m attribution.cli run --window 3 --input examples/ops.jsonl \
    --journal journal.log [--output out.jsonl]              # 处理操作流
python3.11 -m attribution.cli verify --window 3 --input examples/ops.jsonl
                                                            # 增量 vs 全量对照
```

## 持久日志与恢复

- 每条日志记录同时包含幂等键、操作与归因变更，单条记录一次 `fsync` 落盘，
  三者同事务生效；恢复时按键去重回放，幂等键与归因变更一起恢复。
- 记录带 CRC 校验；崩溃造成的截断/损坏尾部在恢复时截除，后续追加不受影响。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

覆盖：窗口左闭边界、迟到触点改归、同时间取最小触点ID、版本覆盖与旧版本忽略、
多成交独立、身份合并/拆分及金额守恒、旧版本查询、日志恢复与幂等、截断尾部，
以及 30 个随机种子 × 120 个操作（迟到触点、成交修订、边增删、同业务时间交错）
的增量与全量逐成交一致性对照（含历史版本抽查）。
