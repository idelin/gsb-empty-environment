#!/usr/bin/env python3
"""Run the local fixture and print the published per-transaction changelog.

Usage:  python3 demo.py [path/to/fixture.json]
"""

from __future__ import annotations

import sys
from pathlib import Path

from joinview import JoinChangelog, LeftOuterJoinView, load_fixture

DEFAULT_FIXTURE = Path(__file__).parent / "fixtures" / "changes.json"


def _fmt(row: dict) -> str:
    return "{" + ", ".join(f"{k}={v!r}" for k, v in sorted(row.items())) + "}"


def main(path: Path = DEFAULT_FIXTURE) -> int:
    join_cfg, changes = load_fixture(path)
    view = LeftOuterJoinView(
        right_columns=join_cfg.get("right_columns", ()),
        left_join_key=join_cfg.get("left_key", "k"),
        right_join_key=join_cfg.get("right_key", "k"),
    )
    processor = JoinChangelog(view)

    def on_commit(commit) -> None:
        print(
            f"commit {commit.commit_seq} ({commit.txn_id}): "
            f"{len(commit.retractions)} retraction(s), "
            f"{len(commit.additions)} addition(s)"
        )
        for delta in commit.changes:
            marker = "-" if delta.sign < 0 else "+"
            print(f"    {marker} {_fmt(delta.row)}")

    processor.subscribe(on_commit)

    print(f"Loaded {len(changes)} change record(s) from {path}")
    processor.ingest_all(changes)

    print("\nFinal materialised LEFT OUTER JOIN view:")
    for row in processor.snapshot():
        print(f"  {_fmt(row)}")
    return 0


if __name__ == "__main__":
    fixture = Path(sys.argv[1]) if len(sys.argv) > 1 else DEFAULT_FIXTURE
    raise SystemExit(main(fixture))
