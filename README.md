# 限长标签自动补全

Python 3.11 标准库实现，无第三方依赖。所有示例数据均为合成数据。

## 匹配语义

词典词 `w` 命中查询 `q` 当且仅当（按 Unicode 码点）：

- `q` 是 `w` 的前缀；或
- `edit_distance(q, w) <= 1`，编辑操作仅允许插入 / 删除 / 替换，**不允许交换**
  （因此 `ab` 与 `ba` 距离为 2，不会命中）。

结果按 **权重降序、再按码点词典序升序** 取 TopK；同一词被多条编辑路径命中时只返回一次。
每个候选附带 `q -> w` 的最短编辑脚本（操作数等于距离）。

## 文件

- `autocomplete.py` — 库 + 命令行（Trie 共享索引、朴素对照、访问统计）
- `test_autocomplete.py` — unittest 测试
- `sample_dict.tsv` — 合成示例词典

## 词典格式（输入）

每行一条：`词<TAB>非负权重`；`#` 开头为注释，空行忽略。
词长超过 `--max-len`（默认 32 个码点）或空词被拒绝并计数；重复词保留较大权重。

## 命令行用法

```sh
python3 autocomplete.py --dict sample_dict.tsv --k 5 --check < queries.txt
python3 autocomplete.py --demo          # 使用内置合成词典
```

从标准输入读取查询，每行一个；每个查询输出一行 JSON：

```json
{"query": "ab", "k": 5,
 "candidates": [{"word": "abc", "weight": 9.0, "distance": 1,
                 "script": [["insert", 2, "c"]]}],
 "stats": {"index_nodes_visited": 27, "naive_words_scanned": 15,
           "naive_dp_cells": 192, "consistent": true}}
```

- `distance` / `script`：查询到候选的最短编辑距离与脚本。脚本操作
  `["insert", pos, ch]` / `["delete", pos]` / `["replace", pos, ch]`
  按列表顺序施加到查询上即得候选词（`pos` 以原始查询坐标给出，脚本按位置从右到左排列）。
- `stats.index_nodes_visited`：本次查询在共享 Trie 上访问的节点数。
- `stats.naive_words_scanned` / `naive_dp_cells`：朴素对照（全表扫描 + 完整 DP）的工作量。
- `--check` 时输出 `consistent`：索引结果与朴素对照的候选序列是否一致。

## 设计要点

- **共享索引**：按码点构建的 Trie 只构建一次；查询时做「已匹配长度 × 已用编辑数(0/1)」
  的受限遍历（匹配 / 替换 / 插入 / 删除四种转移），并对 `(节点, i, edits)` 状态去重，
  避免每次扫描全部词。查询耗尽且未用编辑时，子树内所有词均为前缀命中，直接收集。
- **候选去重**：命中集合按词去重，多路径命中只保留一次。
- **编辑脚本**：对少量候选用标准 DP 回溯生成，保证最短且可重放（`apply_script` 验证）。

## 测试

```sh
python3 -m unittest test_autocomplete -v
```

覆盖：距离与交换边界（`ab`/`ac`=1、`ab`/`ba`=2）、Unicode 码点（CJK、emoji）、
多路径去重、权重/词典序排序、TopK、限长拒绝、负权重拒绝、脚本可重放、
随机合成数据上索引与朴素对照等价、索引访问量小于朴素扫描量。
