"""Run the scanner against the synthetic reference document and a
streaming sample, then print redacted reports and a recall check.

Usage: python3.11 run_sample.py
"""

from __future__ import annotations

import json

from sensiscan import ScanBudget, Scanner, StreamScanner
from sensiscan import synth


def get_by_path(doc, path):
    node = doc
    for seg in [s for s in path.split("/") if s != ""]:
        node = node[int(seg)] if isinstance(node, list) else node[seg]
    return node


def recall_check(report, annotations, doc):
    found = {(e.path, e.rule_id) for e in report.evidence}
    missing = [a for a in annotations if (a.path, a.rule_id) not in found]
    print(f"recall: {len(annotations) - len(missing)}/{len(annotations)} annotations found")
    for a in missing:
        print(f"  MISSING {a.rule_id} at {a.path}")
    return not missing


def main():
    doc, annotations, whitelisted, controls = synth.build_reference_document()
    scanner = Scanner(
        dict_rules=synth.dictionary_rules(),
        field_rules=synth.field_rules(),
        whitelist=synth.whitelist(),
    )

    print("=== tree scan (synthetic reference document) ===")
    report = scanner.scan_tree(doc)
    print(report.to_json(indent=2))
    print()
    ok = recall_check(report, annotations, doc)
    print(f"whitelist: {report.stats['whitelisted_count']} emissions suppressed "
          f"({whitelisted[0].path} exempt for known-ssn v3 only)")
    print(f"controls not flagged: {all(c not in json.dumps(report.to_dict()) for c in controls)}")
    print()

    print("=== streaming scan (chunk size 7, secrets cross boundaries) ===")
    text, chunks, expected = synth.build_stream_sample()
    stream = StreamScanner(scanner)
    for chunk in chunks:
        stream.feed(chunk)
    sreport = stream.finish()
    print(sreport.to_json(indent=2))
    raw = text.encode("utf-8")
    for e in sreport.evidence:
        bs, be = e.byte_range
        sliced = raw[bs:be].decode("utf-8")
        print(f"  {e.rule_id}: byte_range={e.byte_range} slices back to {sliced!r} "
              f"(expected {expected[e.rule_id]!r}: {sliced == expected[e.rule_id]})")
    print()

    print("=== budget exhaustion (max_evidence=2) ===")
    limited = Scanner(
        dict_rules=synth.dictionary_rules(),
        field_rules=synth.field_rules(),
        whitelist=synth.whitelist(),
        budget=ScanBudget(max_evidence=2),
    )
    breport = limited.scan_tree(doc)
    print(f"status={breport.status} reason={breport.incomplete_reason!r} "
          f"evidence={len(breport.evidence)}")
    print()
    print("overall recall check:", "PASS" if ok else "FAIL")


if __name__ == "__main__":
    main()
