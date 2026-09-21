# 增量变化点索引

从可更正的数据流中检测均值发生变化的位置，结果支持新增与撤销。仅依赖 Python 标准库。

## 模型

- 事件以 `(source, seq)` 唯一标识；同标识同值重复提交不增加样本量，同标识不同值视为对历史记录的更正。
- 候选点是相邻事件之间的分界。对分界取左右各最近 `window` 个样本，两侧样本数都
  `>= min_samples` 时，若两侧均值差绝对值 `>= threshold`（`Fraction` 有理数精确比较，
  等于阈值算达标），判定为变化点。
- 变化点以 `boundary=(左事件键, 右事件键)` 稳定标识，证据含左右样本集合与两侧均值。

## 接口（changepoint.py）

- `add / correct / retract / apply_batch`：每次修改只重算窗口可能覆盖被改样本的候选点
  （约 `2*window` 个），返回 `ChangeDelta` 列表；`kind='retract'` 表示旧结果失去支持，必须撤销。
- `change_points()`：当前全部变化点及证据。
- `ChangePointConsumer`：消费方，应用 delta 流维护生效集合。
- `full_recompute()`：全量重算，作为测试 oracle。

## 验证

- `python3 -m unittest -v`：13 个测试，覆盖常量 1/5 阈值 3 找分界、差值恰等阈值、
  样本数刚够与差一个、候选点附近撤回、更正历史记录、重复事件、分批输入，
  以及 400 步随机操作与全量 oracle 对拍（含消费方状态一致性）。
- `python3 benchmark.py`：记录增量执行触及的候选点数量。实测每次修改触及约
  `2*window` 个候选点，与数据总量 n 无关；全量重算每次评估 `n+1` 个，随 n 线性增长。
