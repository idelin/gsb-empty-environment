"""Runnable sample: compact local fixtures, emit and verify certificates."""

from __future__ import annotations

import json
import sys

from .certificate import (
    assert_inequivalent,
    build_certificate,
    verify_certificate,
)
from .compaction import compact
from .changelog import load_changes
from .fixtures import FIXTURES, get_fixture


def _show(title: str, value: object) -> None:
    print(f"\n=== {title} ===")
    print(json.dumps(value, indent=2, ensure_ascii=False, sort_keys=True))


def main(argv: list[str] | None = None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    if argv and argv[0] == "verify":
        if len(argv) != 2:
            print("usage: python -m changelog_compaction.demo verify CERT.json")
            return 2
        with open(argv[1], "r", encoding="utf-8") as handle:
            cert = json.load(handle)
        ok, problems = verify_certificate(cert)
        print("certificate valid" if ok else "certificate INVALID")
        for problem in problems:
            print(" -", problem)
        return 0 if ok else 1
    if argv and argv[0] == "emit":
        if len(argv) not in (2, 3):
            print("usage: python -m changelog_compaction.demo emit NAME [OUT.json]")
            return 2
        name = argv[1]
        cert = build_certificate(get_fixture(name), audit=True)
        text = json.dumps(cert, indent=2, ensure_ascii=False, sort_keys=True)
        if len(argv) == 3:
            with open(argv[2], "w", encoding="utf-8") as handle:
                handle.write(text + "\n")
            print(f"wrote {argv[2]}")
        else:
            print(text)
        return 0

    print("# Change-log compaction demo (Python", sys.version.split()[0] + ")")

    for name, raw in FIXTURES.items():
        cert = build_certificate(raw, audit=True)
        ok, problems = verify_certificate(cert)
        print(
            f"fixture={name:<26} raw={len(cert['raw_changes'])} "
            f"compacted={len(cert['compacted_changes'])} "
            f"elisions={len(cert['elisions'])} "
            f"merges={cert['merges_used']} verify={'OK' if ok else problems}"
        )

    # A -> B -> C merges to A -> C.
    _show(
        "A->B->C merges to A->C",
        [
            {
                "op": change.op,
                "before": change.before,
                "after": change.after,
                "idx": change.idx,
            }
            for change in compact(load_changes(get_fixture("abc_merge"))).changes
        ],
    )

    # Insert then delete cancels; audit mode keeps the evidence payloads.
    result = compact(load_changes(get_fixture("insert_delete_cancel")), audit=True)
    _show(
        "insert->delete cancels, audit evidence retained",
        {
            "compacted": [],
            "elisions": [elision.to_dict() for elision in result.elisions],
        },
    )

    # Budget exhausted: only uncompacted fragments survive.
    budgeted = build_certificate(get_fixture("mixed"), budget=1)
    _show(
        "budget=1: remainder emitted uncompacted",
        {
            "merges_used": budgeted["merges_used"],
            "budget_exhausted": budgeted["budget_exhausted"],
            "compacted": budgeted["compacted_changes"],
        },
    )

    # Counter-example 1: hiding an intermediate version across commit points.
    raw = get_fixture("cross_commit_visible")
    illegal_cross_commit = [
        {"idx": 0, "txn_id": "t4", "table": "ledger", "key": "k4",
         "before": None, "after": "C", "commit_seq": 1},
    ]
    _show(
        "COUNTER-EXAMPLE 1: cross-commit merge hides visible version",
        assert_inequivalent(raw, illegal_cross_commit),
    )

    # Counter-example 2: delete/recreate forged as an update loses identity.
    raw = get_fixture("delete_recreate_identity")
    forged_update = [
        {"idx": 0, "txn_id": "t3a", "table": "users", "key": "k3",
         "before": None, "after": "A", "commit_seq": 1},
        {"idx": 1, "txn_id": "t3b", "table": "users", "key": "k3",
         "before": "A", "after": "C", "commit_seq": 2},
    ]
    _show(
        "COUNTER-EXAMPLE 2: same final value, different identity/aggregates",
        assert_inequivalent(raw, forged_update),
    )

    # Full independently replayable certificate sample.
    _show(
        "equivalence certificate (self-contained, replayable)",
        build_certificate(get_fixture("mixed"), audit=True),
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
