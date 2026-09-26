"""限长标签自动补全：前缀匹配 + 最多一次编辑错误的 TopK 检索。

核心语义
--------
- 词典：若干 (词条, 非负权重)，词条长度（Unicode 码点数）不超过 max_len（"限长"）。
- 查询 q 命中词条 w 当且仅当满足以下任一条件：
  1. 精确前缀：w 以 q 为前缀（q 可以是空串，此时命中全部词条）；
  2. 模糊整词：Levenshtein(q, w) <= 1。
  编辑距离按 Unicode 码点计算，允许插入 / 删除 / 替换，不允许交换
  （transposition）。因此查询 "ab" 与词条 "ba" 的整词距离为 2（两次替换），
  不会因交换被算作 1，故 "ba" 不命中；而 "ac" 距离为 1，命中。
- 同一候选被多条路径命中（例如 "abc" 对查询 "ab" 既是精确前缀、整词距离
  也为 1）只返回一次，并取更优的精确前缀解释（距离 0）。
- 结果按 (权重降序, 词典序升序) 排序取 TopK；词典序即 Python 字符串序（按码点）。
- 每个候选附带查询 q -> 匹配前缀 的最短编辑脚本（0 或 1 步操作）：
  - 精确前缀命中：匹配前缀为 q 本身，脚本为空，补全部分为词剩余后缀；
  - 模糊整词命中：匹配前缀为整个词条，脚本为 q -> 词条 的最短脚本。

索引与对照
----------
- TagIndex：共享前缀 Trie，一次构建多次查询。查询时在 Trie 上 DFS，
  每个节点维护一行动态规划（DP）行；不在精确前缀路径上且 DP 行最小值 > 1
  的子树直接剪枝，避免每次扫描全部词条。
  同一条词条在 Trie 中只有一个终止节点，天然去重。
- naive_search：朴素对照实现，逐词条判定（前缀检查 + 整词编辑距离）。
- 两侧均输出访问统计（Trie：访问节点数；朴素：检查词条数 / 距离计算次数）。
"""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass

MAX_EDITS = 1


class _Node:
    __slots__ = ("children", "weight")

    def __init__(self):
        self.children = {}
        self.weight = None  # None 表示非终止节点


@dataclass(frozen=True)
class Candidate:
    word: str             # 命中词条（完整词）
    weight: float         # 词条权重
    distance: int         # 查询 -> 匹配前缀 的编辑距离（0 或 1）
    matched_prefix: str   # 匹配前缀（精确前缀命中为查询本身；模糊命中为整个词条）
    completion: str       # 补全部分：word == matched_prefix + completion
    script: tuple         # 查询 -> 匹配前缀 的最短编辑脚本（dict 元组）

    def to_dict(self):
        return {
            "word": self.word,
            "weight": self.weight,
            "distance": self.distance,
            "matched_prefix": self.matched_prefix,
            "completion": self.completion,
            "script": list(self.script),
        }


@dataclass(frozen=True)
class SearchResult:
    candidates: tuple     # tuple[Candidate, ...]，已按 (权重降序, 词典序) 排序
    nodes_visited: int    # 本次查询访问的 Trie 节点数（含根）


@dataclass(frozen=True)
class NaiveResult:
    candidates: tuple
    words_examined: int   # 扫描的词条数
    distance_checks: int  # 计算整词编辑距离的次数


def _validate_word(word, max_len):
    if not isinstance(word, str):
        raise TypeError(f"词条必须是 str，得到 {type(word).__name__}")
    if len(word) > max_len:
        raise ValueError(f"词条长度 {len(word)} 超过上限 {max_len}: {word!r}")


def _validate_weight(weight):
    if isinstance(weight, bool) or not isinstance(weight, (int, float)):
        raise TypeError(f"权重必须是数字，得到 {type(weight).__name__}")
    if weight != weight or weight == float("inf") or weight < 0:
        raise ValueError(f"权重必须是非负有限数，得到 {weight!r}")


def _next_row(prev_row, ch, query):
    """Levenshtein DP：在 prev_row（当前前缀 vs query 各前缀）基础上追加字符 ch。"""
    row = [prev_row[0] + 1]
    append = row.append
    for j, qc in enumerate(query, 1):
        cost = 0 if ch == qc else 1
        best = prev_row[j] + 1          # 删除 ch
        v = row[j - 1] + 1              # 插入
        if v < best:
            best = v
        v = prev_row[j - 1] + cost      # 匹配 / 替换
        if v < best:
            best = v
        append(best)
    return row


