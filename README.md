# 同键连续变更压缩（可见性安全的 Change-Log Compaction）

Python 3.11 + 标准库 + `unittest`，数据全部来自本地夹具（事务ID、表、主键、
前后值、提交序号），不接任何消息平台。

## 压缩规则

- 仅合并同一提交点内、同表、同主键、连续（前值可校验）的变更。
- 禁止跨越四类屏障：`commit_seq`（已声明可见点/事务屏障）、表（schema
  屏障）、主键（实体屏障）、`txn_id`（同提交内事务ID不一致直接拒绝）。
- `A → B → C` 合并为 `A → C`（保留最早前值与最终状态）。
- 插后删净效果为不存在，变更可消去；审计模式额外保留每一步的前/后值证据，
  非审计模式至少保留来源 `idx` 列表。
- 删除后重建在中间值 `ABSENT` 处切开，输出 `DELETE + INSERT`，绝不伪造
  `UPDATE`；实体重建获得新的身份世代（`table:key#gN`），下游 churn 聚合
  （inserts/deletes）也随之保留。
- 跨提交点的同键变更绝不合并，中间已声明可见版本不会被隐藏。
- 预算（`budget`）限制可消失的原始变更数；一旦超限，后续片段按原样保留，
  不做压缩（`budget_exhausted=true`）。

## 等价性（压缩前后必须对所有声明可见提交点成立）

每个声明的 `commit_seq` 上同时比较：

1. 最终状态（每行最终值）；
2. 实体身份（身份世代，删除重建 ≠ 原地更新）；
3. 前值验证（重放时逐条校验 `before` 与物化状态一致）；
4. 下游增量聚合效果（行数 count、数值列 sum、inserts/deletes/updates churn）。

## 等价证书

`build_certificate` 生成自包含 JSON：原始流、压缩流、消去证据、所有提交点
快照视图、各部分 SHA-256 摘要和 HMAC 签名。`verify_certificate` 不相信证书
内嵌快照，而是从原始流与压缩流分别从零重放，重算视图、校验前值、摘要和签名，
并检查每条融合记录的来源是否跨越任何屏障。

```bash
python3.11 -m changelog_compaction.demo                      # 运行样例（含反例）
python3.11 -m changelog_compaction.demo emit mixed cert.json # 导出证书
python3.11 -m changelog_compaction.demo verify cert.json     # 独立重放校验
python3.11 -m unittest changelog_compaction.test.test_compaction -v
```

## 反例（最终值相同、中间语义不同）

- **跨提交隐藏中间版本**：提交1/2/3 上的 `None→A→B→C` 不能压成提交1的
  `None→C`；提交2 的可见状态与累计聚合立即不一致。
- **删除重建伪装成更新**：`A→删除→C` 最终值也是 C，但身份世代不同
  （`#g2` vs `#g1`），inserts/deletes churn 也不同。

## 穷举核验

测试对单个实体长度 1–4 的全部合法状态链（ABSENT/A/B/C，相邻必变化，
共 160 条路径：4+12+36+108）分别在审计/非审计模式下压缩，并断言两个流在所有提交点的状态、
身份、聚合完全一致。

## 文件

- `changelog_compaction/changelog.py`：变更模型、提交重放、身份世代、增量聚合。
- `changelog_compaction/compaction.py`：屏障感知的压缩器、预算与审计证据。
- `changelog_compaction/certificate.py`：证书生成与独立重放校验、反例分析。
- `changelog_compaction/fixtures.py`：本地夹具。
- `changelog_compaction/demo.py`：运行样例 / emit / verify CLI。
- `changelog_compaction/test/test_compaction.py`：19 个 unittest 用例。
- `samples/`：证书样例、演示输出、测试结果存档。
