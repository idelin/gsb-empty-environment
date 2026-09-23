"""Run sample: replay a local change fixture through the grouped TopK index.

Usage: python3.11 main.py
"""
import json
import os
import tempfile

from topk import FieldSpec, SortSpec, TopKIndex

HERE = os.path.dirname(os.path.abspath(__file__))


def load_fixture(path):
    with open(path, encoding="utf-8") as fh:
        return [json.loads(line) for line in fh if line.strip()]


def fmt_top(entries):
    return ["%s(score=%s,name=%s)" % (e.id, e.values.get("score"),
                                      e.values.get("name")) for e in entries]


def main():
    events = load_fixture(os.path.join(HERE, "fixtures", "changes.jsonl"))
    spec = SortSpec([FieldSpec("score", desc=True, nulls_last=True),
                     FieldSpec("name", desc=False, nulls_last=True)])
    tmp = tempfile.mkdtemp(prefix="topk-demo-")
    index = TopKIndex(spec, k=3, group_field="grp", table="scores",
                      storage_dir=tmp, window_size=4, page_size=4)

    commits = {}
    for ev in events:
        commits.setdefault(ev["commit_seq"], []).append(ev)

    print("== apply commits, stable-id diff per commit ==")
    snap1 = None
    for seq in sorted(commits):
        diff = index.apply_batch(commits[seq])
        print("commit %d: %s" % (seq, json.dumps(diff, default=repr)))
        if seq == 4:
            snap1 = index.snapshot()  # fixed-version snapshot mid-stream

    print("\n== final TopK per group (k=3) ==")
    for group, entries in index.topk().items():
        print("group %s: %s" % (group, fmt_top(entries)))

    print("\n== snapshot pagination (fixed version) ==")
    print("snap1 version=%s page(A,0,2)=%s"
          % (snap1.version, [r["id"] for r in snap1.page("A", 0, 2)]))
    snap2 = index.snapshot()
    print("snap2 version=%s page(A,0,2)=%s"
          % (snap2.version, [r["id"] for r in snap2.page("A", 0, 2)]))
    print("snap1 unchanged after later commits: page(A,0,3)=%s"
          % [r["id"] for r in snap1.page("A", 0, 3)])

    print("\n== block access stats (segment page I/O) ==")
    print(index.stats.snapshot())

    print("\n== recovery round-trip ==")
    index.save()
    restored = TopKIndex.load(tmp)
    same = all([e.id for e in restored.topk(g)] == [e.id for e in es]
               for g, es in index.topk().items())
    print("restored topk identical:", same)
    print("restored commit_seq:", restored.commit_seq)

    print("\n== segmented spill + bounded delete backfill ==")
    bulk = [{"tx_id": "b", "table": "scores", "pk": "b%04d" % i,
             "before": None,
             "after": {"grp": "bulk", "score": i, "name": "x"},
             "commit_seq": 7} for i in range(500)]
    index.apply_batch(bulk)
    pages = len(index.groups["bulk"].pages)
    top = [e.id for e in index.groups["bulk"].topk(4)]
    index.stats.reset()
    index.apply_batch([{"tx_id": "b", "table": "scores", "pk": top[0],
                        "before": {"grp": "bulk", "score": 0, "name": "x"},
                        "after": None, "commit_seq": 8}])
    print("group 'bulk' spilled to %d pages; delete rank-1 -> new top3=%s"
          % (pages, [e.id for e in index.topk("bulk")]))
    print("blocks touched by that delete:", index.stats.snapshot())


if __name__ == "__main__":
    main()
