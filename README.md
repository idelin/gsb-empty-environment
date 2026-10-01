# 风险阈值迟滞告警机（hysteresis_alert）

纯 Python 3.11 标准库实现，无第三方依赖。所有示例数据均为合成数据。

## 规则

- 数值流按序号（seq）严格递增处理。
- 值 `>= H` 记为“高”：连续 `m` 次进入告警（`ALERT`）。
- 值 `<= L` 记为“低”：连续 `n` 次解除告警（`RESOLVE`），要求 `L < H`。
- 中间值（`L < v < H`）与缺测均重置高/低连续计数。
- 重复序号：值相同则幂等（无副作用、无输出）；值不同则拒绝（退出码 1）。
- 序号小于已处理序号（乱序）同样拒绝。
- 配置切换只影响后续数据，重置连续计数，保留当前告警状态。
- 每条数据处理后原子落盘（临时文件 + rename），重启后从持久化状态恢复，
  不会重复发送已发生的状态变更；重放最后一条仍幂等。

## 命令行用法

```bash
# 1. 初始化或切换配置（切换会重置连续计数）
python3.11 hysteresis_alert.py config --state state.json --high 10 --low 5 --m 2 --n 2

# 2. 处理数据流（缺省读标准输入，也可指定输入文件）
printf '1 11\n2 NA\n3 11\n4 11\n' | python3.11 hysteresis_alert.py ingest --state state.json

# 3. 查看当前状态
python3.11 hysteresis_alert.py status --state state.json
```

## 输入格式（ingest）

每行一条记录，空白分隔两列，`#` 开头与空行跳过：

```
<seq> <value>
```

- `seq`：整数序号，必须严格递增（允许等于上一条做幂等重放）。
- `value`：浮点数；`NA` / `N/A` / `MISSING` / `NONE` / `NULL`（大小写不敏感）表示缺测。

## 输出格式

- `ingest`：仅在发生状态变更时向 stdout 输出一行 JSON：

  ```json
  {"seq": 4, "event": "ALERT", "value": 11.0, "state": "ALERTED"}
  ```

  `event` 为 `ALERT` 或 `RESOLVE`。无变更则无输出。
- `config`：输出一行 JSON 确认配置生效。
- `status`：输出完整状态 JSON（配置、计数、最后序号/值、历史事件）。
- 错误（格式错误 / 拒绝）写 stderr；退出码：0 成功，1 数据被拒绝，2 用法/状态错误。

## 验收边界验证（H=10, L=5, m=2, n=2）

输入 `11, NA, 11, 11, 4, 7, 4, 4`（seq 1–8）实际运行结果：

- seq1–3（11, 缺测, 11）：无输出，未触发；
- seq4（11）：`ALERT`；
- seq5–7（4, 7, 4）：无输出，未解除；
- seq8（4）：`RESOLVE`。

## 测试

```bash
python3.11 -m unittest test_hysteresis_alert -v
```

18 个用例覆盖：验收边界序列、中间值/缺测重置、阈值含边界、告警中去重、
序号幂等与拒绝、配置切换、持久化重启不重发。
