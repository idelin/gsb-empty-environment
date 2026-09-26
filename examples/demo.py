"""运行样例：本地合成 UTF-8 文档上的倒排表与短语查询演示。
用法：python3 examples/demo.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from invidx import Index, PhraseQuery


def show(title, result):
    print(f"\n== {title} ==")
    for m in result:
        evidence = ", ".join(
            f"slot{h.slot_index}={'/'.join(h.terms)}@{h.positions}" for h in m.evidence
        )
        print(
            f"  doc={m.docid} field={m.field} "
            f"pos[{m.pos_start},{m.pos_end}] char[{m.start_char},{m.end_char}) "
            f"text={m.text!r} | {evidence}"
        )
    if not result.matches:
        print("  (no hits)")


def main():
    idx = Index()
    idx.add_document("news-1", {
        "title": "Quick Fox 快报",
        "body": "The quick brown fox jumps over the lazy dog. 快狐快狐!",
    })
    idx.add_document("news-2", {
        "title": "地铁观察",
        "body": "the new york subway is busy, and the nyc subway is old",
    })
    idx.commit()
    idx.add_document("blog-1", {
        "body": "a quick red fox and a quick blue fox and a a a",
    })
    idx.commit()
    print(f"segments: {idx.segment_count()}")

    show("exact phrase: quick brown fox", idx.search(PhraseQuery.of("quick brown fox")))

    show(
        "synonym graph: (new york | nyc) subway",
        idx.search(PhraseQuery([[("new", "york"), ("nyc",)], ["subway"]])),
    )

    show(
        "synonyms + slop: (quick|fast) fox, slop=2",
        idx.search(PhraseQuery([["quick", "fast"], ["fox"]], slop=2)),
    )

    show("repeated term: a a (needs two positions)", idx.search(PhraseQuery.of("a a")))

    print(f"\nbefore delete: df(quick)={idx.doc_freq('quick')} df(a)={idx.doc_freq('a')}")
    idx.delete("blog-1")
    print(f"after delete blog-1: df(quick)={idx.doc_freq('quick')} df(a)={idx.doc_freq('a')}")
    show("a a after delete", idx.search(PhraseQuery.of("a a")))

    idx2 = Index()
    idx2.add_document("d1", {"body": "hello world"})
    idx2.add_document("d2", {"body": "hello there"})
    idx2.commit()
    snap = idx2.snapshot()
    idx2.add_document("d3", {"body": "hello snapshot"})
    idx2.commit()
    idx2.delete("d1")
    print("\n== snapshot cursor ==")
    print(f"  snapshot docs: {snap.live_docids()}  current docs: {idx2.snapshot().live_docids()}")
    cur = snap.cursor()
    while not cur.exhausted():
        for docid, _ in cur.next_batch(1):
            print(f"  cursor yields: {docid} (position={cur.position})")

    print(f"\nsegments before merge: {idx.segment_count()}")
    idx.merge()
    print(f"segments after merge: {idx.segment_count()}")
    show("after merge: (quick|fast) fox, slop=2",
         idx.search(PhraseQuery([["quick", "fast"], ["fox"]], slop=2)))

    hot = Index()
    hot.add_document("hot", {"f": "x " * 20000 + "y " + "x " * 20000})
    hot.commit()
    result = hot.search(PhraseQuery.of("x y x"))
    stats = result.stats
    total = hot._segments[0].terms["x"]["hot"]["f"].total_blocks()
    print(f"\n== high-frequency term x (40000 occurrences, {total} blocks) ==")
    for m in result:
        print(f"  hit pos[{m.pos_start},{m.pos_end}]")
    print(
        f"  blocks_decoded={stats.blocks_decoded} (y:{stats.per_term_blocks.get('y', 0)}, "
        f"x:{stats.per_term_blocks.get('x', 0)}), positions_scanned={stats.positions_scanned}"
    )


if __name__ == "__main__":
    main()
