# 风险阈值迟滞告警机 (alert_machine)

纯 Python 3.11 标准库实现，无第三方依赖。

## 语义

- 数值流按序号 `seq` 单调递增处理。
- `value >= H` 记为一次高触发；连续 `m` 次高触发 → 进入 `ALERT`，发出 `ALERT` 事件。
- `value <= L` 记为一次低触发；连续 `n` 次低触发 → 回到 `NORMAL`，发出 `RESOLVE` 事件。
- 要求 `L < H`。中间值（`L < v < H`）与缺测（`value: null` / `"MISSING"` / `"NaN"` / `""`）
  均不计高不计低，并将高、低连续计数同时清零。
- 状态迁移发生后计数器清零，重新累计。
- 重复序号：同值幂等（返回 `duplicate`，不产生副作用）；不同值拒绝（`rejected`）。
  小于已处理序号的乱序记录同样拒绝。
- 配置切换只影响后续记录：重启时若配置文件与持久化配置不同，保留当前告警状态、
  清零计数器，之后按新配置判定。
- 每条记录处理后状态原子写入状态文件（临时文件 + rename），重启后从断点继续，
  已发送的状态变更事件不会重发（重放触发记录只会得到 `duplicate`）。

## 运行

```bash
python3 alert_machine.py --config config.json --state state.json [--input input.jsonl]
```

`--input` 缺省时从 stdin 读取；结果逐行输出到 stdout（JSONL）。
任何记录被拒绝时进程退出码为 1，否则为 0。

## 输入格式

- 配置文件（JSON）：`{"high": 10, "low": 5, "m": 2, "n": 2}`
- 输入流（JSONL，每行一条）：`{"seq": <非负整数>, "value": <数值>}`
  - 缺测：`"value": null`、`"value": "MISSING"`，或省略 `value` 字段。

## 输出格式

每行一个 JSON 对象：

- `{"seq": N, "status": "processed", "state": "NORMAL"|"ALERT", "event": null|"ALERT"|"RESOLVE"}`
- `{"seq": N, "status": "duplicate", "state": ..., "event": null}` — 同序号同值，幂等
- `{"seq": N, "status": "rejected", "error": "..."}` — 同序号不同值 / 乱序 / 非法输入

## 测试

```bash
python3 -m unittest test_alert_machine -v
```

覆盖：验收边界序列（H10 L5 m2 n2：11,缺测,11 不触发，再 11 触发；4,7,4 不解除，
再 4 解除）、边界值含等号、缺测/中间值重置计数、迁移后计数清零、同值幂等、
异值拒绝、乱序拒绝、配置切换重置计数且仅影响后续、持久化重启不重发事件。
所有示例与测试数据均为合成数据。
