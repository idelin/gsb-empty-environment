# ledger-merge：分支账本三方合并

对带版本的账本条目做三方合并（共同基线 base + 两个分支 left / right）。
纯 Python 3.11+ 标准库实现，无第三方依赖。所有示例数据均为合成数据。

## 数据模型与合并语义

每个账本条目有：

- `id`：稳定字符串 ID，三个快照中的条目 ID 集合必须一致；
- `amount`：数值（int/float，拒绝 bool 与非数值）；
- `tag_ops`：带操作 ID 的标签增删操作日志。

**金额（三方合并）**：仅一方相对基线修改 → 取修改方；双方改成相同值 → 取该值；
双方改成不同值 → 产生显式冲突，合并结果中 `amount` 为 `null`，
绝不静默选择"后到者"。冲突证据（基线值 + 双方值）完整保留在 `conflicts` 中。

**标签（observed-remove 集合 / OR-set 语义）**：

- `{"op": "add", "op_id": "...", "tag": "..."}`：添加标签；
- `{"op": "remove", "op_id": "...", "removes": ["add-op-id", ...]}`：只取消其
  **观察到**的 add 操作（按 `op_id` 引用），并发的新增不受影响；
- 合并 = 双方操作日志按 `op_id` 求并集；同一 `op_id` 载荷不同 → 显式冲突；
- 生效标签 = 未被任何 remove 取消的 add 的标签集合。

**代数性质**：合并是确定性的、幂等的（重复合并 / 与自身合并均为不动点）、
交换的（左右互换结果相同），且冲突证据永不丢失。

## 输入格式

三个输入文件均为 JSON，结构相同：

```json
{
  "entries": [
    {
      "id": "e1",
      "amount": 10,
      "tag_ops": [
        {"op": "add", "op_id": "a1", "tag": "x"},
        {"op": "remove", "op_id": "r1", "removes": ["a1"]}
      ]
    }
  ]
}
```

`tag_ops` 可省略（等价于空列表）。每个 `op_id` 在单个快照的单个条目内必须唯一。

## 输出格式

```json
{
  "status": "clean" | "conflict",
  "merged": [
    {"id": "e1", "amount": 11, "tags": ["x"], "tag_ops": [ ... ]}
  ],
  "conflicts": [
    {
      "entry_id": "e1",
      "field": "amount",
      "reason": "both sides changed amount to different values",
      "base": 10,
      "values": [11, 12]
    }
  ]
}
```

- `merged[].amount` 在金额冲突时为 `null`；`merged[].tags` 为由 `tag_ops` 计算的生效标签；
- `conflicts[].values` 为排序后的双方分歧值（两边证据都保留，与左右顺序无关）；
- 输出条目按 `id` 排序、操作按 `op_id` 排序，结果字节级确定。

## 命令行用法

```sh
python3 ledger_merge.py BASE.json LEFT.json RIGHT.json [-o OUT.json] [--compact]
```

退出码：`0` = 合并干净；`1` = 存在冲突（结果仍正常输出）；`2` = 输入/用法错误。

示例（合成数据，见 `examples/`）：

```sh
# 基线 10，左改 11，右只加标签 x → 合并为 11 含 x，退出码 0
python3 ledger_merge.py examples/base.json examples/left.json examples/right.json

# 左改 11、右改 12 → 显式冲突，amount 为 null，退出码 1
python3 ledger_merge.py examples/base.json examples/left.json examples/right-conflict.json
```

## 测试

```sh
python3 -m unittest test_ledger_merge -v
```

覆盖：两条验收边界、金额三方规则、标签 observed-remove 语义、
幂等 / 交换 / 冲突证据保留、输入校验与 CLI 退出码。
