# 分支账本三方合并

对带稳定 ID、金额与标签集合的账本条目做三方合并（共同基线 + 两个分支）。
纯 Python 3.11+ 标准库实现，无第三方依赖。所有示例数据均为合成数据。

## 合并语义

- **金额**：与基线比较。仅一侧修改 → 取该侧；两侧改为相同值 → 取该值；
  两侧改为不同值 → **显式冲突**，输出中该条目 `amount` 为 `null`，
  冲突记录完整保留基线值与双方操作 ID、金额，绝不静默选边。
  同一分支内连续多次修改属单一有序历史，以最后值为准，不构成冲突。
- **标签**：带操作 ID 的增删集合（observed-remove set）。
  每次添加有唯一 `op_id`；删除操作记录它在所属分支中**已观察到**的
  添加操作 ID 集合，只移除这些添加。因此并发删除与并发重新添加并存时，
  后到的添加仍然生效；对未观察到的标签执行删除是空操作。
- **代数性质**：合并对操作集合做并集，天然满足**交换律**与**幂等律**；
  冲突证据保存在状态中，再次参与合并不丢失。
  同一 `op_id` 在两个分支承载不同值时拒绝合并并显式报错（防止证据被覆盖）。

## 命令行

```sh
python3 merge_ledger.py merge INPUT.json [--emit-state]
```

退出码：`0` 合并成功无冲突；`1` 合并成功但存在冲突；`2` 输入/操作错误。

## 输入格式

```json
{
  "base":  [{"id": "e1", "amount": 10, "tags": ["a"]}],
  "left":  [{"op_id": "L1", "op": "set_amount", "entry": "e1", "amount": 11}],
  "right": [{"op_id": "R1", "op": "add_tag", "entry": "e1", "tag": "x"}]
}
```

- `base`：基线条目数组，每项含稳定 `id`、`amount`（JSON 数值）、可选 `tags`。
- `left` / `right`：分支操作日志（按时间顺序排列），省略时视为无修改。
  也可传入此前 `--emit-state` 输出中的 `state` 对象，用于多轮合并。

操作（`op_id` 必须全局唯一）：

| op           | 字段                              | 说明 |
|--------------|-----------------------------------|------|
| `set_amount` | `entry`, `amount`                 | 修改金额；分支内后写覆盖先写 |
| `add_tag`    | `entry`, `tag`                    | 添加标签 |
| `remove_tag` | `entry`, `tag`, 可选 `removes`    | 删除标签；`removes` 缺省时自动取该分支当前观察到的生效添加操作 ID |

## 输出格式

```json
{
  "ok": true,
  "entries": [{"id": "e1", "amount": 11, "tags": ["a", "x"]}],
  "conflicts": []
}
```

冲突时 `ok` 为 `false`，冲突条目 `amount` 为 `null`，`conflicts` 形如：

```json
[{"entry": "e1", "field": "amount", "base": 10, "values": {"L1": 11, "R1": 12}}]
```

`--emit-state` 额外输出 `state` 字段（内部操作集合状态），可作为后续
合并的 `left`/`right` 输入；`merge(base, M, M) == M`（幂等）。

## 示例

```sh
python3 merge_ledger.py merge examples/merge_ok.json       # 退出码 0：金额 11 含标签 x
python3 merge_ledger.py merge examples/merge_conflict.json # 退出码 1：11 与 12 显式冲突
```

## 测试

```sh
python3 -m unittest test_merge_ledger -v
```
