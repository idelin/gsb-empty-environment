"""Runnable demo: synthetic UTF-8 documents -> TopKIndex.

Run with:  python3.11 demo.py
"""

from topk import StaleCursorError, TopKIndex, tokenize

DOCS = [
    "Apple pie, apple tart. APPLE pie! An apple a day keeps the doctor away.",
    "Application forms apply apples. APPle APP APP app.",
    "Banana bread, banana split. Band of bananas. BAND!",
    " café CAFÉ Café — naïve, NAÏVE. Straße und STRASSE. ",
    "北京 北京 上海 广州 北京 北京。apple 苹果，Apple 苹果。",
    "app apple application apply apex apparat. The apple appears again: APPLE!",
]


def show(title, suggestions):
    print(f"\n== {title} ==")
    if not suggestions:
        print("  (no suggestions)")
    for rank, s in enumerate(suggestions, 1):
        print(f"  {rank:2d}. {s.display:<14} word={s.word:<14} freq={s.freq}")


def main():
    print("== Tokenization (display -> normalized) ==")
    sample = "Straße, APPLE! naïve_1 北京。"
    print("  ", " | ".join(f"{t.display}->{t.normalized}" for t in tokenize(sample)))

    idx = TopKIndex(topk_capacity=4)  # small cache: forces best-first paths
    for doc in DOCS:
        idx.load_text(doc)

    show("Top-5 for prefix 'app' (case-insensitive)", idx.suggest("APP", 5))
    show("Prefix 'app' again: 'app' is a prefix of 'apple' etc.", idx.suggest("app", 6))
    show("Empty prefix, capped at limit=5", idx.suggest("", 5))
    show("UTF-8 prefix 'stra' (Straße folds to 'strasse')", idx.suggest("stra", 3))
    show("CJK prefix '北'", idx.suggest("北", 3))

    print("\n== Rank-cursor pagination over prefix 'a', page size 3 ==")
    after, page_no = None, 1
    while True:
        page = idx.suggest("a", 3, after=after)
        if not page:
            break
        show(f"page {page_no}", page)
        after = (page[-1].freq, page[-1].word)
        page_no += 1

    print("\n== Frequency decrease backfills the next candidate ==")
    show("before: prefix 'ban'", idx.suggest("ban", 3))
    idx.set_freq("banana", 1)
    show("after set_freq('banana', 1)", idx.suggest("ban", 3))

    print("\n== Deleting the top word backfills the next one ==")
    show("before: prefix 'app'", idx.suggest("app", 3))
    idx.remove("apple")
    show("after remove('apple')", idx.suggest("app", 3))

    print("\n== Memory compaction and cursor versioning ==")
    stats_before = idx.stats()
    cursor = idx.open_cursor("app")
    strict_cursor = idx.open_cursor("app", strict=True)
    snapshot = cursor.read(3)
    idx.compact()
    idx.set_freq("application", 999)  # mutate after compaction
    stats_after = idx.stats()
    print(f"  stats before compact: {stats_before}")
    print(f"  stats after  compact: {stats_after}")
    show("non-strict cursor still reads the OLD version", cursor.read(3))
    try:
        strict_cursor.read(3)
        print("  strict cursor: unexpectedly succeeded")
    except StaleCursorError as exc:
        print(f"  strict cursor raised StaleCursorError: {exc}")
    show("fresh query sees the new version", idx.suggest("app", 3))


if __name__ == "__main__":
    main()
