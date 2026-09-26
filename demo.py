"""qexpand 运行样例：本地合成 UTF-8 文档 + 显式同义组扩展查询。

运行：python3.11 demo.py
"""

from qexpand import PositionalIndex, SynonymGroups, search

# 本地合成 UTF-8 文档（中英文混合）
DOCS = {
    "d1": "New York is a city that never sleeps. I love New York.",
    "d2": "NY subway runs all night in NY.",
    "d3": "I bought a car last week. The car is red.",
    "d4": "My automobile is fast and red.",
    "d5": "Machine learning powers modern search. ML is everywhere.",
    "d6": "深度学习是机器学习的分支，深度学习很有用。",
    "d7": "A bike or a bicycle is fine for short trips.",
    "d8": "York is historic, quite unlike anything new.",
    "d9": "new things and old york tales",
}


def build():
    groups = SynonymGroups()
    groups.add_word_group(["car", "automobile"])      # 单词同义（对称，a<->b）
    groups.add_word_group(["bike", "bicycle"])
    groups.add_phrase_group(["new york", "ny"])       # 短语同义（多词 <-> 单词）
    groups.add_phrase_group(["machine learning", "ml"])
    groups.add_phrase_group(["深度学习", "dl"])
    index = PositionalIndex()
    for doc_id, text in DOCS.items():
        index.add_document(doc_id, text)
    return groups, index


def show(res, k):
    status = "完整" if res.complete else "不完整（路径预算截断）"
    print(f"  查询词元: {res.query_tokens}  路径数: {len(res.paths)}"
          f"（上限 {res.max_paths}，枚举{status}）")
    for i, p in enumerate(res.paths[:8]):
        tag = "原词" if p.is_original else f"扩展{list(p.group_keys)}"
        print(f"    路径{i}: {' '.join(p.terms)!r:40} 权重={p.weight:.2f} "
              f"长度={p.length} 原词区间={p.original_span} [{tag}]")
    if len(res.paths) > 8:
        print(f"    ... 其余 {len(res.paths) - 8} 条路径略")
    if not res.hits:
        print("  无命中")
    for rank, (score, doc) in enumerate(res.hits[:k], 1):
        print(f"  #{rank} {doc}  score={score:.3f}")
        for m in res.evidence[doc]:
            tag = "原词" if m.is_original else f"扩展{list(m.group_keys)}"
            print(f"      [{tag}] 词={m.terms} 位置=[{m.start},{m.end}) "
                  f"权重={m.weight:.2f}")


def main():
    groups, index = build()

    print("=" * 72)
    print("样例 1：短语同义 new york <-> ny（多词不可拆散，d8/d9 不应命中）")
    print("=" * 72)
    show(search(index, "new york", groups, k=5), k=5)

    print()
    print("=" * 72)
    print("样例 2：单词同义 car <-> automobile（原词权重高于扩展，叠分有界）")
    print("=" * 72)
    show(search(index, "car", groups, k=5), k=5)

    print()
    print("=" * 72)
    print("样例 3：中文短语同义 深度学习 <-> dl（UTF-8 按字切分）")
    print("=" * 72)
    show(search(index, "深度学习", groups, k=5), k=5)

    print()
    print("=" * 72)
    print("样例 4：路径预算截断（两组各 5 个同义词，(1+4)^2=25 条路径，预算 4）")
    print("=" * 72)
    g2 = SynonymGroups()
    g2.add_word_group([f"a{i}" for i in range(5)])
    g2.add_word_group([f"b{i}" for i in range(5)])
    idx2 = PositionalIndex()
    idx2.add_document("x1", "a0 b0")
    idx2.add_document("x2", "a3 b2")
    res = search(idx2, "a0 b0", g2, max_paths=4)
    show(res, k=5)
    if res.truncated:
        print("  ⚠ 结果不完整：路径预算已耗尽，未枚举的扩展未参与计分")


if __name__ == "__main__":
    main()
