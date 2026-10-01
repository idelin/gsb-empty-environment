# 资源租约的级联撤销（cascade-lease）

纯 Python 标准库实现（兼容 Python 3.11+），无第三方依赖。

## 模型

- 租约构成一棵树：根租约无父租约，其余租约从父租约借出。
- 子租约时间区间（半开区间 `[start, end)`）必须包含在父租约区间内。
- 任意时刻，同一父租约下所有活跃子租约的配额总和不得超过父租约配额。
- 父租约缩短区间或降低配额时，计算**最小损失撤销集合**：
  - 损失 = 被撤销租约的权重之和；
  - 撤销递归级联到全部子孙，同一租约的损失只计一次；
  - 损失相同时，按被撤销 ID 排序序列的字典序取最小者。
- 区间缩短后不再被包含的子租约为强制撤销（同样级联）。

## 运行

```bash
python3 cascade_lease.py examples/acceptance.json   # 从文件读取
python3 cascade_lease.py < examples/acceptance.json # 从标准输入读取
python3 -m unittest test_cascade_lease -v           # 运行测试
```

## 输入格式

标准输入或指定文件为一个 JSON 对象，包含 `operations` 数组，按序执行：

```json
{
  "operations": [
    {"op": "add_root",  "id": "root", "quota": 5, "start": 0, "end": 100, "weight": 0},
    {"op": "add_lease", "id": "a", "parent": "root", "quota": 3, "start": 0, "end": 50, "weight": 10},
    {"op": "resize",    "id": "root", "quota": 3}
  ]
}
```

- `add_root`：创建根租约。字段：`id`、`quota`、`start`、`end`、`weight`（可选，默认 0）。
- `add_lease`：从父租约借出子租约。字段同上，另需 `parent`。
  区间不被父包含、或加入后并发配额超父额时，该操作报错且系统状态不变。
- `resize`：变更租约的 `quota` / `start` / `end`（均可选，缺省保持原值）。
  变更后若逃逸自身父租约约束则报错；否则触发最小损失撤销。

## 输出格式

向标准输出打印 JSON：每个操作对应一个结果项。

```json
{
  "results": [
    {"op": "add_root", "id": "root", "status": "ok"},
    {"op": "add_lease", "id": "a", "status": "ok"},
    {"op": "resize", "id": "root", "status": "ok", "revoked": ["b"], "loss": 1}
  ]
}
```

- `status: "ok"`：成功；`resize` 额外返回 `revoked`（按字典序排序的被撤销 ID，
  含级联子孙）与 `loss`（总损失）。
- `status: "error"`：失败，附 `error` 说明；后续操作继续执行。

## 示例

- `examples/acceptance.json`：验收边界——父额 5，并发子租约 3（权重 10）与
  2（权重 1），父减至 3 时撤销配额 2 的子租约 `b`，损失 1。
- `examples/cascade.json`：级联场景——撤销 `p` 时其子孙 `p1`、`p2`、`p1a`
  一并撤销，损失 2+5+7+11=25（每个租约只计一次）。

## 最优性说明

求解器对被调整租约的直接子租约做子集穷举（每个子租约的撤销成本为其整个
子树的权重和），在所有满足并发配额约束的方案中取损失最小、字典序最小者，
因此撤销集合**保证全局最优**。复杂度为 O(2^k)（k 为直接子租约数），
适用于单父租约下子租约数量有限的场景。

所有示例与测试数据均为合成数据。
