"""End-to-end demo: out-of-order events, duplicates, deletes, restart.

Run:  python3 demo.py
"""
from __future__ import annotations

import shutil
from pathlib import Path

from indexing import Event, IndexingPipeline, tokenize

STATE_DIR = Path(__file__).parent / "demo_state"

DOCS = {
    "doc-1": "Hello world, hello Python 世界",
    "doc-2": "Python 3.11 支持原子提交吗？支持！",
    "doc-3": "Search engine index, 不用外部搜索库。",
}


def show_search(p: IndexingPipeline, term: str) -> None:
    print(f"  search({term!r}) -> {p.search(term)}")


def main() -> None:
    shutil.rmtree(STATE_DIR, ignore_errors=True)
    p = IndexingPipeline(STATE_DIR)

    print("== 1. Tokenization (UTF-8, local synthetic docs) ==")
    print(f"  {DOCS['doc-1']!r} -> {tokenize(DOCS['doc-1'])}")
    print(f"  {DOCS['doc-2']!r} -> {tokenize(DOCS['doc-2'])}")

    print("\n== 2. Out-of-order submit: seq=2 arrives before seq=1 ==")
    r = p.submit(Event(seq=2, doc_id="doc-2", op="upsert", text=DOCS["doc-2"]))
    print(f"  submit seq=2 -> {r.status}, missing={r.missing}")
    r = p.submit(Event(seq=1, doc_id="doc-1", op="upsert", text=DOCS["doc-1"]))
    print(f"  submit seq=1 -> {r.status}, drained={r.drained}, missing={r.missing}")

    print("\n== 3. Duplicate event is dropped (idempotent) ==")
    r = p.submit(Event(seq=1, doc_id="doc-1", op="upsert", text=DOCS["doc-1"]))
    print(f"  re-submit seq=1 -> {r.status}")

    print("\n== 4. Newer delete (seq=4) arrives before seq=3: it waits ==")
    r = p.submit(Event(seq=4, doc_id="doc-1", op="delete"))
    print(f"  submit delete seq=4 -> {r.status}, missing={r.missing}")
    r = p.submit(Event(seq=3, doc_id="doc-3", op="upsert", text=DOCS["doc-3"]))
    print(f"  submit seq=3 -> {r.status}, drained={r.drained}")
    c = p.commit()
    print(f"  commit -> committed={c.committed}, checkpoint={c.checkpoint}, "
          f"commit_version={c.batch.commit_version}")
    show_search(p, "hello")   # doc-1 was upserted then deleted, in seq order
    show_search(p, "python")
    show_search(p, "支")      # CJK is tokenized as unigrams

    print("\n== 5. Newer upsert for doc-1 (seq=5) brings it back ==")
    p.submit(Event(seq=5, doc_id="doc-1", op="upsert", text="Hello 世界 hello"))
    c = p.commit()
    print(f"  commit -> committed={c.committed}, checkpoint={c.checkpoint}")
    show_search(p, "hello")
    show_search(p, "世")

    print("\n== 6. Restart: state reloads, frequencies unchanged, replays dropped ==")
    before = p.stats()
    p2 = IndexingPipeline(STATE_DIR)
    after = p2.stats()
    print(f"  stats before restart: {before}")
    print(f"  stats after  restart: {after}")
    r = p2.submit(Event(seq=3, doc_id="doc-3", op="upsert", text=DOCS["doc-3"]))
    print(f"  replay seq=3 after restart -> {r.status}")
    p2.commit()
    show_search(p2, "hello")
    assert p2.search("hello") == p.search("hello"), "restart must not double-count"
    print("  OK: term frequencies identical after restart")

    print("\n== 7. Missing seq is reported explicitly ==")
    r = p2.submit(Event(seq=8, doc_id="doc-4", op="upsert", text="gap test"))
    print(f"  submit seq=8 while checkpoint={p2.checkpoint} -> {r.status}, missing={r.missing}")
    print(f"  pipeline.missing_seqs() -> {p2.missing_seqs()}")


if __name__ == "__main__":
    main()
