# shard-tx: 跨表分片事务协调器

业务事务横跨多个表分片，按清单（manifest）齐备后**一起发布**；超时仅标记
**未决（PENDING）**，绝不部分提交。变更来自本地 JSON 夹具，不接消息平台。
仅使用 Python 3.11 标准库与 unittest。

## 核心规则

- 清单声明事务 ID、期望的表集合（如 `{A, B}`）与 TTL，并由此算出摘要（SHA-256）。
- 分片携带事务 ID、表、主键、前后值与提交序号，以及清单摘要。
- 只有清单中每张表都各有一个已接受分片时才发布；发布按提交序号排序后原子落库。
- 只到 A 不发布；A 的重复分片被忽略，不能顶替缺失的 B。
- 分片摘要与已注册清单冲突时拒绝（`ManifestConflictError`）。
- 超时清扫只把未完成事务标为 PENDING；PENDING 是"未决"，迟到的分片仍可
  补齐清单并发布。

## 结构

- `shard_tx/models.py` — `Change` / `Shard` / `Manifest` / `TxState`
- `shard_tx/coordinator.py` — `ShardCoordinator`（注册清单、接收分片、超时清扫、原子发布）
- `shard_tx/store.py` — `TableStore`，`apply_all` 先校验后落库，全部或全不
- `shard_tx/fixtures.py` — 本地 JSON 夹具加载
- `fixtures/` — 示例夹具（`tx_1001.json` 齐备；`tx_1002_only_a.json` 只到 A）
- `tests/test_coordinator.py` — unittest 验收用例
- `run_sample.py` — 运行样例

## 运行

```sh
python3 -m unittest discover -s tests -v   # 测试
python3 run_sample.py                      # 样例演示
```
