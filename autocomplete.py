#!/usr/bin/env python3
"""限长标签自动补全。

匹配语义：词典词 w 命中查询 q 当且仅当
  - q 是 w 的前缀（按 Unicode 码点），或
  - edit_distance(q, w) <= 1（允许插入/删除/替换，不允许交换）。

结果按权重降序、再按词典序（码点序）升序取 TopK，并给出 q -> w 的最短编辑脚本。
共享索引为按码点构建的 Trie，查询时只做受限遍历，不扫描全部词；
同时提供朴素对照（全表扫描 + 完整 DP）与访问节点统计。
"""
from __future__ import annotations

import argparse
import json
import sys

DEFAULT_MAX_LEN = 32
MAX_EDITS = 1


# ---------------------------------------------------------------- 编辑距离 / 脚本

def edit_script(a: str, b: str):
    """返回 (距离, 脚本)。按 Unicode 码点计算，仅插入/删除/替换。

    脚本为操作列表，需按列表顺序施加到 a 上即得 b：
      ("insert", pos, ch)  在当前串的 pos 处插入 ch
      ("delete", pos)      删除当前串 pos 处的字符
      ("replace", pos, ch) 将当前串 pos 处的字符替换为 ch
    pos 以原始 a 的坐标给出；脚本按位置从右到左排列，因此顺序施加时坐标始终有效。
    """
    la, lb = len(a), len(b)
    dp = [[0] * (lb + 1) for _ in range(la + 1)]
    for i in range(la + 1):
        dp[i][0] = i
    for j in range(lb + 1):
        dp[0][j] = j
    for i in range(1, la + 1):
        ai = a[i - 1]
        row, prev = dp[i], dp[i - 1]
        for j in range(1, lb + 1):
            cost = 0 if ai == b[j - 1] else 1
            row[j] = min(prev[j] + 1, row[j - 1] + 1, prev[j - 1] + cost)
    ops = []
    i, j = la, lb
    while i > 0 or j > 0:
        if i > 0 and j > 0 and a[i - 1] == b[j - 1] and dp[i][j] == dp[i - 1][j - 1]:
            i, j = i - 1, j - 1
        elif j > 0 and dp[i][j] == dp[i][j - 1] + 1:
            ops.append(("insert", i, b[j - 1]))
            j -= 1
        elif i > 0 and dp[i][j] == dp[i - 1][j] + 1:
            ops.append(("delete", i - 1))
            i -= 1
        else:
            ops.append(("replace", i - 1, b[j - 1]))
            i -= 1
            j -= 1
    return dp[la][lb], ops


def edit_distance(a: str, b: str) -> int:
    return edit_script(a, b)[0]


def apply_script(a: str, ops) -> str:
    chars = list(a)
    for op in ops:
        if op[0] == "insert":
            chars.insert(op[1], op[2])
        elif op[0] == "delete":
            del chars[op[1]]
        else:  # replace
            chars[op[1]] = op[2]
    return "".join(chars)


# ---------------------------------------------------------------- Trie 索引

class TrieNode:
    __slots__ = ("children", "weight")

    def __init__(self):
        self.children = {}
        self.weight = None  # 非 None 表示某词在此终止


class TagIndex:
    """共享索引：构建一次，多次查询。"""

    def __init__(self, max_len: int = DEFAULT_MAX_LEN):
        self.root = TrieNode()
        self.max_len = max_len
        self.words = {}  # word -> weight（供朴素对照使用）
        self.rejected = 0

    def add(self, word: str, weight: float) -> bool:
        if weight < 0:
            raise ValueError(f"权重必须非负: {word!r} -> {weight}")
        if not word or len(word) > self.max_len:
            self.rejected += 1
            return False
        if word in self.words:
            self.words[word] = max(self.words[word], weight)
        else:
            self.words[word] = weight
        node = self.root
        for ch in word:
            node = node.children.setdefault(ch, TrieNode())
        node.weight = self.words[word]
        return True

    # -- 查询：前缀 + 最多一次编辑的受限 Trie 遍历 --
    def search(self, query: str, k: int):
        """返回 (candidates, nodes_visited)。

        candidates: [(word, weight, distance, script), ...] 已按 (-weight, word) 排序取 TopK。
        """
        visited = 0
        found = set()  # 去重：同一词可能被多条编辑路径命中
        n = len(query)
        seen_states = set()
        stack = [(self.root, 0, 0, [])]  # (节点, 已匹配查询长度, 已用编辑数, 路径码点)
        while stack:
            node, i, edits, path = stack.pop()
            state = (id(node), i, edits)
            if state in seen_states:
                continue
            seen_states.add(state)
            visited += 1
            if node.weight is not None and edits + (n - i) <= MAX_EDITS:
                # 词是查询的前缀（剩余查询字符靠删除消掉），或编辑后恰好相等
                found.add("".join(path))
            if i == n:
                if edits == 0:
                    # 查询是当前路径的前缀：子树内所有词均为前缀命中
                    visited += self._collect(node, path, found)
                # edits == 1 时继续延伸将超出一次编辑，且不再是前缀命中，停止
                continue
            ch = query[i]
            child = node.children.get(ch)
            if child is not None:
                stack.append((child, i + 1, edits, path + [ch]))
            if edits < MAX_EDITS:
                stack.append((node, i + 1, edits + 1, path))  # 删除 query[i]
                for c2, child2 in node.children.items():
                    stack.append((child2, i, edits + 1, path + [c2]))  # 插入 c2
                    if c2 != ch:
                        stack.append((child2, i + 1, edits + 1, path + [c2]))  # 替换
        results = []
        for w in found:
            dist, script = edit_script(query, w)
            results.append((w, self.words[w], dist, script))
        results.sort(key=lambda r: (-r[1], r[0]))
        return results[:k], visited

    def _collect(self, node, path, out):
        """收集子树内全部终止节点（前缀命中），返回访问的节点数。"""
        visited = 0
        stack = [(node, path)]
        while stack:
            nd, p = stack.pop()
            visited += 1
            if nd.weight is not None:
                out.add("".join(p))
            for c2, child2 in nd.children.items():
                stack.append((child2, p + [c2]))
        return visited


