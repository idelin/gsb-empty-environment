"""Runnable sample: synthetic UTF-8 docs, phrase queries, lifecycle ops.

Run:  python3.11 demo.py
"""
from phraseindex import DecodeCounter, Index, PhraseQuery, verify_snapshot


def show(title, matches):
    print(f"\n== {title} ==")
    if not matches:
        print("  (no match)")
    for m in matches:
        ev = ", ".join(
            f"q{e.element}->{e.term}@pos{e.position}[{e.start}:{e.end}]"
            for e in m.evidence
        )
        print(
            f"  {m.doc_id}.{m.field} range=[{m.start}:{m.end}] "
            f"text={m.text!r} positions={m.positions}\n    evidence: {ev}"
        )


def main():
    idx = Index()
    idx.add_documents(
        [
            (
                "d1",
                {
                    "title": "快速棕色狐狸",
                    "body": "The quick brown fox jumps over the lazy dog.",
                },
            ),
            ("d2", {"body": "a a a a a"}),
            ("d3", {"body": "quick red fox, quick blue fox"}),
            (
                "d4",
                {
                    "title": "hello 世界",
                    "body": "Café naïve — 中文分词测试, quick brown",
                },
            ),
        ]
    )
    idx.add_document("d5", {"body": "fast brown fox, quick brown bear"})

    show("exact phrase [quick brown fox]", idx.search(PhraseQuery(["quick", "brown", "fox"])))
    show("gapped [quick fox] gap<=1", idx.search(PhraseQuery(["quick", "fox"], gaps=1)))
    show(
        "synonym graph [{quick,fast} {brown,fox}] gap<=1",
        idx.search(PhraseQuery([{"quick", "fast"}, {"brown", "fox"}], gaps=1)),
    )
    show("repeated term [a a] (needs 2 distinct positions)", idx.search(PhraseQuery(["a", "a"])))
    show("UTF-8 phrase [中文分词测试 quick]", idx.search(PhraseQuery(["中文分词测试", "quick"])))

    print("\n== document frequency before/after retraction ==")
    print("  df(quick) =", idx.df("quick"))
    idx.delete_document("d3")
    print("  delete d3 -> df(quick) =", idx.df("quick"))
    show("after retracting d3: [quick fox] gap<=1", idx.search(PhraseQuery(["quick", "fox"], gaps=1)))

    print("\n== snapshot cursor ==")
    snap = idx.snapshot()
    idx.add_document("d6", {"body": "quick golden fox"})
    print("  snapshot sees:", sorted({m.doc_id for m in snap.search(PhraseQuery(["quick", "fox"], gaps=1))}))
    print("  live index sees:", sorted({m.doc_id for m in idx.search(PhraseQuery(["quick", "fox"], gaps=1))}))
    page = snap.search(PhraseQuery(["quick"]), after_doc="d1")
    print("  cursor page after doc 'd1':", [(m.doc_id, m.field) for m in page])

    print("\n== segment merge ==")
    print("  segments before merge:", idx.num_segments)
    idx.merge()
    print("  segments after merge:", idx.num_segments, "| df(quick) =", idx.df("quick"))

    print("\n== independent per-document verification ==")
    for q in (
        PhraseQuery(["quick", "fox"], gaps=2),
        PhraseQuery([{"quick", "fast"}, {"brown", "fox"}], gaps=1),
        PhraseQuery(["a", "a"]),
    ):
        rep = verify_snapshot(idx.snapshot(), q)
        print(
            f"  {q.elements} gaps={q.gaps}: ok={rep.ok} docs={rep.docs_checked} "
            f"matches={rep.matches_actual} blocks_decoded={rep.blocks_decoded}"
        )

    print("\n== high-frequency repeated term (decode block accounting) ==")
    hot = Index()
    hot.add_document("hot", {"body": " ".join(["a"] * 500)})
    counter = DecodeCounter()
    matches = hot.search(PhraseQuery(["a", "a"]), counter=counter)
    print(f"  'a a' over 500 x 'a': matches={len(matches)} {counter}")
    rep = verify_snapshot(hot.snapshot(), PhraseQuery(["a", "a"]))
    print(f"  verification: ok={rep.ok} expected={rep.matches_expected} actual={rep.matches_actual}")


if __name__ == "__main__":
    main()
