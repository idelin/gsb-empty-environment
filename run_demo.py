"""Demo: boundary-aware compaction + equivalence certificates.

Run:  python3 run_demo.py
"""

import json
import os

from compactor import (
    build_certificate,
    check_equivalence,
    compact,
    exhaustive_verify,
    find_counterexample,
    load_fixture,
    verify_certificate,
)

FIX = os.path.join(os.path.dirname(__file__), "fixtures")


def show(title):
    print(f"\n=== {title} ===")


def fmt(changes):
    if not changes:
        return "  <empty>"
    return "\n".join(
        f"  [{c.tx_id}@c{c.commit_seq} {c.schema}] {c.table}/{c.pk}: "
        f"{c.before!r} -> {c.after!r}" for c in changes
    )


def main():
    show("1. A->B->C within one transaction merges to A->C")
    changes, visible = load_fixture(os.path.join(FIX, "merge_within_tx.json"))
    out = compact(changes, visible)["output"]
    print("input:\n" + fmt(changes))
    print("output:\n" + fmt(out))

    show("2. insert+delete annihilates; audit mode retains evidence")
    changes, visible = load_fixture(os.path.join(FIX, "insert_then_delete.json"))
    plain = compact(changes, visible)
    audited = compact(changes, visible, audit=True)
    print("output (no audit):\n" + fmt(plain["output"]))
    print("audit evidence:", json.dumps(audited["audit_log"], ensure_ascii=False))

    show("3. delete+recreate is an identity barrier (no merge)")
    changes, visible = load_fixture(os.path.join(FIX, "delete_recreate.json"))
    out = compact(changes, visible)["output"]
    print("output:\n" + fmt(out))

    show("4. visible commit point blocks cross-commit merge")
    changes, visible = load_fixture(os.path.join(FIX, "cross_commit_visible.json"))
    out = compact(changes, visible)["output"]
    print(f"visible={visible} -> {len(out)} changes kept")
    changes, visible = load_fixture(os.path.join(FIX, "cross_commit_invisible.json"))
    out = compact(changes, visible)["output"]
    print(f"visible={visible} -> merged:")
    print(fmt(out))

    show("5. schema barrier blocks merge")
    changes, visible = load_fixture(os.path.join(FIX, "schema_barrier.json"))
    out = compact(changes, visible)["output"]
    print(f"output keeps {len(out)} changes (schema v1 and v2 not merged)")

    show("6. budget exceeded: remainder stays uncompressed")
    changes, visible = load_fixture(os.path.join(FIX, "budget.json"))
    res = compact(changes, visible, budget=1)
    print(f"merges used: {res['merges']} (budget 1)")
    print("output:\n" + fmt(res["output"]))

    show("7. equivalence certificate: build, serialize, independently verify")
    changes, visible = load_fixture(os.path.join(FIX, "aggregates.json"))
    cert = build_certificate(changes, visible, audit=True)
    blob = json.dumps(cert, ensure_ascii=False, indent=1)
    print(f"certificate: {len(cert['input'])} inputs -> {len(cert['output'])} "
          f"outputs, equivalent={cert['equivalent']}")
    print("checkpoints (visible state + incremental aggregates):")
    for cp in cert["checkpoints"]:
        print(f"  commit {cp['commit_seq']}: state={cp['state']} "
              f"aggregates={cp['aggregates']}")
    reloaded = json.loads(blob)
    print("independent replay verification:", verify_certificate(reloaded))
    tampered = json.loads(blob)
    tampered["output"][0]["after"] = 999
    try:
        verify_certificate(tampered)
        print("tamper check: NOT detected (bad!)")
    except Exception as exc:
        print(f"tamper check: detected -> {type(exc).__name__}: {exc}")

    show("8. counterexample: same final state, different intermediate semantics")
    ce = find_counterexample()
    print(f"final state A == final state B: "
          f"{ce['final_state_a'] == ce['final_state_b']} ({ce['final_state_a']})")
    print(f"naive cross-commit merge equivalent: {ce['naive_merge_equivalent']}")
    for d in ce["naive_merge_diffs"]:
        print("  diff:", d)
    print(f"boundary-aware compaction equivalent: "
          f"{ce['boundary_aware_equivalent']} "
          f"({len(ce['boundary_aware_output'])} changes kept)")

    show("9. exhaustive verification of small change sequences")
    summary = exhaustive_verify(max_len=3, values=(0, 1), commits=(1, 2))
    print(f"cases checked: {summary['cases']}")
    print(f"boundary-aware failures: {len(summary['failures'])}")
    print(f"naive compaction violations caught: {summary['naive_violations']}")


if __name__ == "__main__":
    main()
