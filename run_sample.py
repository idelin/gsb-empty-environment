"""运行样例：阈值式多字段 TopK 融合 vs 全量参考融合。

用法:
    python3.11 run_sample.py [query]
默认查询 "检索 融合"，字段权重 title=0.7 body=0.3。
"""

from __future__ import annotations

import sys

from fusion import Index, exhaustive_fuse, fuse_topk, load_corpus

CORPUS = "data/corpus.tsv"


def show(result, title: str) -> None:
    print(f"== {title} ==")
    print(f"mode={result.mode} stop={result.stop_reason} "
          f"degraded={result.degraded} docs_seen={result.docs_seen} "
          f"pages={result.pages_fetched}")
    if result.degrade_reasons:
        for r in result.degrade_reasons:
            print(f"  退化原因: {r}")
    for i, h in enumerate(result.hits, start=1):
        parts = ", ".join(f"{f}={n:.3f}" for f, n in h.field_scores.items())
        miss = ",".join(h.missing_fields) or "-"
        print(f"  {i}. {h.doc_id} score={h.score:.6f} "
              f"exact={h.exact} fields[{parts}] missing[{miss}]")
    print("-- 检索轨迹 --")
    for line in result.trace:
        print("   " + line)
    print()


def main() -> int:
    query = sys.argv[1] if len(sys.argv) > 1 else "检索 融合"
    weights = {"title": 0.7, "body": 0.3}

    docs = load_corpus(CORPUS)
    index = Index(docs)

    threshold = fuse_topk(index, query, weights, k=3, page_size=2)
    reference = exhaustive_fuse(index, query, weights, k=3)
    show(threshold, f"阈值融合 query={query!r} weights={weights}")
    show(reference, "全量参考融合")

    same = [(h.doc_id, round(h.score, 12)) for h in threshold.hits]
    ref = [(h.doc_id, round(h.score, 12)) for h in reference.hits]
    print("结果一致:", same == ref)
    return 0 if same == ref else 1


if __name__ == "__main__":
    raise SystemExit(main())