def edit_distance(a, b):
    """标准 Levenshtein 距离（按码点，插入/删除/替换，无交换）。"""
    prev = list(range(len(b) + 1))
    for i, ca in enumerate(a, 1):
        row = [i]
        for j, cb in enumerate(b, 1):
            cost = 0 if ca == cb else 1
            row.append(min(prev[j] + 1, row[j - 1] + 1, prev[j - 1] + cost))
        prev = row
    return prev[len(b)]


def edit_script(source, target):
    """source -> target 的一条最短编辑脚本。

    返回操作 dict 列表，操作按顺序作用于 source 的码点序列：
    - {"op": "insert",  "pos": i, "char": c}      在位置 i 前插入 c
    - {"op": "delete",  "pos": i, "char": c}      删除位置 i 的字符 c
    - {"op": "replace", "pos": i, "from": a, "to": b}  位置 i 的 a 替换为 b
    pos 均为原始 source 中的 0 基码点下标。
    """
    n, m = len(source), len(target)
    dp = [[0] * (m + 1) for _ in range(n + 1)]
    for i in range(1, n + 1):
        dp[i][0] = i
    for j in range(1, m + 1):
        dp[0][j] = j
    for i in range(1, n + 1):
        si = source[i - 1]
        row, prev = dp[i], dp[i - 1]
        for j in range(1, m + 1):
            cost = 0 if si == target[j - 1] else 1
            row[j] = min(prev[j] + 1, row[j - 1] + 1, prev[j - 1] + cost)
    ops = []
    i, j = n, m
    while i > 0 or j > 0:
        if i > 0 and j > 0 and source[i - 1] == target[j - 1] \
                and dp[i][j] == dp[i - 1][j - 1]:
            i -= 1
            j -= 1
        elif i > 0 and j > 0 and dp[i][j] == dp[i - 1][j - 1] + 1:
            ops.append({"op": "replace", "pos": i - 1,
                        "from": source[i - 1], "to": target[j - 1]})
            i -= 1
            j -= 1
        elif i > 0 and dp[i][j] == dp[i - 1][j] + 1:
            ops.append({"op": "delete", "pos": i - 1, "char": source[i - 1]})
            i -= 1
        else:
            ops.append({"op": "insert", "pos": i, "char": target[j - 1]})
            j -= 1
    ops.reverse()
    return ops


def _sort_hits(hits):
    # (权重降序, 词典序升序)
    hits.sort(key=lambda h: (-h[1], h[0]))
    return hits


class TagIndex:
    """共享前缀 Trie 索引：一次构建，多次查询，避免每次扫描全部词条。"""

    def __init__(self, max_len=64):
        if not isinstance(max_len, int) or max_len < 0:
            raise ValueError(f"max_len 必须是非负整数，得到 {max_len!r}")
        self.max_len = max_len
        self._root = _Node()
        self._size = 0
        self._node_count = 1

    @property
    def size(self):
        """去重后的词条数。"""
        return self._size

    @property
    def node_count(self):
        """Trie 节点总数（含根）。"""
        return self._node_count

    @classmethod
    def from_entries(cls, entries, max_len=64):
        index = cls(max_len=max_len)
        for word, weight in entries:
            index.add(word, weight)
        return index

    def add(self, word, weight):
        _validate_word(word, self.max_len)
        _validate_weight(weight)
        node = self._root
        for ch in word:
            child = node.children.get(ch)
            if child is None:
                child = _Node()
                node.children[ch] = child
                self._node_count += 1
            node = child
        if node.weight is None:
            self._size += 1
            node.weight = weight
        elif weight > node.weight:
            # 重复词条：保留较大权重，保证候选唯一
            node.weight = weight

    def entries(self):
        """产出全部 (词条, 权重)，每条一次。"""
        stack = [(self._root, "")]
        while stack:
            node, prefix = stack.pop()
            if node.weight is not None:
                yield prefix, node.weight
            for ch, child in node.children.items():
                stack.append((child, prefix + ch))

    def search(self, query, k=10):
        """返回 SearchResult：TopK 候选 + 访问节点数。"""
        if not isinstance(query, str):
            raise TypeError("查询必须是 str")
        if k < 0:
            raise ValueError("k 必须非负")
        n = len(query)
        if n > self.max_len + MAX_EDITS:
            # 词条长度 <= max_len：精确前缀要求 len(q) <= len(w)，
            # 模糊命中要求 |len(q) - len(w)| <= 1，均不可能
            return SearchResult((), 0)
        initial_row = list(range(n + 1))
        hits = []  # (word, weight, distance, matched_prefix)
        visited = 0
        # 栈元素：(节点, 节点对应前缀串, DP 行)
        stack = [(self._root, "", initial_row)]
        while stack:
            node, prefix, row = stack.pop()
            visited += 1
            exact = prefix.startswith(query)  # 精确前缀子树（含 query 节点本身）
            if node.weight is not None:
                if exact:
                    hits.append((prefix, node.weight, 0, query))
                elif row[n] <= MAX_EDITS:
                    hits.append((prefix, node.weight, row[n], prefix))
            # 精确前缀路径上的节点（query 的前缀或以其为前缀）必须继续下行；
            # 其余节点若 DP 行最小值 > 1，则子树内整词距离均 > 1，剪枝。
            on_exact_path = exact or query.startswith(prefix)
            if not on_exact_path and min(row) > MAX_EDITS:
                continue
            for ch, child in node.children.items():
                stack.append((child, prefix + ch, _next_row(row, ch, query)))
        cands = []
        for word, weight, dist, mp in _sort_hits(hits)[:k]:
            script = tuple(edit_script(query, mp))
            cands.append(Candidate(word, weight, dist, mp, word[len(mp):], script))
        return SearchResult(tuple(cands), visited)


