# proofdag — 等价证明 DAG 规范化

纯 Python 3.11 标准库实现，无第三方依赖。对等价证明 DAG 做去重、排序、
编号与结构摘要，支持增量删边与可重放证据。

## 结构

- `proofdag/model.py` — `Fact` / `Rule` / `Step`，均带稳定 ID 与版本；
  事实身份包含来源，同值不同来源不合并。
- `proofdag/dag.py` — `ProofDAG`：反向引用、循环/未知前提拒绝、
  增量删边并撤销不再可达的共享子图。
- `proofdag/canon.py` — 规范化：结构哈希cons去重、按结构键排序编号、
  交换律前提排序 / 有序操作保序、与遍历顺序无关的结构摘要；
  摘要碰撞时按规范形式做结构验证（`summaries_equal`）。
- `proofdag/evidence.py` — 证据导出为 JSON、重放重建并校验。
- `sample.py` — 合成数据样例；`tests/` — unittest 测试。

## 运行

```sh
python3.11 sample.py
python3.11 -m unittest discover -s tests -v
```

## 不变量

- 随机重命名内部节点、排列输入顺序 → 同一规范结果与摘要。
- 来源替换 → 摘要必变；交换合取前提 → 摘要不变。
- 顺序敏感（非交换）规则前提交换 → 不合并、摘要不同。
- 多引用子证明在规范形式中只存一次，反向引用完整。
- 循环证明在规范化时抛出 `CycleError`。
