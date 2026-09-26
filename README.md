# 定额优惠组合求解器

在空仓库中实现的优惠组合求解组件：从一组定额优惠中选出子集并决定执行次序，
使**最终实付最小**；实付相同时取**优惠 ID 序列（按应用顺序）字典序最小**的方案。
仅依赖 Python 3.11 标准库，所有示例数据均为合成数据。

## 语义规则

1. 每个优惠可指定：整数折扣 `amount`、覆盖商品集合 `item_ids`、
   最低原价门槛 `min_subtotal`、互斥组 `mutex_group`。
2. **门槛始终按原价判定**：覆盖商品的原价小计 `>= min_subtotal` 才可用，
   与已应用的优惠无关（原价不随折扣改变），因此门槛判定是静态的。
3. **折扣不超过覆盖商品剩余应付**：优惠生效时实际折扣为
   `min(amount, 覆盖商品剩余应付合计)`，实付不会变负。
4. 每个优惠最多使用一次；同一互斥组内最多选用一个。
5. **执行次序是决策变量**：由于规则 3 的截断效应，不同次序可能产生不同总折扣
   （见下文示例）。
6. 实际折扣为 0 的优惠不会被应用（无意义，且避免字典序比较出现退化序列）。
7. 优惠 ID 按字符串字典序比较。

**分摊规则**：每个优惠的实际折扣按商品在输入中的顺序依次填满覆盖商品的剩余
应付。该规则是确定性的，仅影响逐步分摊的展示，不影响总实付。

## 输入格式（JSON）

```json
{
  "items": [
    {"id": "BOOK", "price": 10, "quantity": 1}
  ],
  "offers": [
    {"id": "SAVE7", "amount": 7, "item_ids": ["BOOK"],
     "min_subtotal": 0, "mutex_group": "site"}
  ]
}
```

| 字段 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `items[].id` | string | 是 | — | 商品 ID，全局唯一 |
| `items[].price` | int ≥ 0 | 是 | — | 商品单价（整数） |
| `items[].quantity` | int ≥ 1 | 否 | 1 | 数量，原价小计 = price × quantity |
| `offers[].id` | string | 是 | — | 优惠 ID，全局唯一 |
| `offers[].amount` | int ≥ 1 | 是 | — | 整数折扣金额 |
| `offers[].item_ids` | string[] | 否 | 全部商品 | 覆盖商品集合 |
| `offers[].min_subtotal` | int ≥ 0 | 否 | 0 | 最低原价门槛（按原价判定） |
| `offers[].mutex_group` | string \| null | 否 | null | 互斥组，同组最多选一个 |

## 输出格式（JSON）

```json
{
  "original_total": 20,
  "total_discount": 20,
  "final_payment": 0,
  "sequence": ["BOOK8", "BUNDLE15"],
  "steps": [
    {"offer_id": "BOOK8", "effective_discount": 8,
     "allocation": {"BOOK": 8},
     "remaining_after": {"BOOK": 2, "PEN": 10}}
  ],
  "excluded": [
    {"offer_id": "VIP5", "reason": "threshold_not_met", "detail": "..."}
  ]
}
```

- `sequence`：最优优惠 ID 应用序列（字典序最小者）。
- `steps`：**逐步分摊**——每步的实际折扣、分摊到各商品的金额、执行后各商品剩余应付。
- `excluded`：**被排除优惠及原因**，`reason` 取值：
  - `threshold_not_met`：覆盖商品原价小计低于门槛（按原价判定）。
  - `mutex_conflict`：同互斥组中已选用其他优惠（`detail` 指明胜者）。
  - `no_remaining_payable`：最优序列执行后覆盖商品剩余应付已为 0。
  - `not_selected`：兜底原因（理论上不可达：否则追加该优惠可进一步降低实付）。

## 命令行用法

```bash
python3 discount_solver.py example.json            # 从文件求解
cat example.json | python3 discount_solver.py      # 从标准输入求解
python3 discount_solver.py --verify example.json   # 附加穷举对照器比对结果
python3 discount_solver.py --max-brute-force 10 --verify example.json
```

输入非法时退出码为 2，错误信息输出到 stderr。

## 求解器与穷举对照器

- **主求解器** `solve()`：记忆化 DFS，状态为 `(各商品剩余应付, 已用优惠集合)`，
  返回值 `(最大额外折扣, 字典序最小后缀序列)`。公共前缀不改变后缀的字典序比较
  结果，因此按 `(折扣降序, 序列字典序升序)` 逐状态取优即可组合出全局最优。
  复杂度关于优惠数呈指数级，适用于中小规模。
- **穷举对照器** `brute_force()`：用 `itertools.permutations` 枚举所有优惠子集的
  全部排列，语义与主求解器完全一致（含互斥约束与零效果跳过），默认最多接受
  8 个可用优惠，超出返回 `None`。`compare_with_brute_force()` 输出两者是否一致。

## 运行测试

```bash
python3 -m unittest -v test_discount_solver
```

测试覆盖：两条验收边界（互斥选减 7；两优惠各减 6 实付为 0 而非 -2）、
门槛按原价判定、次序敏感用例、字典序平局、子集覆盖截断、数量乘单价、
输入校验、CLI（文件/标准输入/非法输入退出码），以及 300 组固定种子的
合成随机数据上主求解器与穷举对照器的交叉验证。

## 已知限制

- 价格与折扣必须为整数（题目要求整数折扣）。
- 主求解器为指数级精确算法，优惠数很大（数十个以上）时不保证可行；
  穷举对照器默认仅用于 ≤ 8 个可用优惠的小规模验证。
