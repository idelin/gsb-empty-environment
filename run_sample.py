#!/usr/bin/env python3
"""Sample run over the synthetic reference corpus (synthetic data only).

Prints the redacted scan report and cross-checks it against the
independent hand-written annotations in ``reference_corpus``.
"""
import reference_corpus as corpus
from sensiscan.scanner import scan_document


def main() -> int:
    report = scan_document(corpus.DOC, corpus.RULES, corpus.WHITELIST, chunk_size=8)
    print("=== redacted scan report ===")
    print(report.to_json())

    print("\n=== cross-check against independent annotations ===")
    found = {}
    for ev in report.evidence:
        key = (ev.path, ev.rule_id)
        found[key] = found.get(key, 0) + 1
    ok = report.complete
    for exp in corpus.EXPECTED:
        key = (exp["path"], exp["rule_id"])
        got = found.get(key, 0)
        status = "ok" if got == exp["count"] else "MISMATCH"
        if got != exp["count"]:
            ok = False
        print(f"  [{status}] {key[0]:<22} {key[1]:<12} expected={exp['count']} got={got}")
    unexpected = set(found) - {(e["path"], e["rule_id"]) for e in corpus.EXPECTED}
    if unexpected:
        ok = False
        print("  [MISMATCH] unexpected evidence:", sorted(unexpected))
    if report.suppressed != corpus.EXPECTED_SUPPRESSED:
        ok = False
        print(
            f"  [MISMATCH] suppressed: expected {corpus.EXPECTED_SUPPRESSED}, "
            f"got {report.suppressed}"
        )
    print(
        f"result: {'OK' if ok else 'FAILED'} "
        f"(evidence={len(report.evidence)}, suppressed={report.suppressed}, "
        f"complete={report.complete})"
    )
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
