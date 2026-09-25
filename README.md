# rulecov — 规则覆盖 / 不可达 / 同优先冲突分析器

对一组带优先级的规则做静态分析。条件只支持两类有限域：**有限枚举**与
**整数闭区间**。所有反例都输出**具体输入**，所有证据（分析步骤）带稳定 ID，
可通过 `verify` 子命令完整重放。仅依赖 Python 3.11 标准库。

## 分析语义

- **不可达规则（unreachable_rule）**：规则的条件区域被**严格更高优先级**
  规则的条件区域之并集**完全覆盖**，标记为不可达，并给出覆盖它的规则集合。
- **部分重叠（partial_overlap）**：只被高优先级规则部分覆盖时，**不**标不可达，
  只为每对重叠规则输出一个落在**重叠区域内**的具体反例输入（该输入下高优先级
  规则胜出）。
- **同优先冲突（same_priority_conflict）**：优先级相同、动作不同且条件区域相交
  的规则对，输出落在交集中的具体反例输入（该输入下胜者不唯一）。
  动作相同的重叠视为冗余，不报冲突。
- **预算（budget）**：每次集合运算（相交/差集）消耗 1 单位预算并记录为一步。
  预算耗尽时，受影响阶段标记为 `unknown`，报告 `complete=false`，退出码为 1；
  **绝不**在未检查完时声称不存在冲突。

## 稳定 ID 与版本

- 事实、规则、规格本身都带 `id` 与 `version`（见 `examples/sample_spec.json`）。
- 每个分析步骤带确定性 ID（`S0001`、`S0002`……），finding 通过
  `evidence.steps` 引用相关步骤。
- 报告带 `report_id`（内容哈希）与规格的 `sha256`，同样的输入永远产生
  字节级一致的报告。

## 运行

```console
$ python3 -m rulecov analyze examples/sample_spec.json -o examples/report.json
spec: shipping-fees v3 (sha256: c73589c0eb43...)
budget: used 38 / 100000 (exhausted: False)
phases: coverage=complete overlap=complete conflict=complete
complete: True
findings: 12
  F-UNREACHABLE-R3: rule R3 fully covered by [R1, R2]
  F-OVERLAP-R2-R1: R2 shadowed by R1 e.g. {"customer_tier": "gold", "order_total": 500, "destination": "domestic"}
  ...
  F-CONFLICT-R6-R7: R6 vs R7 at priority 50 e.g. {"customer_tier": "bronze", "order_total": 0, "destination": "domestic"}
```

重放验证（重算全部 38 步并逐条核验反例证人）：

```console
$ python3 -m rulecov verify examples/sample_spec.json examples/report.json
verification OK: report RPT-5344650bb80ebb38 replays (38 steps)
```

预算耗展示例（注意冲突阶段为 `unknown`，退出码 1）：

```console
$ python3 -m rulecov analyze examples/sample_spec.json --budget 20
spec: shipping-fees v3 (sha256: c73589c0eb43...)
budget: used 20 / 20 (exhausted: True)
phases: coverage=complete overlap=unknown conflict=unknown
complete: False
findings: 2
  F-UNREACHABLE-R3: rule R3 fully covered by [R1, R2]
  F-OVERLAP-R2-R1: R2 shadowed by R1 e.g. {"customer_tier": "gold", ...}
$ echo $?
1
```

## 样例数据（合成）

`examples/sample_spec.json` 是合成的运费规则（spec `shipping-fees` v3）：

- `R3`（金牌且金额 500–10000）被 `R1`（金牌即免运费）完全覆盖 → 不可达。
- `R5`（金额 1000–3000 固定运费）与 `R1`、`R2` 部分重叠 → 输出重叠反例。
- `R6`（国内小额标准费）与 `R7`（小额折扣）同优先级 50、动作不同、
  区域相交 → 冲突，反例 `{customer_tier: bronze, order_total: 0, destination: domestic}`。

## 测试

```console
$ python3 -m unittest discover -s tests -v
...
----------------------------------------------------------------------
Ran 32 tests in 0.005s

OK
```

测试覆盖：区域代数（枚举/区间的交与差）、不可达判定、部分重叠反例的
具体有效性（证人同时命中两条规则且高优先级胜出）、同优先冲突、
同动作不误报、预算耗尽的诚实性（不报"无冲突"）、跨运行确定性、
以及证据重放（篡改证人/步骤日志/删除 finding 均被检出）。

## 代码结构

- `rulecov/regions.py` — 枚举集合与整数区间的区域代数（交、差、证人选取）。
- `rulecov/model.py` — 带 ID/版本的规格模型、校验、具体输入求值。
- `rulecov/analyze.py` — 三阶段分析、预算控制、步骤记录、报告生成。
- `rulecov/evidence.py` — 报告重放与证人独立核验。
- `rulecov/__main__.py` — `analyze` / `verify` CLI。
- `examples/` — 合成样例规格与已生成的报告。
- `tests/` — unittest 测试套件。
