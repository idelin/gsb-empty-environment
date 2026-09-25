"""Driver: optimize a source file, run both versions, verify the contract.

Usage: python3.11 driver.py FILE [--json]
"""
from __future__ import annotations

import json
import sys

from interp import run
from lang_parser import parse
from nodes import Block, ExprStmt, If, Let, Print
from optimize import optimize_program, unparse


def show_program(stmts, indent=0):
    lines = []
    pad = "  " * indent
    for stmt in stmts:
        if isinstance(stmt, Let):
            lines.append("%slet %s = %s;" % (pad, stmt.name,
                                             unparse(stmt.value)))
        elif isinstance(stmt, Print):
            lines.append("%sprint %s;" % (pad, unparse(stmt.value)))
        elif isinstance(stmt, ExprStmt):
            lines.append("%s%s;" % (pad, unparse(stmt.value)))
        elif isinstance(stmt, If):
            lines.append("%sif %s {" % (pad, unparse(stmt.cond)))
            lines.extend(show_program(stmt.then_body, indent + 1))
            if stmt.else_body:
                lines.append("%s} else {" % pad)
                lines.extend(show_program(stmt.else_body, indent + 1))
            lines.append("%s}" % pad)
        elif isinstance(stmt, Block):
            lines.append("%s{" % pad)
            lines.extend(show_program(stmt.body, indent + 1))
            lines.append("%s}" % pad)
    return lines


def describe_run(out, err, src_bytes):
    lines = ["  outputs: %s" % (out if out else "[]")]
    if err is not None:
        s, e = err.span
        snippet = src_bytes[s:e].decode("utf-8", "replace")
        lines.append("  error:   %s  at bytes [%d, %d)  ->  %r"
                     % (err.message, s, e, snippet))
    else:
        lines.append("  error:   <none>")
    return lines


def main(argv):
    if len(argv) < 2:
        print(__doc__)
        return 2
    path = argv[1]
    as_json = "--json" in argv
    src_bytes = open(path, "rb").read()
    src = src_bytes.decode("utf-8")

    original = parse(src)
    optimized, rationale = optimize_program(parse(src))

    out0, err0 = run(original)
    out1, err1 = run(optimized)

    print("== source: %s ==" % path)
    print(src.rstrip())
    print()
    print("== optimized program ==")
    for line in show_program(optimized.body):
        print(line)
    print()
    print("== rationale map (%d entries) ==" % len(rationale))
    if as_json:
        print(json.dumps(rationale, ensure_ascii=False, indent=2))
    else:
        for i, r in enumerate(rationale):
            s, e = r["span"]
            snippet = src_bytes[s:e].decode("utf-8", "replace")
            print("  [%d] %s  bytes [%d, %d) %r" % (i, r["rule"], s, e,
                                                    snippet))
            print("      before: %s" % r["before"])
            print("      after:  %s" % r["after"])
            print("      reason: %s" % r["reason"])
    print()
    print("== run: original ==")
    for line in describe_run(out0, err0, src_bytes):
        print(line)
    print("== run: optimized ==")
    for line in describe_run(out1, err1, src_bytes):
        print(line)
    print()
    same_out = out0 == out1
    same_err = ((err0 is None) == (err1 is None)
                and (err0 is None
                     or (err0.message == err1.message
                         and err0.span == err1.span)))
    ok = same_out and same_err
    print("== contract check ==")
    print("  outputs identical:            %s" % same_out)
    print("  error message+span identical: %s" % same_err)
    print("  RESULT: %s" % ("PASS" if ok else "FAIL"))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
