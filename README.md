# 双时态计价回放账本

同时按 **业务生效时间** 与 **系统知悉时间** 查询的价格账本。纯 Python 3.11 标准库实现，无第三方依赖。所有示例数据均为合成数据。

## 语义规则

- **价格区间**：`[valid_from, valid_to)`，左闭右开。
- **冲突消解**：同商品同币种、同一业务时刻上重叠的规则，先按知悉时间 `known_at`、再按修订号 `revision` 取最大者。
- **撤销**：只在撤销被知悉的时刻（含）之后影响查询；此前的历史查询结果不变。
- **金额**：一律为整数分；非整数、负数、布尔值一律拒绝。
- **修订号唯一性**：同 `(product, currency, revision)` 内容不同的插入被拒绝（`ConflictError`）；内容完全相同的重复插入为幂等空操作，保留原知悉时间。
- **证据**：每次查询可输出涉及的全部规则（含已撤销、含未命中区间的），并标注其在查询知悉时间下的撤销状态与区间命中情况。
- **缓存**：查询结果按 `(product, currency, business_time, knowledge_time)` 缓存；插入迟到修订或撤销时，仅失效「同商品同币种 ∧ 业务时间落在变更区间 ∧ 查询知悉时间 ≥ 变更知悉时间」的缓存项。缓存不持久化。

## 文件

- `bitemporal_ledger.py` — 核心库（`Ledger` 类）+ 命令行入口
- `test_bitemporal_ledger.py` — unittest 测试（21 个用例）

## 命令行用法

状态持久化在 `--db` 指定的 JSON 文件中（默认 `ledger.json`）。

```bash
# 插入：价格 10 分，业务 [0,20)，修订 1，知悉时间 2
python3 bitemporal_ledger.py --db ledger.json insert \
  --product SKU1 --currency USD --amount 10 \
  --valid-from 0 --valid-to 20 --revision 1 --known-at 2
# 输出: {"status": "ok", "known_at": 2}

# 插入迟到修订：价格 15 分，业务 [5,10)，修订 2，知悉时间 8
python3 bitemporal_ledger.py --db ledger.json insert \
  --product SKU1 --currency USD --amount 15 \
  --valid-from 5 --valid-to 10 --revision 2 --known-at 8

# 查询：业务时间 7、知悉时间 7 → 10；知悉 8 → 15；业务 10 → 10
python3 bitemporal_ledger.py --db ledger.json query \
  --product SKU1 --currency USD --business-time 7 --knowledge-time 7

# 撤销修订 2，知悉时间 9
python3 bitemporal_ledger.py --db ledger.json revoke \
  --product SKU1 --currency USD --revision 2 --known-at 9
```

`--known-at` 缺省时使用账本内部逻辑时钟（每次写操作自增 1）。

## 输入输出格式

### insert

- 入参：`--product --currency --amount --valid-from --valid-to --revision [--known-at]`
- 正常输出（stdout，JSON 单行）：`{"status": "ok", "known_at": <int>}`
- 冲突/非法输入（stderr，退出码 1）：`{"status": "error", "error": "<原因>"}`

### revoke

- 入参：`--product --currency --revision [--known-at]`
- 正常输出：`{"status": "ok", "revoked_known_at": <int>}`
- 修订不存在：stderr 输出错误，退出码 1

### query

- 入参：`--product --currency --business-time --knowledge-time [--no-evidence]`
- 输出（stdout）：

```json
{
  "product": "SKU1",
  "currency": "USD",
  "business_time": 7,
  "knowledge_time": 8,
  "amount": 15,
  "winner": {"product": "SKU1", "currency": "USD", "amount": 15,
             "valid_from": 5, "valid_to": 10, "revision": 2, "known_at": 8},
  "evidence": [
    {"product": "SKU1", "currency": "USD", "amount": 10, "valid_from": 0,
     "valid_to": 20, "revision": 1, "known_at": 2,
     "revoked": false, "in_business_range": true}
  ]
}
```

- `amount` / `winner`：无命中规则时为 `null`。
- `evidence`：本次查询涉及的全部规则（同商品同币种且在查询知悉时间前已知悉），每条附 `revoked`（查询知悉时间下是否已撤销）与 `in_business_range`（业务时间是否落入区间）。

## 库 API

```python
from bitemporal_ledger import Ledger

ledger = Ledger()
ledger.insert("SKU1", "USD", 10, 0, 20, revision=1, known_at=2)
ledger.insert("SKU1", "USD", 15, 5, 10, revision=2, known_at=8)
result = ledger.query("SKU1", "USD", business_time=7, knowledge_time=8)
result.amount    # 15
result.winner    # 命中的 PriceRule
result.evidence  # list[EvidenceEntry]，证据列表
ledger.revoke("SKU1", "USD", revision=2, known_at=9)
```

持久化：`ledger.dump()` → dict（可 JSON 序列化）；`Ledger.load(data)` 还原。

## 测试

```bash
python3 -m unittest test_bitemporal_ledger -v
```
