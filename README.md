# 异步税率版本重算（合成数据演示）

> 免责声明：本组件仅实现**合成规则**，用于演示税率版本重算流程，
> **不提供任何真实税务建议**。所有示例数据均为合成数据。

## 需求与环境

- Python 3.11（仅标准库：`fractions`、`asyncio`、`dataclasses`、`argparse`、`json`、`unittest`）

## 公开舍入定义（契约）

- 行税额：`round_half_even(net_cents × rate)`，使用 `fractions.Fraction` 做**有理数精确计算**后**半偶舍入**（ties-to-even）。
- 整票税额：**各行税额之和**。
- 注意：逐行舍入与"合计后舍入"不等价。例如税率 1/2、两行各 1 分时，
  逐行结果为 `0 + 0 = 0`，而合计后舍入为 `round(2 × 1/2) = 1`。
  本组件固定采用逐行定义，且该定义是公开、稳定的契约。
- 验收边界：净额 1 分、税率 1/2 → 0；净额 3 分、税率 1/2 → 2。

## 领域模型

- **发票（Invoice）**：不可变。由净额行（整数分）、税率版本、舍入策略（`half_even`）、
  司法辖区、开票日期组成。
- **税率版本（TaxRate）**：按司法辖区 + 生效区间 `[valid_from, valid_to)` 匹配；
  `valid_to` 为 `null` 表示开放区间。计算时按发票指定的 `rate_version` 精确锁定版本。
- **迟到修订（Revision）**：生成**冲销**（原行取负、引用原票）与**重开**发票
  （同净额行、新税率版本）。原票永不被修改。
- **幂等**：同一 `(invoice_id, revision_id)` 重放不会再次冲销，返回首次生成的单据。
- **异步**：`RecalculationService` 基于 `asyncio.Queue`，修订入队后由 worker 异步应用。

## 命令行用法

```bash
# 计算单张发票（逐行税额 + 合计）
python3.11 taxcalc.py compute examples/compute.json

# 应用迟到税率修订（异步冲销 + 重开；示例中同一修订重放一次以演示幂等）
python3.11 taxcalc.py revise examples/revise.json

# 运行测试
python3.11 -m unittest test_taxcalc -v
```

## 输入/输出格式

### `compute` 输入（JSON）

```json
{
  "invoice": {
    "id": "INV-100",
    "jurisdiction": "SYNTH",
    "issued_on": "2026-01-15",
    "rate_version": "v1",
    "rounding": "half_even",
    "lines": [{"description": "synthetic widget", "net_cents": 1}]
  },
  "rates": [
    {"version": "v1", "jurisdiction": "SYNTH", "numerator": 1, "denominator": 2,
     "valid_from": "2026-01-01", "valid_to": "2026-07-01"}
  ]
}
```

- `net_cents`：整数（分）。税率用 `numerator`/`denominator` 表示为有理数。
- `valid_to` 可为 `null`（开放区间）；区间左闭右开。

### `compute` 输出（JSON）

```json
{
  "id": "INV-100", "kind": "original", "references": null,
  "jurisdiction": "SYNTH", "issued_on": "2026-01-15",
  "rate_version": "v1", "rounding": "half_even",
  "lines": [{"description": "synthetic widget", "net_cents": 1, "tax_cents": 0}],
  "total_net_cents": 1, "total_tax_cents": 0
}
```

### `revise` 输入

在 `compute` 输入基础上增加：

```json
"revisions": [{"id": "REV-1", "new_rate_version": "v2"}]
```

### `revise` 输出

```json
{"documents": [
  {"invoice_id": "INV-100", "revision_id": "REV-1", "created": true,
   "reversal": { ... "kind": "reversal", "references": "INV-100",
                 "lines": [{"net_cents": -1, "tax_cents": 0} ...] ...},
   "reissue":  { ... "kind": "reissue",  "references": "INV-100",
                 "rate_version": "v2" ...}}
]}
```

- `created: false` 表示该修订为重放，未生成新冲销（返回首次结果的引用）。

## 文件结构

- `taxcalc.py` — 核心引擎 + CLI（舍入、税率匹配、修订引擎、异步服务）
- `test_taxcalc.py` — unittest 测试（舍入边界、逐行 vs 合计、区间匹配、冲销/重开、幂等、异步）
- `examples/` — 合成示例输入
