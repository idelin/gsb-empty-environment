# shardstitch：分片拼接验证器

各分片提供**局部结论**与**边界假设**；拼接时要求每个假设都由另一分片的
**有效证据**满足，并检测互相假设却无根支撑的环。纯 Python 3.11+ 标准库实现，
测试使用 unittest，样例数据为合成数据。

## 设计要点

- **稳定 ID 与版本**：`Fact` / `Rule` / `Step` / `Proof` / `Shard` 均以
  `(id, version)` 寻址；规则可同 ID 多版本共存，步骤按版本精确引用。
- **证据可重放**：`replay()` 从局部事实与假设出发，按步骤重新执行确定性算子
  （`join` / `concat` / `sum` / `count`），重放不出结论即拒绝。
- **内容寻址摘要**：证据摘要 = 规范化 JSON 的 SHA-256。假设声明
  `expected_digest`，拼接时**摘要不匹配的边界证据一律拒绝**
  （`BOUNDARY_DIGEST_MISMATCH`）。
- **无根互假设不能成立**：采用良基接地（well-founded grounding）不动点——
  无假设的证明是根，假设须由已接地且摘要匹配的证据满足。由于摘要内容寻址，
  "A 假设 B 且 B 假设 A" 无法构造出互相匹配的摘要（需要哈希不动点），
  必然不被接地；残留的图级环由 Tarjan SCC 检测并报告
  （`ASSUMPTION_CYCLE_NO_ROOT`）。
- **重复不重复计贡献**：同 `(shard_id, version)` 且内容摘要相同的重复提交被
  忽略；不同分片携带相同证明（同摘要）时全局只计一次贡献。同 ID 同版本但
  内容不同的分片按 `CONFLICTING_SHARD_VERSION` 拒绝。

## 拒绝原因

| 原因 | 含义 |
| --- | --- |
| `REPLAY_FAILURE` | 证据无法重放（步骤/规则/结论不一致） |
| `UNSATISFIED_ASSUMPTION` | 假设无任何分片提供证据 |
| `BOUNDARY_DIGEST_MISMATCH` | 有提供方但证据摘要不匹配 |
| `UNGROUNDED_PROVIDER` | 摘要匹配但提供方自身未接地 |
| `ASSUMPTION_CYCLE_NO_ROOT` | 互相假设构成无根环 |
| `CONFLICTING_SHARD_VERSION` | 同 ID 同版本分片内容冲突 |

## 运行

```bash
python3 examples/run_sample.py          # 合成样例：正常链 + 拒绝场景
python3 -m unittest discover -s tests -t .   # 测试（15 个用例）
```

## 结构

- `shardstitch/model.py` — 带 ID/版本的实体与规范化摘要
- `shardstitch/replay.py` — 确定性证据重放
- `shardstitch/stitch.py` — 拼接：接地不动点、环检测、贡献去重
- `shardstitch/synthetic.py` — 合成样例数据（ingest → analytics → reporting）
- `examples/run_sample.py` — 可运行样例
- `tests/test_stitch.py` — unittest 测试
