# 多渠道推送抑制决策 (Multi-Channel Push Suppression)

给定候选推送通知（用户、渠道、收益、可发送时间窗口、渠道耗费、互斥活动 ID），
在以下约束下选择「发送哪些通知、在什么时刻发送」，使总收益最大：

1. **每用户滚动窗口限频**：同一用户在任意长度为 `window_length`（默认 3）的
   **半开** 时间窗口 `[s, s+L)` 内，最多发送 `max_pushes_per_window`（默认 1）条。
   窗口起点 `s` 遍历所有时刻，不是按固定整段分桶计数。
2. **全局渠道容量**：每个渠道在每个时刻被所有用户发送的通知耗费之和
   不超过该渠道容量。
3. **互斥活动**：同一 `activity_id` 的通知最多发送一条。
4. **时间窗口**：通知只能在其闭区间 `[release, deadline]` 内、且位于
   `0 .. horizon-1` 的离散时刻发送；也可以不发送。
5. **身份合法**：调度中只允许出现输入中存在的通知 ID，每条通知至多发送一次。

时间为有限离散轴；求解采用**穷举 + DFS 剪枝**（仅标准库），搜索完整结束时
结果被证明为全局最优；若因节点上限中断，则显式报告 `proven_optimal=false`。

平局规则（确定性）：按通知 ID 升序排列，把每条通知的发送时刻组成序列
（未发送记为 `-1`，排在所有具体时刻之前），序列字典序最小者获胜。

## 运行要求

- Python 3.11（仅标准库，无第三方依赖）

## 命令行

```sh
python3.11 -m pushsuppress solve     -i examples/small.json     # 求最优调度
python3.11 -m pushsuppress enumerate -i examples/tiny.json      # 枚举所有可行调度
python3.11 -m pushsuppress check     -i examples/check_invalid.json  # 复核给定调度
# 省略 -i 时从 stdin 读取 JSON
```

退出码：`check` 发现违例返回 1，输入错误返回 2，成功返回 0。

## 输入格式（`solve` / `enumerate`）

```json
{
  "horizon": 6,
  "window_length": 3,
  "max_pushes_per_window": 1,
  "channel_capacity": {"sms": 10, "push": 10, "email": 5},
  "notifications": [
    {
      "id": "n1",
      "user": "u1",
      "channel": "sms",
      "revenue": 10,
      "release": 0,
      "deadline": 1,
      "cost": 6,
      "activity_id": null
    }
  ]
}
```

- `horizon`（整数 ≥1）：离散时刻数，合法时刻为 `0 … horizon-1`。
- `window_length`（默认 3）、`max_pushes_per_window`（默认 1）：滚动限频参数。
- `channel_capacity`：渠道 → 每时刻容量（按耗费计量）。
- 通知字段：`id`（唯一字符串）、`user`、`channel`、`revenue`（数值）、
  `release`/`deadline`（闭区间整数时刻，`0 ≤ release ≤ deadline < horizon`）、
  `cost`（数值，占用渠道容量）、`activity_id`（字符串或 `null`）。

### `check` 输入

```json
{"instance": { /* 同上 */ }, "schedule": {"a": 0, "b": 3}}
```

`schedule` 为 `通知ID -> 发送时刻` 的对象，只列出发送的通知。

## 输出格式

### `solve`

```json
{
  "feasible": true,
  "proven_optimal": true,
  "revenue": 18.0,
  "schedule": [{"id": "a", "send_slot": 0}, {"id": "b", "send_slot": 3}],
  "tie_key": [0, 3],
  "stats": {"nodes_explored": 12, "nodes_pruned": 5, "node_limit": 2000000}
}
```

未证明最优时会增加 `"warning"` 字段且 `proven_optimal` 为 `false`。

### `check`

```json
{
  "feasible": false,
  "revenue": null,
  "violations": [
    {"constraint": "user_rolling_limit", "detail": "...",
     "notification_ids": ["a", "b"]}
  ]
}
```

### `enumerate`

输出 `count`、`optimal_revenue`，以及全部可行调度（含 `revenue`、`tie_key`、
`optimal` 标记），按规范字典序排列。

## 验收边界（半开滚动窗口）

以 `examples/tiny.json` 为例（同一用户两条通知，时刻 0…3 可发）：

- 时刻 **0 与 2 冲突**：二者同属窗口 `[0,3)`（长度 3、半开），违反「最多 1 条」。
- 时刻 **0 与 3 允许**：没有任何长度 3 的半开窗口同时包含二者——`[0,3)` 不含 3，
  `[1,4)` 不含 0。
- 固定整段分桶（如 `[0,3)`、`[3,6)`）会漏掉 `1 和 3`、`2 和 4` 这类冲突；
  本实现枚举窗口起点 `s = 0 … horizon-1`，对 `[s,s+3)` 逐一计数。

复核方式：

```sh
python3.11 -m pushsuppress check -i examples/check_invalid.json   # {a:0,b:2}  -> infeasible, 退出码1
python3.11 -m pushsuppress check -i examples/check_valid.json     # {a:0,b:3}  -> feasible, 退出码0
python3.11 -m pushsuppress solve -i examples/tiny.json            # 最优 a@0,b@3 收益18
python3.11 -m pushsuppress enumerate -i examples/tiny.json        # 11 个可行调度，两个最优，平局取 [0,3]
```

`examples/small.json` 是含三渠道、容量、互斥活动的稍大小例，最优总收益 46
（发送 n1@0、n2@3、n3@1、n4@4、n5@1；互斥活动 A 中 n6 被抑制）。

## 约束复核器

`pushsuppress/checkers.py` 中每个约束都是独立复核器，均返回
`Violation` 列表（空列表表示满足）：

- `check_identity`：ID 与时刻类型合法
- `check_time_window`：时刻位于全局轴与通知窗口内
- `check_user_rolling_limit`：逐起点半开滚动窗口限频
- `check_channel_capacity`：每渠道每时刻耗费容量
- `check_mutex`：互斥活动至多发送一次
- `verify_schedule`：串联运行以上全部复核器

## 最优性与局限

- 搜索空间为 `∏(1 + 窗口宽度)`，属于指数级；本实现面向小例的精确枚举
  （默认安全节点上限 2,000,000，可用 `--node-limit` 调整）。
- 剪枝只在「乐观上界严格小于当前最优」时发生，因此不影响最优性与平局枚举；
  搜索完整结束（`proven_optimal=true`）即为**已证明全局最优**。
- 若命中节点上限，会如实返回 `proven_optimal=false` 与警告，不谎称最优。

## 测试

```sh
./run_tests.sh
# 或
python3.11 -m unittest discover -s tests -v
```

所有示例与测试数据均为合成数据。
