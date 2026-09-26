"""运行样例：python3.11 -m editindex.demo

在本地合成的 UTF-8 文本上建索引，演示阈值查询、TopK 查询、
破坏三角不等式的加权成本，并与暴力全扫描参考对照。
"""

from .distance import Weights, weighted_distance
from .index import BKTreeIndex
from .reference import full_scan_search, full_scan_topk
from .tokenizer import tokenize, vocabulary

# 本地合成的 UTF-8 样本文本（含重复词、大小写/重音变体、CJK、emoji）
SAMPLE_TEXT = (
    "Café café cafe CAFÉ café naïve naive "
    "cat bat rat cats car cart dog "
    "东京 南京 北京 西京 东京 东京 猫咪 猫 🐱 🐱🐶 🐶🐱 "
    "straße STRASSE Strasse 版本2 版本3"
)


def show(title, matches):
    print(f"\n== {title} ==")
    for m in matches:
        print(f"  {m.word!r:14} 距离={m.distance:g}")
    if not matches:
        print("  （无结果）")


def main():
    vocab = vocabulary(tokenize(SAMPLE_TEXT))
    print(f"词表（去重后 {len(vocab)} 个规范词）：")
    print("  " + " ".join(vocab))

    # 1) 单位权重阈值查询（阈值含等号）
    index = BKTreeIndex(Weights())
    index.build(vocab)
    show("阈值查询 search('café', 1)，单位权重", index.search("café", 1))
    print(f"  剪枝统计：访问节点 {index.last_visited}/{len(vocab)}，"
          f"精确距离计算 {index.last_exact_computed} 次")

    # 2) TopK，等距候选完整且稳定
    show("TopK 查询 topk('cat', 3)（第 3 名等距者一并返回）",
         index.topk("cat", 3))
    show("TopK 查询 topk('东京', 2)", index.topk("东京", 2))

    # 3) 破坏三角不等式的成本：非对称插删 + 廉价换位
    w = Weights(insert=1, delete=5, substitute=3, transpose=1)
    print(f"\n== 加权成本 {w} ==")
    print("  对称性被破坏：d('','ab') =",
          weighted_distance("", "ab", w),
          "而 d('ab','') =", weighted_distance("ab", "", w))
    w_unit = Weights()
    print("  三角不等式被破坏（OSA）：d('ca','abc') =",
          weighted_distance("ca", "abc", w_unit),
          "> d('ca','ac') + d('ac','abc') =",
          weighted_distance("ca", "ac", w_unit)
          + weighted_distance("ac", "abc", w_unit))

    # 4) 在该成本下建索引并与暴力全扫描对照
    wv = vocabulary(tokenize(SAMPLE_TEXT + " ca ac abc bac cab"))
    index2 = BKTreeIndex(w)
    index2.build(wv)
    ok = True
    for query in ["café", "ca", "abc", "东京", "straße", "🐱🐶"]:
        for radius in (0, 1, 2, 4, 8):
            got = index2.search(query, radius)
            ref = full_scan_search(wv, query, w, radius)
            if got != ref:
                ok = False
                print(f"  不一致！query={query!r} radius={radius}")
        for k in (1, 3, 6):
            if index2.topk(query, k) != full_scan_topk(wv, query, w, k):
                ok = False
                print(f"  不一致！query={query!r} k={k}")
    print(f"  索引 vs 暴力全扫描（{len(wv)} 词 × 6 查询 × 多阈值/TopK）："
          + ("完全一致，无漏词 ✔" if ok else "存在差异 ✘"))

    show(f"加权阈值查询 search('café', 4)，{w}", index2.search("café", 4))
    print(f"  剪枝统计：访问节点 {index2.last_visited}/{len(wv)}，"
          f"精确距离计算 {index2.last_exact_computed} 次")


if __name__ == "__main__":
    main()
