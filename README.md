# 资源借用的级联撤销（Cascading Lease Revocation）

纯 Python 3.11 标准库实现。租约（lease）组成一棵树：子租约从父租约借出配额，
满足两条不变式：

1. **时间包含**：子租约的时间区间 `[start, end)` 必须包含于父租约区间内；
2. **并发配额**：任一时刻，同时活跃的子租约配额之和不得超过父租约配额
   （时间不重叠的子租约可以轮流使用同一配额）。

当某个租约被**缩短**（区间变小）或**减额**（配额变小）时，组件在其直接子租约中
计算**最小损失撤销集合**：

- **损失** = 被撤销租约的权重之和。撤销会**级联**到整个子孙子树；被级联撤销的
  子孙计入撤销集合与损失，但每个租约只计一次（不重复计损失）；
- **平局**：损失相同时，取撤销 ID 排序序列字典序最小者（含级联子孙的完整集合）；
- 新区间不再包含的子租约属于**强制撤销**，必然进入撤销集合；
- 撤销后的剩余状态保证不超额（结果中 `valid: true`，并在内部做了全树校验）。

## 运行

```sh
python3.11 lease_revoke.py examples/acceptance.json     # 从文件读入
cat examples/acceptance.json | python3.11 lease_revoke.py   # 或从标准输入读入
```

输入不合法（结构非法、初始树违反不变式、操作越界等）时，错误信息输出到
stderr，退出码为 2。

## 输入格式（JSON）

```json
{
  "leases": [
    {"id": "root", "parent": null,  "start": 0, "end": 100, "quota": 5, "weight": 0},
    {"id": "a",    "parent": "root", "start": 0, "end": 50,  "quota": 3, "weight": 10}
  ],
  "operation": {"lease": "root", "quota": 3, "start": 0, "end": 100}
}
```

- `leases`：租约数组，必须恰好一个根（`parent` 为 `null`）。
  - `id`：字符串，唯一；`parent`：父租约 `id` 或 `null`；
  - `start` / `end`：整数，半开区间 `[start, end)`，`end > start`；
  - `quota`：非负整数；`weight`：非负整数（省略时为 0）。
- `operation`：对 `lease` 指定的租约执行缩短/减额；`start`、`end`、`quota`
  均可选，省略表示保持不变。新区间必须仍包含于其父租约，新配额不得使父租约超载。

## 输出格式（JSON）

```json
{
  "lease": "root",
  "revoked": ["light"],
  "loss": 1,
  "valid": true
}
```

- `lease`：被操作的租约 ID；
- `revoked`：实际撤销的租约 ID 排序列表（含级联子孙，每个 ID 只出现一次）；
- `loss`：撤销集合的权重之和（每个租约只计一次）；
- `valid`：撤销后全树不变式校验通过时为 `true`。

## 验收边界示例

父配额 5，两个并发子租约配额 3（权重 10）与 2（权重 1）；父减额至 3 时，
撤销配额为 2 的子租约（损失 1），保留配额 3 的子租约，剩余负载 3 ≤ 3 不超额：

```sh
$ python3.11 lease_revoke.py examples/acceptance.json
{
  "lease": "root",
  "revoked": [
    "light"
  ],
  "loss": 1,
  "valid": true
}
```

`examples/cascade.json` 演示级联：根减额至 1 时，撤销 `child` 会连带撤销
`grand`、`great`，损失 10+7+4+1=22，每个租约只计一次。

## 测试

```sh
python3.11 -m unittest test_lease_revoke -v
```

覆盖：验收边界、最小损失选择、字典序平局、强制撤销（区间包含）、
并发/非并发配额扫描、级联撤销与不重复计损失、非法输入校验、CLI 冒烟测试。

## 复杂度说明

最小损失撤销通过对直接子租约做子集枚举求得**精确最优解**，时间复杂度为
O(2^k · k)（k 为该租约的直接子租约数），适合 k 较小（约 ≤ 20）的场景；
该问题本身等价于带权覆盖问题，一般情形下是 NP 难的。字典序平局在完整
撤销集合（含级联子孙）的排序 ID 序列上比较。

所有示例数据均为合成数据。
