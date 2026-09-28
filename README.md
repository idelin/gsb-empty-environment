# push_suppress — 多渠道推送抑制决策

在离散有限时间上为候选通知选择**总收益最大**的可行发送调度。仅使用
Python 3.11 标准库，所有示例数据均为合成数据。

## 约束模型

每条候选通知包含：用户、渠道、收益、可发送时间窗口、耗费、互斥活动 ID。
可行调度需同时满足：

1. **发送一次且在窗口内**：每条通知最多发送一次，发送时刻 `t` 满足
   `start <= t <= end`（整数、闭区间）；设置 `time_horizon` 时还需落在
   `[0, time_horizon)` 内。
2. **每用户滚动窗口（半开）**：对任意用户、任意整数 `t`，半开区间
   `[t, t + L)` 内至多 `K` 条发送（默认 `L=3, K=1`，可配置）。
   这是真·滑动窗口，**不是固定整段计数**：`L=3, K=1` 时，
   时刻 0 与 2 冲突（同在 `[0,3)`），时刻 0 与 3 允许；
   时刻 2 与 4 也冲突（同在 `[2,5)`），尽管它们分属固定段
   `[0,3)` 与 `[3,6)`。精确判定：用户发送时刻排序后需满足
   `times[i+K] - times[i] >= L`。
3. **全局渠道容量**：每个渠道每个时刻的发送数不超过
   `channel_capacity[channel]`（跨用户全局计数；未列出的渠道不限）。
4. **互斥活动**：同一 `mutex_group`（活动 ID）的通知至多发送一条。
5. **耗费预算**：所有发送的 `cost` 之和不超过 `budget`（可缺省为不限）。

## 最优性与平局规则

求解器为精确分支限界（`push_suppress/solver.py`），对给定实例返回**可证
最优**的调度。总收益并列时，按如下键取字典序最小者：将调度中的
`(通知ID, 发送时刻)` 对按（通知 ID，发送时刻）排序后逐对比较。
因此 ID 较小者优先；同一通知取较早时刻；空元组最小，故零收益通知不会
被多余加入。同一实例内通知 ID 必须可相互比较（全为字符串或全为整数）。

注意：该精确算法最坏情况为指数复杂度，适用于中小规模实例。

## 输入格式（实例 JSON）

```json
{
  "time_horizon": 6,
  "user_window": {"length": 3, "max_sends": 1},
  "budget": 4,
  "channel_capacity": {"email": 1, "sms": 1},
  "notifications": [
    {"id": "n1", "user": "u1", "channel": "email", "profit": 6,
     "window": [0, 2], "cost": 1, "mutex_group": "campA"}
  ]
}
```

- `notifications`（必填）：`id`、`user`、`channel`、`profit`、
  `window: [start, end]` 必填；`cost`（默认 0）、`mutex_group`
  （默认无）可选。
- `user_window`（可选）：`length`（默认 3）、`max_sends`（默认 1）。
- `channel_capacity`（可选）：渠道 -> 每时刻容量。
- `budget`（可选）：总耗费上限，缺省不限。
- `time_horizon`（可选）：时间槽数，校验窗口落在 `[0, H)`。

## 输出格式

`solve` 输出：

```json
{
  "profit": 13.0,
  "optimal": true,
  "nodes_explored": 64,
  "sends": [{"id": "n1", "time": 0}, {"id": "n3", "time": 3}],
  "violations": []
}
```

`sends` 按（通知 ID，时刻）排序；`violations` 为全部约束复核器对结果
的复查（正常应为空）。调度文件格式为 `{"sends": [{"id": ..., "time": ...}]}`，
也是 `check` 子命令的输入。

## 命令行用法

```bash
python3 -m push_suppress solve examples/small_instance.json
python3 -m push_suppress check examples/small_instance.json examples/schedule_example.json
python3 -m push_suppress enumerate examples/small_instance.json [--limit N]
python3 -m push_suppress demo
```

- `solve`：求解并输出最优调度（含复核结果）。
- `check`：对给定调度运行全部约束复核器；可行退出码 0，否则 1。
- `enumerate`：枚举全部可行调度（小例），标注最优解。
- `demo`：运行内置合成小例，输出最优调度与全部可行调度枚举。

## 约束复核器

`push_suppress/checkers.py` 提供独立复核函数，均返回违规描述列表
（空列表 = 通过）：

- `check_send_once_and_window` — 每条至多一次、窗口与时间界
- `check_user_rolling_window` — 每用户半开滚动窗口
- `check_channel_capacity` — 全局渠道容量
- `check_mutex` — 互斥活动
- `check_budget` — 耗费预算
- `validate_schedule` — 聚合以上全部

## 测试

```bash
python3 -m unittest discover -s tests -v
```

测试覆盖：滚动窗口验收边界（0/2 冲突、0/3 允许、滑动窗口 vs 固定段）、
各复核器、求解器正确性，以及 60 个确定性伪随机小实例上**求解器与独立
暴力枚举的逐一比对**（收益与平局键均一致），即在这些小例上最优性经过
穷举验证。
