"""Command line interface.

The alphabet and the transition structure are read explicitly from JSON;
missing transitions are completed with a dead state automatically.
"""

from __future__ import annotations

import argparse
import json
import sys

from .core import build_dfa, minimize, prepare
from .verify import counterexample_script, product_verify


def dfa_from_json(data: dict):
    return build_dfa(
        alphabet=data["alphabet"],
        states=data["states"],
        transitions=data.get("transitions", {}),
        start=data["start"],
        accepting=data.get("accepting", []),
    )


def _load(path: str):
    with open(path, encoding="utf-8") as fh:
        return dfa_from_json(json.load(fh))


def _qsort_key(name: str):
    return int(name[1:]) if name.startswith("q") and name[1:].isdigit() else name


def render_certificate(cert) -> str:
    lines = []
    alpha = ", ".join(cert.prepared.alphabet) or "(empty)"
    lines.append(f"Alphabet: {{{alpha}}}")
    if cert.dead_state is not None:
        lines.append(f"Missing transitions completed with dead state: {cert.dead_state}")
    else:
        lines.append("No missing transitions; no dead state needed.")
    if cert.removed_unreachable:
        lines.append(f"Removed unreachable states: {', '.join(cert.removed_unreachable)}")
    else:
        lines.append("All states reachable; nothing removed.")
    lines.append(f"Prepared DFA states: {len(cert.prepared.states)}")
    lines.append("")
    lines.append("Old -> new state mapping:")
    for old in sorted(cert.mapping):
        lines.append(f"  {old} -> {cert.mapping[old]}")
    lines.append("")
    lines.append("Quotient DFA (canonical naming):")
    lines.append(f"  start: {cert.quotient.start}")
    acc = ", ".join(sorted(cert.quotient.accepting, key=_qsort_key)) or "(none)"
    lines.append(f"  accepting: {acc}")
    for s in sorted(cert.quotient.states, key=_qsort_key):
        row = ", ".join(
            f"{a} -> {cert.quotient.transitions[s][a]}" for a in cert.quotient.alphabet
        )
        lines.append(f"  {s}: {row or '(no outgoing transitions)'}")
    lines.append("")
    lines.append("Language-equivalence partition certificate:")
    groups: dict[str, list[str]] = {}
    for old, new in cert.mapping.items():
        groups.setdefault(new, []).append(old)
    for new in sorted(groups, key=_qsort_key):
        lines.append(f"  block {new}: {{{', '.join(sorted(groups[new]))}}}")
    if cert.witnesses:
        lines.append("  Shortest distinguishing strings for every pair of quotient states:")
        for (x, y), w in sorted(cert.witnesses.items(),
                                key=lambda kv: (_qsort_key(kv[0][0]), _qsort_key(kv[0][1]))):
            lines.append(f"    {x} vs {y}: {w!r}")
    else:
        lines.append("  Single block: no distinguishing strings needed.")
    return "\n".join(lines)


def cmd_minimize(args) -> int:
    dfa = _load(args.file)
    cert = minimize(dfa)
    print(render_certificate(cert))
    ver = product_verify(cert.prepared, cert.quotient)
    print()
    print("Independent product-search verification (prepared DFA vs quotient):")
    print(f"  state pairs explored: {ver.pairs_explored}")
    if ver.equivalent:
        print("  result: EQUIVALENT")
        return 0
    print(f"  result: NOT EQUIVALENT, witness {ver.witness!r}")
    return 1


def cmd_verify(args) -> int:
    dfa1 = prepare(_load(args.file1))
    dfa2 = prepare(_load(args.file2))
    try:
        ver = product_verify(dfa1, dfa2)
    except ValueError as exc:
        print(f"error: {exc}")
        return 2
    print(f"state pairs explored: {ver.pairs_explored}")
    if ver.equivalent:
        print("result: EQUIVALENT")
        return 0
    print(f"result: NOT EQUIVALENT, shortest witness {ver.witness!r}")
    if args.output:
        script = counterexample_script(dfa1, dfa2, ver.witness,
                                       name1=args.file1, name2=args.file2)
        with open(args.output, "w", encoding="utf-8") as fh:
            fh.write(script)
        print(f"standalone counterexample script written to: {args.output}")
    return 1


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        prog="dfa-min",
        description="Minimize a DFA and certify the language-equivalence partition.",
    )
    sub = parser.add_subparsers(dest="command", required=True)
    p_min = sub.add_parser("minimize", help="minimize a DFA given as JSON")
    p_min.add_argument("file", help="JSON file with alphabet/states/transitions/start/accepting")
    p_ver = sub.add_parser("verify", help="product-search equivalence of two DFAs")
    p_ver.add_argument("file1")
    p_ver.add_argument("file2")
    p_ver.add_argument("-o", "--output",
                       help="write a standalone counterexample script when inequivalent")
    args = parser.parse_args(argv)
    if args.command == "minimize":
        return cmd_minimize(args)
    return cmd_verify(args)


if __name__ == "__main__":
    sys.exit(main())
