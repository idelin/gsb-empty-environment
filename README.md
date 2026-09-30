# BM25：显式公式 + 可增删统计 + 块级 TopK 剪枝

纯 Python 3.11 标准库实现，不使用任何搜索服务或全文检索库。

## 文件

- `bm25.py` — 索引、快照、打分、块级剪枝（含全部规则说明）
- `test_bm25.py` — unittest 测试套件（16 个用例）
- `demo.py` — 本地合成 UTF-8 语料的运行样例

## 运行

```bash
python3.11 demo.py                  # 运行样例
python3.11 -m unittest test_bm25 -v # 测试
```

## 分词规则（查询与文档一致）

1. 先对整段文本做 Unicode `casefold`；
2. token = 极大连续的 Unicode 字母/数字序列（正则 `[^\W_]+`），下划线、空白、标点均为分隔符；
3. 不做词干化、不去停用词、不做 CJK 切分（`我爱北京` 是 1 个 token，`我爱 北京` 是 2 个）。

## 打分公式（显式 BM25）

```
score(q, d) = Σ_{t ∈ q} idf(t) · tf(t,d)·(k1+1) / (tf(t,d) + k1·(1−b+b·|d|/avgdl))
idf(t)      = ln(1 + (N − df(t) + 0.5) / (df(t) + 0.5))    # Robertson IDF，恒 ≥ 0
```

- **重复查询词**：q 是多重集合，词出现 m 次累加 m 次，即 `score([t,t],d) = 2·score([t],d)`，可手算复核。
- **空文档**：`|d|=0` 时 tf 恒为 0，贡献为 0；全库皆空（avgdl=0）时归一项退化为 `k1·(1−b)`，不除零。
- **排名**：按 `(-score, doc_id)` 排序，同分按 ID 升序。

## 增删与快照

- `add_document` / `delete_document` 维护文档长度、词频、文档频率，并使 `version + 1`；删除后 `avgdl` 立即更新。
- `snapshot()` 固化当前统计（N、avgdl、df）与倒排数据；之后的增删不影响旧快照。
- 块上界缓存以 `(version, 查询, 块号)` 为键：统计变化 ⇒ version 变化 ⇒ 旧上界键永不命中，**不会复用失效的得分上界**。

## 块级 TopK 剪枝

快照把按 doc_id 升序的文档切成定长块。块 B 的上界：

```
UB(B) = Σ_{t ∈ q} m_q(t) · max_{d ∈ B} contrib(t, d)   # 逐项精确最大值，非近似
```

任意 `d ∈ B` 的得分 ≤ UB(B)，故上界正确。剪枝规则（阈值 = 当前第 k 名的 `(score, doc_id)`）：
`UB < 阈值分数`，或 `UB == 阈值分数 且 块内最小 doc_id ≥ 阈值 doc_id`（同分按 ID 升序），
则整块跳过。`topk_pruned` 返回逐块审计轨迹（UB + 决策时阈值），即未读块不可能胜过阈值的证据；
结果与 `topk_full` 全量评分完全一致（测试强制构造了“最优项在最后一块”和“高频词更新”场景，并做随机化对照）。
