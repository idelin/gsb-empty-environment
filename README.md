# 限长标签自动补全（tagcomplete）

Python 3.11 标准库实现，无第三方依赖。词典词条带非负权重，查询支持
**精确前缀** 与 **最多一次编辑错误**，按 (权重降序, 词典序升序) 返回 TopK
及最短编辑脚本。所有示例数据均为合成数据。

## 匹配语义

查询 `q` 命中词条 `w` 当且仅当满足任一条件：

1. **精确前缀**：`w` 以 `q` 为前缀（`q` 为空串时命中全部词条）；
2. **模糊整词**：`Levenshtein(q, w) <= 1`。

- 编辑按 **Unicode 码点** 计算，允许插入 / 删除 / 替换，**不允许交换**。
  因此查询 `ab` 与词条 `ba` 的整词距离为 2（两次替换），不会命中；
  词条 `ac` 距离为 1，命中。
- 同一候选被多条路径命中（如 `abc` 对查询 `ab` 既是精确前缀、整词距离
  也为 1）**只返回一次**，并取距离更小的精确前缀解释。
- 词典中重复词条只保留一条（权重取较大者）。
- 词条长度（码点数）超过 `max_len` 会被拒绝（"限长"）；负权重、NaN、
  无穷权重同样被拒绝。

## 索引与对照

- `TagIndex`：共享前缀 Trie，一次构建多次查询。查询时在 Trie 上 DFS，
  每个节点维护一行 Levenshtein 动态规划行；不在精确前缀路径上且 DP 行
  最小值 > 1 的子树直接剪枝，避免每次扫描全部词条。
- `naive_search`：朴素对照实现，逐词条判定（前缀检查 + 整词编辑距离）。
- 访问统计：Trie 侧报告 `nodes_visited`（访问节点数，含根）；朴素侧报告
  `words_examined`（扫描词条数）与 `distance_checks`（距离计算次数）。

## 命令行用法

```sh
python3 tagcomplete.py 词典文件 [-k TOPK] [--max-len N] [--check] < 查询文件
```

- 从**标准输入**逐行读取查询（空行表示空查询，命中全部词条）；
- 向**标准输出**逐行输出 JSON（UTF-8，`ensure_ascii=False`）；
- `--check`：同时运行朴素对照，输出其统计并给出 `naive_match` 一致性标志。

### 词典文件格式

每行一条：`词条<TAB>非负权重`；空行与 `#` 开头的行忽略。词条可含任意
Unicode 字符（制表符与换行除外）。示例见 `sample_dict.txt`（合成数据）。

### 输出格式（每行一个 JSON 对象）

```json
{
  "query": "ab",
  "k": 5,
  "results": [
    {
      "word": "ac",            // 命中词条（完整词）
      "weight": 5,             // 权重
      "distance": 1,           // 查询 -> 匹配前缀 的编辑距离（0 或 1）
      "matched_prefix": "ac",  // 匹配前缀（精确前缀命中为查询本身；模糊命中为整个词条）
      "completion": "",        // 补全部分：word == matched_prefix + completion
      "script": [              // 查询 -> 匹配前缀 的最短编辑脚本
        {"op": "replace", "pos": 1, "from": "b", "to": "c"}
      ]
    }
  ],
  "stats": {
    "nodes_visited": 19,       // 本次查询访问的 Trie 节点数
    "index_nodes": 38,         // 索引节点总数
    "index_words": 15,         // 索引词条数（去重后）
    "naive_words_examined": 15,   // --check 时才有
    "naive_distance_checks": 13   // --check 时才有
  },
  "naive_match": true          // --check 时才有：与朴素结果是否一致
}
```

编辑脚本操作（`pos` 为原始查询中的 0 基码点下标）：

- `{"op": "insert", "pos": i, "char": c}`：在位置 `i` 前插入 `c`
- `{"op": "delete", "pos": i, "char": c}`：删除位置 `i` 的字符 `c`
- `{"op": "replace", "pos": i, "from": a, "to": b}`：位置 `i` 的 `a` 替换为 `b`

### 示例

```sh
python3 tagcomplete.py sample_dict.txt -k 5 --check < sample_queries.txt
```

## 库接口

```python
from tagcomplete import TagIndex, naive_search, edit_distance, edit_script

index = TagIndex.from_entries([("apple", 10), ("app", 8)], max_len=64)
result = index.search("app", k=10)   # SearchResult(candidates, nodes_visited)
for c in result.candidates:          # Candidate(word, weight, distance,
    print(c.word, c.script)          #   matched_prefix, completion, script)
```

## 测试

```sh
python3 -m unittest test_tagcomplete -v
```

覆盖：编辑距离与脚本（含码点级 Unicode、不允许交换）、精确前缀 / 模糊整词
命中、TopK 排序（权重降序 + 词典序）、多路径去重、重复词条去重、空查询、
限长与权重校验、访问统计，以及随机合成数据上 Trie 与朴素实现的等价性
（含脚本可复现匹配前缀的性质验证）。

实测（2 万合成词条、56 个随机查询）：索引查询总耗时约为朴素扫描的 1/19，
单次查询仅访问约 3.5% 的 Trie 节点，结果与朴素实现完全一致。
