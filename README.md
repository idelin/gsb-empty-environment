# TopK 建议器（压缩前缀树）

纯 Python 3.11+ 标准库实现的前缀 TopK 建议，不依赖任何搜索服务或全文检索库。
文档为本地合成 UTF-8 文本，由内置分词器切词。

## 结构

- `topk/tokenizer.py` — 分词与规范化
- `topk/trie.py` — 压缩前缀树（radix trie）+ 带版本的 TopK 上界缓存
- `topk/suggester.py` — 建议器 API：词频增删改、排名/字典序分页、内存压缩
- `demo.py` — 运行样例（`python3 demo.py`）
- `tests/test_suggester.py` — unittest（`python3 -m unittest discover -s tests -v`）

## 分词规则

1. 文本先做 Unicode NFC 规范化（组合字符与预组合字符等价）。
2. 词 = 满足 `ch == "_" or ch.isalnum()` 的字符的**最大连续序列**；
   其余字符（空白、标点、连字符、撇号）均为分隔符。
   例：`state-of-the-art` → `state/of/the/art`，`don't` → `don/t`。
3. 分词器保留表面形式；规范身份 = `NFC + casefold`
   （`Straße`/`STRASSE`/`strasse` 聚合为同一词）。

## 核心设计

**压缩前缀树**：边携带字符串标签，单子链在删除后合并，节点数与前缀分叉数同阶。

**带版本的 TopK 上界缓存**：每个节点缓存子树精确 Top-`bound` 列表
（排名键 `(-freq, word)`，即频次降序 + 规范词字典序稳定决胜），
附 `cache_version` 与 `stale` 标记；`cache[0]` 即子树频次上界。

- 词频**上升/插入**：沿根到终端路径就地并入候选（offer），缓存保持精确。
- 词频**下降/删除**：仅把路径标记 stale；查询时按需重建——自底向上合并
  子节点缓存，把此前被截断在 Top-`bound` 之外的候选**补回**（backfill）。
- 查询只刷新 stale 或容量不足的路径，未受影响分支零开销，
  **不会每次扫描整个前缀子树**（`EfficiencyTest` 用重建计数器验证）。

**显示形式与别名聚合**：同一规范词的多个表面形式按观测频次聚合，
显示出现次数最多者；并列取字典序最小，保证确定性。

**排名 + 分页**：`suggest(prefix, k, cursor)` 按 `(频次降序, 规范词升序)` 排名；
游标绑定 `(generation, 规范化前缀, 已消费位次, 末条排名键)`，跨页原样回传，
缓存容量自动扩展到 `已消费 + k`。`list_lex(prefix, limit, start_after)`
提供字典序分页，利用子树共享前缀整体剪枝。空前缀即全局 TopK，
结果条数受 `max_results` 上限约束。

**内存压缩重定位**：`compact()` 把全部词重建进全新节点存储（逻辑重定位，
丢弃旧缓存），`generation` 递增。采用**明确失效**策略：此后使用旧游标
抛出 `StaleCursorError`，调用方应重新发起第一页查询。

**一词是另一词前缀**：终端标记挂在中间节点上，`program` 与 `programming`
均可作为建议返回；删除高频词后下一候选自动补入。

## 运行

```bash
python3 demo.py                              # 运行样例
python3 -m unittest discover -s tests -v     # 测试（含随机增删对照全排序）
```

随机差分测试（`RandomizedDifferentialTest`）：固定种子下 3000 步随机
add/set/remove（小字母表制造大量前缀重叠），周期性插入 `compact()`，
每步随机前缀/随机 k 与"全排序参考模型"逐条比对，并校验分页链拼接一致。
