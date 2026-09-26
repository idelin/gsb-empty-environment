"""运行样例：合成本地 UTF-8 文档，建索引，执行三类查询并与全扫描核对。"""

import random

from interval_index import Interval, IntervalIndex

WORDS = ["苹果", "香蕉", "轨道", "卫星", "apple", "orbit", "文档", "索引", "测试", "数据"]


def make_doc(rng, doc_id):
    words = " ".join(rng.choice(WORDS) for _ in range(rng.randint(3, 6)))
    if rng.random() < 0.2:
        return f"doc-{doc_id} {words}"  # 约 20% 缺失区间属性
    lo = rng.choice([None, rng.randint(0, 10)])
    hi = rng.choice([None, rng.randint(0, 15)])
    lb, rb = rng.choice("[("), rng.choice("])")
    return f"doc-{doc_id} {words} range={lb}{'' if lo is None else lo},{'' if hi is None else hi}{rb}"


def show(name, got, want):
    ok = "一致" if got == want and len(got) == len(set(got)) else "不一致!"
    print(f"  {name:<14} 索引结果({len(got)}条): {got}")
    print(f"  {'':<14} 全扫描({len(want)}条): {want}  => {ok}")


def main():
    rng = random.Random(42)
    idx = IntervalIndex()
    docs = {f"d{i}": make_doc(rng, i) for i in range(20)}

    print("== 合成文档样例（前 6 条，UTF-8）==")
    for doc_id in list(docs)[:6]:
        print(f"  {doc_id}: {docs[doc_id]}")

    for doc_id, text in docs.items():
        idx.upsert(doc_id, text)

    # 演示更新：d0 改为新区间，d1 改为缺失属性
    idx.upsert("d0", "doc-0 苹果 更新后 range=[100,200]")
    idx.upsert("d1", "doc-1 香蕉 更新后没有区间属性")
    idx.check_invariants()

    queries = [
        ("相交 [0,5)", Interval(0, 4), idx.intersect, idx.full_scan_intersect),
        ("相交 [5,8)", Interval(5, 7), idx.intersect, idx.full_scan_intersect),
        ("包含 [2,6]", Interval(2, 6), idx.contains, idx.full_scan_contains),
        ("被包含于 [0,10)", Interval(0, 9), idx.contained_by, idx.full_scan_contained_by),
        ("相交 (-inf,+inf)", Interval(None, None), idx.intersect, idx.full_scan_intersect),
    ]
    print("\n== 查询（索引 vs 全扫描）==")
    for name, q, query_fn, scan_fn in queries:
        print(f"查询: {name}")
        show(name, query_fn(q), scan_fn(q))

    print("\n== 约定验证 ==")
    a, b = Interval(0, 4), Interval(5, 7)  # [0,5) 与 [5,8)
    print(f"  [0,5) 与 [5,8) 相交? {a.intersects(b)}（应为 False）")
    print(f"  点区间 [5,5] 与 [5,8) 相交? {Interval(5, 5).intersects(b)}（应为 True）")
    print(f"  词元倒排 docs_with_token('苹果'): {idx.docs_with_token('苹果')}")


if __name__ == "__main__":
    main()
