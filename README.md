# 批量API成本预算器（batch cost budgeter）

将一组作业划分为若干批次，每个作业恰好被打包一次，最小化总成本。
纯 Python 3.11 标准库实现，无第三方依赖。所有示例数据均为合成数据。

## 运行

```bash
python3.11 budgeter.py examples/same_tenant.json          # 文本输出
python3.11 budgeter.py examples/tiered_mixed.json --json  # JSON 输出
cat examples/cross_tenant.json | python3.11 budgeter.py - # 标准输入
python3.11 -m unittest test_budgeter -v                   # 运行测试
```

退出码：`0` 正常；`1` 校验失败（覆盖/合法性/枚举对照不一致）；`2` 输入非法。

## 打包规则

- 同批次作业必须同租户（严格禁止跨租户混包）、同端点；
- 同批次载荷之和不得超过 `max_payload`；
- 每包成本 = `fixed_fee` + 载荷阶梯费：按包内总载荷命中的第一个
  （`up_to` 最小的）阶梯收 `fee`；超过所有阶梯上限时按最高阶梯收费；
- 每个作业恰好属于一个批次。

## 求解与最优性

- 按 (租户, 端点) 分组后各组独立求解（组间不可混包，成本可加）；
- 组内作业数 ≤ `exact_threshold`（默认 14）时用子集 bitmask DP 精确求解，
  输出标注“已证最优”；
- 超过阈值时退化为 FFD 启发式，输出明确标注“未证最优”；
- 总作业数 ≤ `verify_threshold`（默认 10）时，额外用独立的全集合划分枚举
  （restricted growth strings）交叉验证最小成本，并校验“每个作业恰好覆盖一次”
  与“无跨租户/跨端点/超载”。规模超过阈值时输出明确说明未做枚举对照。

## 输入格式（JSON）

```json
{
  "config": {
    "max_payload": 4,
    "fixed_fee": 3,
    "payload_tiers": [{"up_to": 2, "fee": 0}, {"up_to": 4, "fee": 1}],
    "exact_threshold": 14,
    "verify_threshold": 10
  },
  "jobs": [
    {"id": "j1", "tenant": "tenant-a", "endpoint": "chat", "payload": 2}
  ]
}
```

- `max_payload`（必填）：单包载荷上限，正整数；
- `fixed_fee`（可选，默认 0）：每包固定费；
- `payload_tiers`（可选，默认空即无变动费）：阶梯列表，`up_to` 为阶梯上限、
  `fee` 为该阶梯变动费；
- `exact_threshold` / `verify_threshold`（可选）：精确求解与枚举对照的规模阈值；
- `jobs[].id` 必须唯一；`payload` 为 `[0, max_payload]` 内的整数，否则报错。

## 输出格式

文本输出包含：最优性状态、总成本、批次数、每包明细
（租户/端点/载荷/成本/作业列表）、覆盖与合法性校验结果、枚举对照结果。

`--json` 输出结构：

```json
{
  "optimal": true,
  "total_cost": 3,
  "num_batches": 1,
  "notes": [],
  "batches": [
    {"jobs": ["j1", "j2"], "tenant": "tenant-a", "endpoint": "chat",
     "payload": 4, "cost": 3}
  ],
  "verification": {
    "coverage_ok": true,
    "legality_ok": true,
    "problems": [],
    "enumeration": {"partitions_checked": 2, "min_cost": 3,
                    "solver_cost": 3, "consistent": true}
  }
}
```

## 验收边界（已被测试覆盖）

- 同租户两载荷各 2、最大载荷 4、固定费 3、无变动费 → 合包，总成本 3；
- 两作业不同租户 → 必须两包，总成本 6。