def naive_search(entries, query, k=10):
    """朴素对照：逐词条判定（前缀检查 + 整词编辑距离）。"""
    words_examined = 0
    distance_checks = 0
    hits = []
    for word, weight in entries:
        words_examined += 1
        if word.startswith(query):
            hits.append((word, weight, 0, query))
        else:
            distance_checks += 1
            d = edit_distance(query, word)
            if d <= MAX_EDITS:
                hits.append((word, weight, d, word))
    cands = tuple(
        Candidate(word, weight, dist, mp, word[len(mp):],
                  tuple(edit_script(query, mp)))
        for word, weight, dist, mp in _sort_hits(hits)[:k]
    )
    return NaiveResult(cands, words_examined, distance_checks)


def _load_dict(path):
    """读取词典文件：每行 `词条<TAB>非负权重`，空行与 # 开头的行忽略。"""
    entries = []
    with open(path, encoding="utf-8") as fh:
        for lineno, raw in enumerate(fh, 1):
            line = raw.rstrip("\n")
            if not line or line.startswith("#"):
                continue
            try:
                word, wtext = line.rsplit("\t", 1)
            except ValueError:
                raise SystemExit(
                    f"词典第 {lineno} 行格式错误（应为 词条<TAB>权重）: {line!r}")
            try:
                weight = int(wtext)
            except ValueError:
                try:
                    weight = float(wtext)
                except ValueError:
                    raise SystemExit(
                        f"词典第 {lineno} 行权重不是数字: {wtext!r}")
            entries.append((word, weight))
    return entries


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="tagcomplete",
        description="限长标签自动补全：前缀 + 最多一次编辑错误的 TopK 检索。"
                    "从标准输入逐行读取查询，逐行输出 JSON 结果。")
    parser.add_argument("dict", help="词典文件：每行 `词条<TAB>非负权重`，# 开头为注释")
    parser.add_argument("-k", "--topk", type=int, default=10, help="返回候选数（默认 10）")
    parser.add_argument("--max-len", type=int, default=64, help="词条最大码点长度（默认 64）")
    parser.add_argument("--check", action="store_true",
                        help="同时运行朴素对照实现，校验结果一致并输出其统计")
    args = parser.parse_args(argv)

    entries = _load_dict(args.dict)
    index = TagIndex.from_entries(entries, max_len=args.max_len)
    out = sys.stdout
    for raw in sys.stdin:
        query = raw.rstrip("\n")
        result = index.search(query, k=args.topk)
        record = {
            "query": query,
            "k": args.topk,
            "results": [c.to_dict() for c in result.candidates],
            "stats": {
                "nodes_visited": result.nodes_visited,
                "index_nodes": index.node_count,
                "index_words": index.size,
            },
        }
        if args.check:
            naive = naive_search(list(index.entries()), query, k=args.topk)
            record["stats"]["naive_words_examined"] = naive.words_examined
            record["stats"]["naive_distance_checks"] = naive.distance_checks
            record["naive_match"] = (
                [c.to_dict() for c in naive.candidates] == record["results"])
        out.write(json.dumps(record, ensure_ascii=False) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