# ---------------------------------------------------------------- 朴素对照

def naive_search(words: dict, query: str, k: int):
    """全表扫描 + 完整 DP。返回 (candidates, words_scanned, dp_cells)。"""
    cells = 0
    results = []
    for w, wt in words.items():
        dist, script = edit_script(query, w)
        cells += (len(query) + 1) * (len(w) + 1)
        if dist <= MAX_EDITS or w.startswith(query):
            results.append((w, wt, dist, script))
    results.sort(key=lambda r: (-r[1], r[0]))
    return results[:k], len(words), cells


# ---------------------------------------------------------------- 词典加载 / CLI

def load_dict(path: str, index: TagIndex) -> None:
    """词典格式：每行 `词<TAB>权重`，# 开头为注释，空行忽略。"""
    with open(path, encoding="utf-8") as fh:
        for lineno, line in enumerate(fh, 1):
            line = line.rstrip("\n")
            if not line or line.startswith("#"):
                continue
            try:
                word, w = line.split("\t")
                index.add(word, float(w))
            except ValueError as exc:
                raise SystemExit(f"词典第 {lineno} 行格式错误: {line!r} ({exc})")


DEMO_WORDS = [  # 合成示例数据
    ("ab", 5), ("abc", 9), ("abd", 3), ("ac", 7), ("ba", 6), ("aab", 4),
    ("apple", 10), ("app", 8), ("application", 2), ("banana", 6),
    ("你好", 9), ("你号", 1), ("世界", 5), ("🙂🙃", 4), ("🙂", 6),
]


def result_to_json(query, k, cands, stats):
    return {
        "query": query,
        "k": k,
        "candidates": [
            {"word": w, "weight": wt, "distance": d, "script": [list(op) for op in s]}
            for w, wt, d, s in cands
        ],
        "stats": stats,
    }


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description="限长标签自动补全（前缀 + 最多一次编辑）")
    ap.add_argument("--dict", help="词典文件：每行 `词<TAB>非负权重`")
    ap.add_argument("--demo", action="store_true", help="使用内置合成示例词典")
    ap.add_argument("--k", type=int, default=10, help="TopK（默认 10）")
    ap.add_argument("--max-len", type=int, default=DEFAULT_MAX_LEN,
                    help=f"标签最大码点长度（默认 {DEFAULT_MAX_LEN}，超长词被拒绝）")
    ap.add_argument("--check", action="store_true", help="与朴素对照结果比对一致性")
    args = ap.parse_args(argv)

    index = TagIndex(max_len=args.max_len)
    if args.demo:
        for w, wt in DEMO_WORDS:
            index.add(w, wt)
    elif args.dict:
        load_dict(args.dict, index)
    else:
        ap.error("需要 --dict 或 --demo")

    for line in sys.stdin:
        query = line.rstrip("\n")
        cands, visited = index.search(query, args.k)
        naive_cands, scanned, cells = naive_search(index.words, query, args.k)
        stats = {
            "index_nodes_visited": visited,
            "naive_words_scanned": scanned,
            "naive_dp_cells": cells,
        }
        if args.check:
            stats["consistent"] = [c[0] for c in cands] == [c[0] for c in naive_cands]
        print(json.dumps(result_to_json(query, args.k, cands, stats),
                         ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
