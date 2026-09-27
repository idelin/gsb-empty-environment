# 批量 API 成本预算器（batch_budgeter）

将有限作业按合法分组打包，求**覆盖每个作业恰好一次**的最低成本打包方案。
纯 Python 3.11 标准库实现，无第三方依赖。所有示例数据均为合成数据。

## 规则

- 分组兼容性：同一包内所有作业必须 **同租户**（严格禁止跨租户混包）、**同端点**，
  且载荷之和不超过 `max_payload`。
- 每包成本 = `fixed_fee`（固定费）+ 载荷阶梯费。阶梯表 `payload_tiers` 为
  `[[上限, 费用], ...]`，总载荷落入第一个 `上限 >= 总载荷` 的档位收取对应费用；
  空列表表示无变动费。
- 单个作业载荷超过 `max_payload` 时问题无可行解，程序报错退出。

## 求解与验证

- 按 `(租户, 端点)` 分解为独立子问题，每个子问题用子集 DP 求**精确最优**
  （同一兼容类作业数上限 22，复杂度约 O(3^n)）。
- 小规模（作业数 ≤ 10）时，默认用**独立实现**的暴力枚举（受限增长串枚举全部
  集合划分）对照 DP 结果，输出 `verification.status = confirmed`；
  超过该规模则标记 `skipped` 并说明最优性未经独立枚举验证。

## 使用方法

```bash
python3 batch_budgeter.py input.json          # 从文件读取
cat input.json | python3 batch_budgeter.py -  # 从标准输入读取
python3 batch_budgeter.py input.json --no-verify   # 跳过暴力枚举对照
python3 -m unittest test_batch_budgeter -v    # 运行测试
```

## 输入格式（JSON）

```json
{
  "max_payload": 4,
  "fixed_fee": 3,
  "payload_tiers": [[4, 0], [8, 10]],
  "jobs": [
    {"id": "a", "tenant": "t1", "endpoint": "/v1/chat", "payload": 2},
    {"id": "b", "tenant": "t1", "endpoint": "/v1/chat", "payload": 2},
    {"id": "c", "tenant": "t2", "endpoint": "/v1/chat", "payload": 2}
  ]
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `max_payload` | int > 0 | 单包最大载荷（包内作业载荷之和上限） |
| `fixed_fee` | number ≥ 0 | 每包固定费 |
| `payload_tiers` | 数组，可省略 | 阶梯档 `[上限, 费用]`，按上限升序；省略或空表表示无变动费 |
| `jobs[].id` | string | 作业标识，必须唯一 |
| `jobs[].tenant` | string | 租户 |
| `jobs[].endpoint` | string | 端点 |
| `jobs[].payload` | int ≥ 0 | 载荷 |

## 输出格式（JSON）

```json
{
  "total_cost": 6.0,
  "package_count": 2,
  "packages": [
    {
      "jobs": ["a", "b"],
      "tenant": "t1",
      "endpoint": "/v1/chat",
      "payload": 4,
      "fixed_fee": 3.0,
      "tier_fee": 0.0,
      "cost": 3.0
    }
  ],
  "verification": {
    "status": "confirmed",
    "dp_total_cost": 6.0,
    "brute_force_total_cost": 6.0
  }
}
```

- `packages[]`：每包的作业明细、租户、端点、总载荷及费用拆解。
- `verification.status`：`confirmed`（DP 与独立暴力枚举一致）/
  `skipped`（附原因）/ `mismatch`（不一致，程序同时以非零码报错）。

## 验收边界示例

见 `examples/acceptance_same_tenant.json` 与 `examples/acceptance_cross_tenant.json`：

- 同租户两载荷各 2、最大载荷 4、固定费 3、无变动费 → 合包，总成本 3；
- 两作业分属不同租户 → 必须两包，总成本 6。

## 文件

- `batch_budgeter.py` — 求解器、暴力枚举对照与 CLI。
- `test_batch_budgeter.py` — unittest 测试（验收边界、合法性、成本模型、
  输入校验、随机小规模 DP 对暴力枚举、CLI 端到端）。
- `examples/` — 合成示例输入。
