# 事务性小数单位转换（Transactional Exact-Rational Unit Conversion）

Python 3.11 标准库实现，无第三方依赖。所有转换因子与结果均使用
`fractions.Fraction` 精确有理数表示，**不使用任何浮点误差阈值**；
输入的十进制字符串（如 `"0.1"`）按精确十进制解析为分数。

## 设计

- 每条规则 `u = k * v`（`k` 为正有理数）是有向加权图中的一条边，反向边自动以 `1/k` 加入。
- 每个连通分量维护势能表 `ratio`：分量内单位 `x` 满足 `1 x = ratio[x] · (1 root)`。
  同一分量 = 同一维度；不同分量 = 不同维度，查询明确拒绝。
- **批量事务**：一批规则在副本上依次应用；若某条规则两端已在同一分量且
  `ratio[u] != k · ratio[v]`（精确比较），构成矛盾环，整批拒绝，图保持原样，
  并输出矛盾路径（新规则 + 既有规则构成的环）。
- **版本化**：每个成功提交的批次生成一个不可变快照版本（版本 0 为空图），
  查询可指定 `version` 重放历史状态。

## 运行

```sh
python3 unitconvert.py            # 从 stdin 读 JSON Lines，向 stdout 写 JSON Lines
python3 unitconvert.py --file cmds.jsonl
python3 -m unittest -v test_unitconvert   # 运行测试
```

## 输入格式（每行一个 JSON 对象）

| 操作 | 字段 | 说明 |
|---|---|---|
| `add_batch` | `rules: [[unit, factor, unit], ...]` | 原子提交一批规则 `unit = factor · unit`；`factor` 为十进制或 `p/q` 字符串，必须为正 |
| `convert` | `value`, `from`, `to`, 可选 `version` | 把 `value`（`from` 单位）换算为 `to` 单位 |
| `factor` | `from`, `to`, 可选 `version` | 返回 `1 from = f · to` 的因子 `f` |
| `versions` | — | 返回当前版本号 |

## 输出格式（每行一个 JSON 对象）

- 成功：`{"ok": true, "version": N}` / `{"ok": true, "result": "42", ...}`，
  结果为精确分数或整数字符串（如 `"1/6"`）。
- 矛盾拒绝：`{"ok": false, "error": "contradiction", "cycle": ["A =5 C", "A =2 B", "B =3 C"], "detail": "..."}`
  `cycle` 首元素为被拒绝的新规则，其余为推出矛盾因子的既有路径。
- 维度不同：`{"ok": false, "error": "dimension_mismatch", ...}`
- 单位未定义：`{"ok": false, "error": "unknown_unit", ...}`
- 非法因子 / 非法请求 / 非法 JSON：`invalid_factor` / `bad_request` / `bad_json`

## 示例会话（验收边界）

```
> {"op":"add_batch","rules":[["A","2","B"],["B","3","C"]]}
< {"ok": true, "version": 1}
> {"op":"convert","value":"7","from":"A","to":"C"}
< {"ok": true, "result": "42", "version": 1}        # A = 6C
> {"op":"convert","value":"1","from":"C","to":"A"}
< {"ok": true, "result": "1/6", "version": 1}       # C = A/6
> {"op":"add_batch","rules":[["A","5","C"]]}
< {"ok": false, "error": "contradiction",
   "cycle": ["A =5 C", "A =2 B", "B =3 C"],
   "detail": "... imply A =6 C"}                    # 整批拒绝，图不变
> {"op":"convert","value":"1","from":"A","to":"C"}
< {"ok": true, "result": "6", "version": 1}         # 原图不受影响
```

所有示例数据均为合成数据。
