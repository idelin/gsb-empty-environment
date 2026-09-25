# detsample — 确定性分组配额采样

纯 Python 3.11 标准库实现，无任何第三方依赖、不调用现成查询引擎。

## 核心规则

每行的随机键 `key = BLAKE2b-128(seed, row_id)`，只依赖显式 seed 与稳定行 ID，
与输入顺序、批次切分、其他行无关。每组的样本 = 可见行中键最小的 `quota` 行
（并列时按行 ID 编码打破，保证全序确定）。由此直接得到：

- **批次切分 / 输入顺序无关**：样本是 `(seed, quotas, 可见行集)` 的纯函数；
- **分片合并 = 并集上取 k 小键**：各分片导出全部候选（而非仅 top-k），
  合并后等价于对完整可见数据单次处理；绝不"拼接各片样本后均匀截取"；
- **撤回自动补足**：候选全部按键序保留，删除已入样行后从合法候选依次递补；
- **组不足**：返回 `actual`（实际样本量）与 `shortfall`（缺额）；
  `quota = 0` 返回空样本，`quota > 组大小` 返回全组并报缺额。

## 带版本的行撤回

行携带版本号；`retract(row_id, v)` 隐藏所有 `version <= v` 的行版本，
更晚到达的 `version > v` 重新可见。tombstone 单调（只保留最大版本），
且即使对应行尚未到达也会保留，因此撤回可跨分片、跨 checkpoint 正确合并。

## 类型化批次输入

`Batch(columns, rows)` 按列类型校验：`int`（任意精度精确计算，拒绝 bool）、
`float`（NaN 合法且与 null 不同）、`str`、`bool`、`any`。
`None`（null）与 `NaN` 编码不同、永不相等；整数编码为精确十进制，
`2**53` 与 `2**53+1` 永不混淆。

## 文件

- `detsample/values.py` — 类型化值的规范编码（null ≠ NaN，整数精确）
- `detsample/table.py` — 带列类型的批次输入
- `detsample/sampler.py` — 采样器：分片导出/合并、版本化撤回、checkpoint
- `example.py` — 端到端运行样例
- `test_detsample.py` — unittest 测试（27 个用例）

## 运行

```bash
python3.11 example.py          # 运行样例
python3.11 -m unittest -v      # 运行测试
```

测试覆盖：批次切分/输入顺序不变性、分片合并等于全量参考（含 concat+truncate
反例）、撤回补足与版本语义、checkpoint 恢复等于独立全量参考、配额变更、
配额 0 与超组大小、null/NaN 区分、整数精确性，以及小域穷举随机键的
纳入概率精确检验（rotation 键空间下每行纳入次数 == quota，精确等于 quota/n）
和大样本统计频率检验。
