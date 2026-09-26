# 定额优惠组合求解器（quota_offer_solver）

在空仓库中实现的定额优惠组合求解组件。给定一组商品（含原价）与一组定额优惠，
选择优惠子集及其**执行次序**，使实付最小；实付并列时取优惠 ID 序列字典序最小者。
纯 Python 3.11 标准库实现，无第三方依赖。所有示例数据均为合成数据。

## 语义规则

- **定额优惠**：每个优惠为整数折扣额，从覆盖商品的剩余应付中扣减。
- **互斥组**：`group` 相同的优惠最多选择一个；`group` 为 `null`/缺省表示不参与互斥。
- **最低原价门槛**：`min_original` 按覆盖商品的**原价合计**判断，与已应用优惠无关（静态判定，不随优惠后价变化）。
- **折扣封顶**：每个优惠的实际减免 = `min(折扣额, 覆盖商品剩余应付合计)`，实付不会为负。
- **每优惠最多一次**；执行次序由求解器选择。
- **分摊规则（确定性）**：按商品在输入中的顺序，依次从覆盖商品的剩余应付中扣除。
- **零减免不入选**：应用时减免为 0 的优惠不纳入序列，记入被排除列表。
- **目标**：最小化实付 `total_payable`；并列时取优惠 ID 序列字典序最小者。

## 运行

```bash
# 从标准输入读取
python3 quota_offer_solver.py < examples/mutual_exclusion.json

# 从文件读取 + 美化输出 + 穷举对照核验
python3 quota_offer_solver.py examples/multi_item.json --pretty --verify
```

选项：`--verify` 运行小规模穷举对照器核验（优惠数 >8 时很慢）；
`--max-states N` 精确搜索状态预算（默认 200000，超出回退启发式并标记 `proven_optimal: false`）。

## 输入格式（JSON）

```json
{
  "items": [{"id": "apple", "price": 8}],
  "offers": [
    {"id": "COMBO10", "discount": 10, "items": ["apple", "bread"],
     "min_original": 20, "group": "combo"}
  ]
}
```

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `items[].id` | string | 是 | 商品 ID，唯一 |
| `items[].price` | int ≥ 0 | 是 | 原价 |
| `offers[].id` | string | 是 | 优惠 ID，唯一 |
| `offers[].discount` | int ≥ 0 | 是 | 整数折扣额 |
| `offers[].items` | string[] | 否 | 覆盖商品 ID；缺省为全部商品 |
| `offers[].min_original` | int ≥ 0 | 否 | 最低原价门槛，默认 0 |
| `offers[].group` | string/null | 否 | 互斥组，默认不参与互斥 |

## 输出格式（JSON）

| 字段 | 说明 |
| --- | --- |
| `total_original` / `total_discount` / `total_payable` | 原价合计 / 总减免 / 实付 |
| `proven_optimal` | 是否经精确搜索证实最优；启发式回退时为 `false` |
| `sequence` | 优惠 ID 应用次序 |
| `steps[]` | 逐步分摊：`offer`、`discount`（名义折扣）、`deducted`（实际减免）、`allocation`（分商品分摊）、`remaining_after`（分摊后各商品剩余应付） |
| `excluded[]` | 未入选优惠及原因（门槛未满足 / 互斥 / 无减免效果 / 未被选中） |
| `verification` | 仅 `--verify` 时输出：穷举对照器的实付、序列及一致性结论 |

## 求解与对照

- **主求解器**：记忆化精确搜索，状态为（已用优惠位掩码, 各商品剩余应付），
  合并殊途同归的状态；超出 `--max-states` 预算时回退贪心启发式并明确标记未证最优。
- **穷举对照器**（`brute_force`）：独立实现，枚举所有合法子集的全部排列，
  供小规模实例交叉核验（`--verify` 或单元测试）。

## 测试

```bash
python3 -m unittest -v
```

覆盖：两条验收边界（互斥取减 7；双减 6 实付为 0 而非 -2）、门槛按原价、
折扣封顶、次序选择、字典序并列、零减免排除、输入校验、300 例随机小规模
与穷举对照器交叉核验、CLI 冒烟。
