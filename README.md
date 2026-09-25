# group-quota-sampler

按分组配额的确定性抽样。纯 Python 3.11 标准库实现，测试使用 `unittest`，
不依赖任何现成查询引擎。

## 核心规则

每个可见行由稳定行 ID 与显式 seed 计算确定性随机键：

```
key = blake2b-128(seed ‖ encode(row_id))
```

每个组内按 `(key, encode(row_id))` 升序取前 `quota` 行作为样本。由此得到：

- **批次/顺序无关**：样本只取决于可见行集合，与批次切分、输入顺序无关；
- **分片可合并**：全局 top-k 必然包含于各分片 top-k 的并集，因此分片导出
  每组 top-quota 候选（带键），合并端对并集重新排序取 top-k——
  *不是*把各片样本拼接后均匀截取；
- **撤回可补足**：删除已入样行后，按键序的下一个合法候选自动补上；
- **checkpoint 可恢复**：快照保存完整可见键索引，恢复后继续处理等于
  独立全量参考；
- **配额可变**：键与配额解耦，改配额后输出等于同数据独立全量运行。

## 语义定义

- **版本化撤回**：插入带版本 `v_ins`；版本 `v` 的撤回隐藏所有 `v_ins <= v`
  的插入。更高版本的再次插入使行重新可见（可见 ⟺ `v_ins > v_retract`）。
- **组不足**：返回 `actual`（实际样本量）与 `deficit = max(0, quota - actual)`。
- **配额 0**：样本为空，`actual = deficit = 0`。
- **配额大于组大小**：`actual = 组大小`，`deficit = quota - 组大小`。
- **null 与 NaN**：`None`（null）与 `float('nan')` 是不同的分组键/值。
- **整数精确**：`int` 列按 Python 整数精确处理（不经过 float），bool 不算 int。
- **表输入**：`TableBatch(columns, rows)`，`Column(name, type)`，
  类型 ∈ `{int, float, str, bool, bytes}`，逐值校验；null 允许出现在任何列。

## 文件

- `group_quota_sampler/encoding.py` — 规范化编码、随机键、JSON 值编解码
- `group_quota_sampler/table.py` — 带列类型的批次输入与校验
- `group_quota_sampler/sampler.py` — 采样器、分片导出/合并、checkpoint
- `tests/test_sampler.py` — 20 个 unittest 用例
- `demo.py` — 运行样例

## 运行

```sh
python3.11 demo.py
python3.11 -m unittest discover -s tests -v
```

## 样例输出（`demo.py`，seed=20260925）

```
== full data, batches of 5 ==
  group=('eng',)   quota=3 actual=3 deficit=0 row_ids=[5, 8, 7]
  group=('lab',)   quota=5 actual=2 deficit=3 row_ids=[16, 15]
  group=('ops',)   quota=2 actual=2 deficit=0 row_ids=[14, 11]

batch-split invariant: True
shard merge == full reference: True

== after retracting emp_id=5 (v2) ==
  group=('eng',)   quota=3 actual=3 deficit=0 row_ids=[8, 7, 2]
  group=('lab',)   quota=5 actual=2 deficit=3 row_ids=[16, 15]
  group=('ops',)   quota=2 actual=2 deficit=0 row_ids=[14, 11]
repaired == full reference on visible rows: True
quota change == independent full run: True
checkpoint restore == original: True
```

## 测试结果

```
Ran 20 tests in ~0.8s
OK
```

覆盖：批次切分不变性、输入顺序不变性、显式 seed 稳定性、组不足缺额、
配额 0 / 超组大小、配额变更等于独立全量参考、null 与 NaN 区分、大整数
精确性（>2^63）、schema 校验、随机分片合并等于全量参考、合并非"拼接后
截取"的构造性反例、删除入样行后从候选补足、版本化撤回乱序语义、
checkpoint JSON 往返恢复等于全量参考、小域穷举随机键的纳入判定
（纳入 ⟺ 键秩 < k）以及纳入频率 ≈ quota/N 的统计检验（5σ 容差）。
